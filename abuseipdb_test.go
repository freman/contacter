package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func resetIPCache() {
	ipCache = IPCache{Entries: make(map[string]CacheEntry)}
	cacheLoadOnce = sync.Once{}
}

func abuseIPDBServer(t *testing.T, score int) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": score},
		})
	}))
	t.Cleanup(srv.Close)

	return srv
}

func withMockAbuseIPDB(t *testing.T, srv *httptest.Server) {
	t.Helper()

	orig := abuseIPDBBaseURL
	abuseIPDBBaseURL = srv.URL
	t.Cleanup(func() { abuseIPDBBaseURL = orig })
}

func TestCheckAbuseIPDB_Blocked(t *testing.T) {
	resetIPCache()
	withMockAbuseIPDB(t, abuseIPDBServer(t, 75))

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	cfg := AbuseIPDBConfig{APIKey: "test-key"}

	if err := checkAbuseIPDB("1.2.3.4", cfg, cacheFile); err == nil {
		t.Error("checkAbuseIPDB() expected error for score 75, got nil")
	}
}

func TestCheckAbuseIPDB_Allowed(t *testing.T) {
	resetIPCache()
	withMockAbuseIPDB(t, abuseIPDBServer(t, 10))

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	cfg := AbuseIPDBConfig{APIKey: "test-key"}

	if err := checkAbuseIPDB("1.2.3.5", cfg, cacheFile); err != nil {
		t.Errorf("checkAbuseIPDB() unexpected error for score 10: %v", err)
	}
}

func TestCheckAbuseIPDB_Cached(t *testing.T) {
	resetIPCache()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": 10},
		})
	}))
	t.Cleanup(srv.Close)
	withMockAbuseIPDB(t, srv)

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	cfg := AbuseIPDBConfig{APIKey: "test-key"}

	if err := checkAbuseIPDB("1.2.3.6", cfg, cacheFile); err != nil {
		t.Fatal(err)
	}

	if err := checkAbuseIPDB("1.2.3.6", cfg, cacheFile); err != nil {
		t.Fatal(err)
	}

	if callCount != 1 {
		t.Errorf("API called %d times, want 1 (second call should use in-memory cache)", callCount)
	}
}

func TestCheckAbuseIPDB_WritesCache(t *testing.T) {
	resetIPCache()
	withMockAbuseIPDB(t, abuseIPDBServer(t, 20))

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	cfg := AbuseIPDBConfig{APIKey: "test-key"}

	if err := checkAbuseIPDB("1.2.3.7", cfg, cacheFile); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Error("cache file not created after API call")
	}
}

func TestCheckAbuseIPDB_SendsAPIKey(t *testing.T) {
	resetIPCache()

	var receivedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("Key")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": 0},
		})
	}))
	t.Cleanup(srv.Close)
	withMockAbuseIPDB(t, srv)

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	cfg := AbuseIPDBConfig{APIKey: "my-secret-key"}

	_ = checkAbuseIPDB("1.2.3.8", cfg, cacheFile)

	if receivedKey != "my-secret-key" {
		t.Errorf("API key sent = %q, want %q", receivedKey, "my-secret-key")
	}
}

func TestCheckAbuseIPDB_LoadsCache(t *testing.T) {
	resetIPCache()

	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": 0},
		})
	}))
	t.Cleanup(srv.Close)
	withMockAbuseIPDB(t, srv)

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	primed := IPCache{Entries: map[string]CacheEntry{
		"9.9.9.9": {Score: 5, Source: "abuseipdb", Timestamp: time.Now()},
	}}
	data, _ := json.Marshal(&primed)
	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := AbuseIPDBConfig{APIKey: "test-key"}

	if err := checkAbuseIPDB("9.9.9.9", cfg, cacheFile); err != nil {
		t.Fatal(err)
	}

	if apiCalled {
		t.Error("API was called despite IP being in the loaded cache file")
	}
}

func TestCheckIPCache_BlocksSpamRecordedIP(t *testing.T) {
	resetIPCache()

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	recordIP("5.5.5.5", "honeypot", 100, cacheFile)

	if err := checkIPCache("5.5.5.5", cacheFile); err == nil {
		t.Error("checkIPCache() expected error for spam-recorded IP, got nil")
	}
}

func TestCheckIPCache_AllowsCleanIP(t *testing.T) {
	resetIPCache()

	cacheFile := filepath.Join(t.TempDir(), "cache.json")

	if err := checkIPCache("6.6.6.6", cacheFile); err != nil {
		t.Errorf("checkIPCache() unexpected error for unknown IP: %v", err)
	}
}

func TestRecordIP_WritesSource(t *testing.T) {
	resetIPCache()

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	recordIP("7.7.7.7", "setvalue", 100, cacheFile)

	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatal(err)
	}

	var loaded IPCache
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}

	entry, ok := loaded.Entries["7.7.7.7"]
	if !ok {
		t.Fatal("entry not found in cache file")
	}

	if entry.Source != "setvalue" {
		t.Errorf("source = %q, want %q", entry.Source, "setvalue")
	}
}
