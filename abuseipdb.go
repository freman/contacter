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
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

type IPCache struct {
	Entries map[string]CacheEntry `json:"entries"`
	mu      sync.RWMutex
	file    string
}

var (
	ipCache          = IPCache{Entries: make(map[string]CacheEntry)}
	cacheLoadOnce    sync.Once
	abuseIPDBBaseURL = "https://api.abuseipdb.com"
	abuseIPDBClient  = &http.Client{Timeout: 10 * time.Second}
	errAbuseService  = errors.New("service temporarily unavailable")
)

func initIPCache(file string) {
	cacheLoadOnce.Do(func() {
		ipCache.file = file
		if file == "" {
			return
		}

		data, err := os.ReadFile(file)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("failed to read ip cache", "file", file, "err", err)
			}

			return
		}

		ipCache.mu.Lock()
		defer ipCache.mu.Unlock()

		if err := json.Unmarshal(data, &ipCache); err != nil {
			slog.Warn("failed to parse ip cache", "file", file, "err", err)
			ipCache.Entries = make(map[string]CacheEntry)
		}
	})
}

func recordIP(ip, source string, score int, cacheFile string) {
	initIPCache(cacheFile)

	ipCache.mu.Lock()
	ipCache.Entries[ip] = CacheEntry{Score: score, Source: source, Timestamp: time.Now()}
	data, merr := json.MarshalIndent(&ipCache, "", "  ")
	file := ipCache.file
	ipCache.mu.Unlock()

	if merr != nil {
		slog.Warn("failed to marshal ip cache", "err", merr)

		return
	}

	if file == "" {
		return
	}

	if err := os.WriteFile(file, data, 0644); err != nil {
		slog.Warn("failed to write ip cache", "file", file, "err", err)
	}
}

func checkIPCache(ip, cacheFile string) error {
	initIPCache(cacheFile)

	ipCache.mu.RLock()
	entry, exists := ipCache.Entries[ip]
	ipCache.mu.RUnlock()

	if exists && time.Since(entry.Timestamp) <= 24*time.Hour && entry.Score > 50 {
		slog.Info("ip blocked from local cache", "ip", ip, "score", entry.Score, "source", entry.Source)

		return fmt.Errorf("IP %s blocked (score: %d, source: %s)", ip, entry.Score, entry.Source)
	}

	return nil
}

func checkAbuseIPDB(ip string, config AbuseIPDBConfig, cacheFile string) error {
	initIPCache(cacheFile)

	ipCache.mu.RLock()
	entry, exists := ipCache.Entries[ip]
	ipCache.mu.RUnlock()

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

	recordIP(ip, "abuseipdb", result.Data.AbuseConfidenceScore, cacheFile)

	if result.Data.AbuseConfidenceScore > 50 {
		return fmt.Errorf("IP %s blocked (abuse score: %d)", ip, result.Data.AbuseConfidenceScore)
	}

	return nil
}
