package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type AbuseIPDBResponse struct {
	Data struct {
		AbuseConfidenceScore int `json:"abuseConfidenceScore"`
	} `json:"data"`
}

type CacheEntry struct {
	Score     int       `json:"score"`
	Timestamp time.Time `json:"timestamp"`
}

type Cache struct {
	Entries map[string]CacheEntry `json:"entries"`
	mu      sync.RWMutex
}

var (
	abuseIPCache     = Cache{Entries: make(map[string]CacheEntry)}
	abuseLoadOnce    sync.Once
	abuseIPDBBaseURL = "https://api.abuseipdb.com"
	abuseIPDBClient  = &http.Client{Timeout: 10 * time.Second}
	errAbuseService  = errors.New("service temporarily unavailable")
)

func initAbuseIPCache(cacheFile string) {
	abuseLoadOnce.Do(func() {
		data, err := os.ReadFile(cacheFile)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("failed to read abuse cache", "file", cacheFile, "err", err)
			}

			return
		}

		abuseIPCache.mu.Lock()
		defer abuseIPCache.mu.Unlock()

		if err := json.Unmarshal(data, &abuseIPCache); err != nil {
			slog.Warn("failed to parse abuse cache", "file", cacheFile, "err", err)
			abuseIPCache.Entries = make(map[string]CacheEntry)
		}
	})
}

func checkAbuseIPDB(ip string, config AbuseIPDBConfig) error {
	initAbuseIPCache(config.CacheFile)

	abuseIPCache.mu.RLock()
	entry, exists := abuseIPCache.Entries[ip]
	abuseIPCache.mu.RUnlock()

	if exists && time.Since(entry.Timestamp) <= 24*time.Hour {
		if entry.Score > 50 {
			return fmt.Errorf("IP %s blocked (abuse score: %d)", ip, entry.Score)
		}

		return nil
	}

	params := url.Values{"ipAddress": {ip}, "maxAgeInDays": {"90"}}
	req, err := http.NewRequest(http.MethodGet, abuseIPDBBaseURL+"/api/v2/check?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("AbuseIPDB request creation failed: %w", err)
	}

	req.Header.Set("Key", config.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := abuseIPDBClient.Do(req)
	if err != nil {
		return fmt.Errorf("AbuseIPDB request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: AbuseIPDB returned %d", errAbuseService, resp.StatusCode)
	}

	var result AbuseIPDBResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("AbuseIPDB decode failed: %w", err)
	}

	abuseIPCache.mu.Lock()
	abuseIPCache.Entries[ip] = CacheEntry{Score: result.Data.AbuseConfidenceScore, Timestamp: time.Now()}
	data, merr := json.MarshalIndent(&abuseIPCache, "", "  ")
	abuseIPCache.mu.Unlock()

	if merr != nil {
		slog.Warn("failed to marshal abuse cache", "err", merr)
	} else if err := os.WriteFile(config.CacheFile, data, 0644); err != nil {
		slog.Warn("failed to write abuse cache", "file", config.CacheFile, "err", err)
	}

	if result.Data.AbuseConfidenceScore > 50 {
		return fmt.Errorf("IP %s blocked (abuse score: %d)", ip, result.Data.AbuseConfidenceScore)
	}

	return nil
}
