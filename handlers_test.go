package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

// --- helpers ---

const minimalTemplate = `{{if .Success}}success{{else if .Error}}error:{{.Error}}{{else}}form{{end}}`

func writeTemplate(t testing.TB, templatesDir, subdir, content string) {
	t.Helper()

	d := filepath.Join(templatesDir, subdir)
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, "contact.html"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// startMockSMTP starts a local TCP server that speaks just enough SMTP to
// accept messages without sending anything. Returns host and port.
func startMockSMTP(t testing.TB) (host string, port int) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { l.Close() })

	addr := l.Addr().(*net.TCPAddr)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go serveSMTP(conn)
		}
	}()

	return "127.0.0.1", addr.Port
}

func serveSMTP(conn net.Conn) {
	defer conn.Close()

	fmt.Fprintf(conn, "220 mock SMTP ready\r\n")

	r := bufio.NewReader(conn)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		upper := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(upper, "DATA"):
			fmt.Fprintf(conn, "354 Go ahead\r\n")

			for {
				body, err := r.ReadString('\n')
				if err != nil {
					return
				}

				if strings.TrimSpace(body) == "." {
					break
				}
			}

			fmt.Fprintf(conn, "250 OK\r\n")

		case strings.HasPrefix(upper, "QUIT"):
			fmt.Fprintf(conn, "221 Bye\r\n")
			return

		default:
			fmt.Fprintf(conn, "250 OK\r\n")
		}
	}
}

func newTestServer(t testing.TB, configDir, templatesDir, staticDir string) *httptest.Server {
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
	e.GET("/contact", app.handleContact)
	e.POST("/contact", app.handleContact)
	e.GET("/contact/*", app.handleStatic)

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	return srv
}

func defaultTestConfig(smtpHost string, smtpPort int) Config {
	return Config{
		SMTP: SMTPConfig{Host: smtpHost, Port: smtpPort, Recipient: "test@example.com"},
	}
}

// --- isXHR tests ---

func TestIsXHR(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"no headers", nil, false},
		{"accept json", map[string]string{"Accept": "application/json"}, true},
		{"accept json with others", map[string]string{"Accept": "text/html, application/json"}, true},
		{"accept html only", map[string]string{"Accept": "text/html"}, false},
		{"x-requested-with xhr", map[string]string{"X-Requested-With": "XMLHttpRequest"}, true},
		{"x-requested-with other value", map[string]string{"X-Requested-With": "fetch"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/contact", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			c := echo.NewContext(req, httptest.NewRecorder())

			if got := isXHR(c); got != tt.want {
				t.Errorf("isXHR() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- handleContact integration tests ---

func TestHandleContact_GET_HTML(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newTestServer(t, configDir, templatesDir, t.TempDir())

	resp, err := http.Get(srv.URL + "/contact")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandleContact_GET_JSON(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newTestServer(t, configDir, templatesDir, t.TempDir())

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/contact", nil)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestHandleContact_POST_HTML_Success(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newTestServer(t, configDir, templatesDir, t.TempDir())

	form := url.Values{
		"name":    {"Test User"},
		"email":   {"test@example.com"},
		"subject": {"Hello"},
		"message": {"Test message"},
	}

	resp, err := http.Post(srv.URL+"/contact", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHandleContact_POST_JSON_Success(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newTestServer(t, configDir, templatesDir, t.TempDir())

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if success, _ := body["success"].(bool); !success {
		t.Errorf("response = %v, want {\"success\": true}", body)
	}
}

func TestHandleContact_POST_JSON_HeaderInjection(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()
	smtpHost, smtpPort := startMockSMTP(t)

	mustWriteConfig(t, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newTestServer(t, configDir, templatesDir, t.TempDir())

	// Attempt header injection via name and email fields.
	form := url.Values{
		"name":    {"Evil\r\nBcc: attacker@evil.com"},
		"email":   {"user@example.com\r\nBcc: attacker@evil.com"},
		"subject": {"Hi\r\nX-Injected: yes"},
		"message": {"Test"},
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/contact", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should succeed (sanitization happens transparently) or fail gracefully - never 5xx.
	if resp.StatusCode >= 500 {
		t.Errorf("header injection attempt caused server error: status %d", resp.StatusCode)
	}
}

func TestHandleContact_NoConfig(t *testing.T) {
	configDir, templatesDir := t.TempDir(), t.TempDir()

	writeTemplate(t, templatesDir, "default", minimalTemplate)

	srv := newTestServer(t, configDir, templatesDir, t.TempDir())

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/contact", nil)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if _, hasError := body["error"]; !hasError {
		t.Errorf("response = %v, want an error field", body)
	}
}

// --- fuzz ---

func FuzzHandleContact(f *testing.F) {
	configDir := f.TempDir()
	templatesDir := f.TempDir()
	staticDir := f.TempDir()

	smtpHost, smtpPort := startMockSMTP(f)

	mustWriteConfig(f, filepath.Join(configDir, "default.conf"), defaultTestConfig(smtpHost, smtpPort))
	writeTemplate(f, templatesDir, "default", minimalTemplate)

	srv := newTestServer(f, configDir, templatesDir, staticDir)

	f.Add("John Doe", "john@example.com", "Hello", "Test message")
	f.Add("", "", "", "")
	f.Add("Evil\r\nBcc: x@x.com", "x@x.com\r\nBcc: y@y.com", "subj\r\nX: y", "msg")
	f.Add(strings.Repeat("a", 10000), strings.Repeat("b", 10000), strings.Repeat("c", 10000), strings.Repeat("d", 10000))

	f.Fuzz(func(t *testing.T, name, email, subject, message string) {
		form := url.Values{
			"name":    {name},
			"email":   {email},
			"subject": {subject},
			"message": {message},
		}

		req, err := http.NewRequest(http.MethodPost, srv.URL+"/contact", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			t.Errorf("server error for input {%q, %q, %q, %q}: status %d",
				name, email, subject, message, resp.StatusCode)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Errorf("response is not valid JSON for input {%q, %q, %q, %q}: %v",
				name, email, subject, message, err)
		}
	})
}
