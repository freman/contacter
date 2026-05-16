package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func newRateLimitTestServer(t testing.TB, configDir, templatesDir, staticDir string) *httptest.Server {
	t.Helper()

	e := echo.New()
	cc := &ConfigCache{configs: make(map[string]*Config), dir: configDir}
	app := Contacter{echo: e, configCache: cc, staticDir: staticDir}

	var err error

	e.Renderer, err = NewTemplateRegistry(templatesDir)
	if err != nil {
		t.Fatal(err)
	}

	e.IPExtractor = app.IPExtractor

	domainRL := NewDomainRateLimiter(cc)
	e.POST("/contact", app.handleContact, domainRL.Middleware())

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	return srv
}

func postContact(t testing.TB, srv *httptest.Server) int {
	t.Helper()

	form := url.Values{
		"name":    {"Test User"},
		"email":   {"test@example.com"},
		"subject": {"Hello"},
		"message": {"Test message"},
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/contact", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	resp.Body.Close()

	return resp.StatusCode
}

// TestRateLimit_BackwardsCompatible verifies that a config with no rate_limit block
// and no global.conf gets no rate limiting - matching pre-rate-limit behaviour.
func TestRateLimit_BackwardsCompatible(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newRateLimitTestServer(t, configDir, templatesDir, t.TempDir())

	for i := range 5 {
		if status := postContact(t, srv); status == http.StatusTooManyRequests {
			t.Fatalf("request %d got 429 but no rate_limit was configured", i+1)
		}
	}
}

// TestRateLimit_DomainLimitApplies verifies rate limiting kicks in when a
// rate_limit block is present in the domain config.
func TestRateLimit_DomainLimitApplies(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	cfg := defaultTestConfig(smtpHost, smtpPort)
	cfg.RateLimit = RateLimitConfig{Rate: 0.0001, Burst: 1, ExpiresIn: 60}
	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), cfg)
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newRateLimitTestServer(t, configDir, templatesDir, t.TempDir())

	postContact(t, srv) // consume the burst

	if status := postContact(t, srv); status != http.StatusTooManyRequests {
		t.Errorf("second rapid request got %d, want 429", status)
	}
}

// TestRateLimit_GlobalLimitApplies verifies the global limiter activates when
// global.conf contains a rate_limit block, even when the domain config has none.
func TestRateLimit_GlobalLimitApplies(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	gcfg := GlobalConfig{RateLimit: RateLimitConfig{Rate: 0.0001, Burst: 1, ExpiresIn: 60}}
	data, err := json.Marshal(gcfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(configDir, "global.conf"), data, 0644); err != nil {
		t.Fatal(err)
	}

	srv := newRateLimitTestServer(t, configDir, templatesDir, t.TempDir())

	postContact(t, srv) // consume the burst

	if status := postContact(t, srv); status != http.StatusTooManyRequests {
		t.Errorf("second rapid request got %d, want 429", status)
	}
}

// TestRateLimit_EmptyGlobalConfNoLimiting verifies that a global.conf with an
// all-zero rate_limit block does not activate the global limiter.
func TestRateLimit_EmptyGlobalConfNoLimiting(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	// global.conf with an empty rate_limit block - should be a no-op.
	gcfg := GlobalConfig{RateLimit: RateLimitConfig{}}
	data, err := json.Marshal(gcfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(configDir, "global.conf"), data, 0644); err != nil {
		t.Fatal(err)
	}

	srv := newRateLimitTestServer(t, configDir, templatesDir, t.TempDir())

	for i := range 5 {
		if status := postContact(t, srv); status == http.StatusTooManyRequests {
			t.Fatalf("request %d got 429 but rate_limit block was all zeros", i+1)
		}
	}
}
