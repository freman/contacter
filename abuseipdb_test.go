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

// resetAbuseIPCache clears the package-level cache and Once between tests.
func resetAbuseIPCache() {
	abuseIPCache = Cache{Entries: make(map[string]CacheEntry)}
	abuseLoadOnce = sync.Once{}
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
	resetAbuseIPCache()
	withMockAbuseIPDB(t, abuseIPDBServer(t, 75))

	cfg := AbuseIPDBConfig{
		APIKey:    "test-key",
		CacheFile: filepath.Join(t.TempDir(), "cache.json"),
	}

	if err := checkAbuseIPDB("1.2.3.4", cfg); err == nil {
		t.Error("checkAbuseIPDB() expected error for score 75, got nil")
	}
}

func TestCheckAbuseIPDB_Allowed(t *testing.T) {
	resetAbuseIPCache()
	withMockAbuseIPDB(t, abuseIPDBServer(t, 10))

	cfg := AbuseIPDBConfig{
		APIKey:    "test-key",
		CacheFile: filepath.Join(t.TempDir(), "cache.json"),
	}

	if err := checkAbuseIPDB("1.2.3.5", cfg); err != nil {
		t.Errorf("checkAbuseIPDB() unexpected error for score 10: %v", err)
	}
}

func TestCheckAbuseIPDB_Cached(t *testing.T) {
	resetAbuseIPCache()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": 10},
		})
	}))
	t.Cleanup(srv.Close)
	withMockAbuseIPDB(t, srv)

	cfg := AbuseIPDBConfig{
		APIKey:    "test-key",
		CacheFile: filepath.Join(t.TempDir(), "cache.json"),
	}

	if err := checkAbuseIPDB("1.2.3.6", cfg); err != nil {
		t.Fatal(err)
	}

	if err := checkAbuseIPDB("1.2.3.6", cfg); err != nil {
		t.Fatal(err)
	}

	if callCount != 1 {
		t.Errorf("API called %d times, want 1 (second call should use in-memory cache)", callCount)
	}
}

func TestCheckAbuseIPDB_WritesCache(t *testing.T) {
	resetAbuseIPCache()
	withMockAbuseIPDB(t, abuseIPDBServer(t, 20))

	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	cfg := AbuseIPDBConfig{
		APIKey:    "test-key",
		CacheFile: cacheFile,
	}

	if err := checkAbuseIPDB("1.2.3.7", cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Error("cache file not created after API call")
	}
}

func TestCheckAbuseIPDB_SendsAPIKey(t *testing.T) {
	resetAbuseIPCache()

	var receivedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("Key")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": 0},
		})
	}))
	t.Cleanup(srv.Close)
	withMockAbuseIPDB(t, srv)

	cfg := AbuseIPDBConfig{
		APIKey:    "my-secret-key",
		CacheFile: filepath.Join(t.TempDir(), "cache.json"),
	}

	_ = checkAbuseIPDB("1.2.3.8", cfg)

	if receivedKey != "my-secret-key" {
		t.Errorf("API key sent = %q, want %q", receivedKey, "my-secret-key")
	}
}

func TestCheckAbuseIPDB_LoadsCache(t *testing.T) {
	resetAbuseIPCache()

	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"abuseConfidenceScore": 0},
		})
	}))
	t.Cleanup(srv.Close)
	withMockAbuseIPDB(t, srv)

	// Write a pre-populated cache file with a recent entry.
	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	primed := Cache{Entries: map[string]CacheEntry{
		"9.9.9.9": {Score: 5, Timestamp: time.Now()},
	}}
	data, _ := json.Marshal(&primed)
	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := AbuseIPDBConfig{
		APIKey:    "test-key",
		CacheFile: cacheFile,
	}

	if err := checkAbuseIPDB("9.9.9.9", cfg); err != nil {
		t.Fatal(err)
	}

	if apiCalled {
		t.Error("API was called despite IP being in the loaded cache file")
	}
}
