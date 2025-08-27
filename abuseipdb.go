package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

type AbuseIPDBResponse struct {
	Data struct {
		AbuseConfidenceScore int `json:"abuseConfidenceScore"`
	} `json:"data"`
}

// CacheEntry stores an AbuseIPDB result with a timestamp
type CacheEntry struct {
	Score     int       `json:"score"`
	Timestamp time.Time `json:"timestamp"`
}

// Cache stores IP results with thread-safe access
type Cache struct {
	Entries map[string]CacheEntry `json:"entries"`
	mu      sync.RWMutex
}

func checkAbuseIPDB(ip string) error {
	const apiKey = "secrets"
	const cacheFile = "cache.json"
	var cache Cache

	// Load cache (lazy initialization)
	cache.mu.Lock()
	if _, err := os.Stat(cacheFile); err == nil {
		data, err := os.ReadFile(cacheFile)
		if err != nil {
			cache.mu.Unlock()
			fmt.Printf("Failed to read cache file: %v\n", err)
		} else {
			if err := json.Unmarshal(data, &cache); err != nil {
				cache.mu.Unlock()
				fmt.Printf("Failed to unmarshal cache: %v\n", err)
			} else {
				if cache.Entries == nil {
					cache.Entries = make(map[string]CacheEntry)
				}
			}
		}
	} else {
		cache.Entries = make(map[string]CacheEntry)
	}
	cache.mu.Unlock()

	// Check cache
	cache.mu.RLock()
	entry, exists := cache.Entries[ip]
	if exists && time.Since(entry.Timestamp) <= 24*time.Hour {
		cache.mu.RUnlock()
		if entry.Score > 50 {
			return fmt.Errorf("IP %s blocked due to high abuse confidence score: %d", ip, entry.Score)
		}
		return nil
	}
	cache.mu.RUnlock()

	// Query AbuseIPDB
	url := fmt.Sprintf("https://api.abuseipdb.com/api/v2/check?ipAddress=%s&maxAgeInDays=90", ip)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Key", apiKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("AbuseIPDB request failed: %v", err)
	}
	defer resp.Body.Close()

	var result AbuseIPDBResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("AbuseIPDB decode failed: %v", err)
	}

	// Update cache
	cache.mu.Lock()
	cache.Entries[ip] = CacheEntry{
		Score:     result.Data.AbuseConfidenceScore,
		Timestamp: time.Now(),
	}
	data, err := json.MarshalIndent(&cache, "", "  ")
	if err != nil {
		cache.mu.Unlock()
		fmt.Printf("Failed to marshal cache: %v\n", err)
	} else {
		if err := os.WriteFile(cacheFile, data, 0644); err != nil {
			cache.mu.Unlock()
			fmt.Printf("Failed to write cache file: %v\n", err)
		}
	}
	cache.mu.Unlock()

	if result.Data.AbuseConfidenceScore > 50 {
		return fmt.Errorf("IP %s blocked due to high abuse confidence score: %d", ip, result.Data.AbuseConfidenceScore)
	}
	return nil
}
