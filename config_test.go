package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNetworkUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantStr string
	}{
		{"valid CIDR", `"10.0.0.0/8"`, false, "10.0.0.0/8"},
		{"bare IP gets /32", `"192.168.1.1"`, false, "192.168.1.1/32"},
		{"valid IPv6 CIDR", `"::1/128"`, false, "::1/128"},
		{"bare IPv6 gets /128", `"::1"`, false, "::1/128"},
		{"invalid IP", `"not-an-ip"`, true, ""},
		{"empty string", `""`, true, ""},
		{"not a string", `42`, true, ""},
		{"out of range octet", `"256.0.0.1"`, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n Network
			err := json.Unmarshal([]byte(tt.input), &n)

			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalJSON(%s) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}

			if !tt.wantErr && n.String() != tt.wantStr {
				t.Errorf("UnmarshalJSON(%s) = %q, want %q", tt.input, n.String(), tt.wantStr)
			}
		})
	}
}

func FuzzNetworkUnmarshalJSON(f *testing.F) {
	f.Add(`"10.0.0.0/8"`)
	f.Add(`"192.168.1.1"`)
	f.Add(`""`)
	f.Add(`"not-an-ip"`)
	f.Add(`"::1/128"`)
	f.Add(`"256.0.0.1"`)
	f.Add(`"0.0.0.0/0"`)

	f.Fuzz(func(t *testing.T, input string) {
		var n Network
		_ = json.Unmarshal([]byte(input), &n)
		// must not panic
	})
}

func mustWriteConfig(t testing.TB, path string, cfg Config) {
	t.Helper()

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigCacheGet_DomainSpecific(t *testing.T) {
	dir := t.TempDir()
	mustWriteConfig(t, filepath.Join(dir, "example.com.conf"), Config{SMTP: SMTPConfig{Host: "domain-smtp"}})
	mustWriteConfig(t, filepath.Join(dir, "default.conf"), Config{SMTP: SMTPConfig{Host: "default-smtp"}})

	cc := &ConfigCache{configs: make(map[string]*Config), dir: dir}

	got, err := cc.Get("example.com")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.SMTP.Host != "domain-smtp" {
		t.Errorf("Get() SMTP.Host = %q, want %q", got.SMTP.Host, "domain-smtp")
	}
}

func TestConfigCacheGet_DefaultFallback(t *testing.T) {
	dir := t.TempDir()
	mustWriteConfig(t, filepath.Join(dir, "default.conf"), Config{SMTP: SMTPConfig{Host: "default-smtp"}})

	cc := &ConfigCache{configs: make(map[string]*Config), dir: dir}

	got, err := cc.Get("unknown.example.com")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.SMTP.Host != "default-smtp" {
		t.Errorf("Get() SMTP.Host = %q, want %q", got.SMTP.Host, "default-smtp")
	}
}

func TestConfigCacheGet_NotFound(t *testing.T) {
	dir := t.TempDir()
	cc := &ConfigCache{configs: make(map[string]*Config), dir: dir}

	_, err := cc.Get("missing.example.com")
	if err == nil {
		t.Error("Get() expected error for missing config, got nil")
	}
}

func TestConfigCacheGet_Cached(t *testing.T) {
	dir := t.TempDir()
	mustWriteConfig(t, filepath.Join(dir, "default.conf"), Config{SMTP: SMTPConfig{Host: "smtp"}})

	cc := &ConfigCache{configs: make(map[string]*Config), dir: dir}

	first, err := cc.Get("example.com")
	if err != nil {
		t.Fatal(err)
	}

	second, err := cc.Get("example.com")
	if err != nil {
		t.Fatal(err)
	}

	if first != second {
		t.Error("Get() returned different pointers on second call - cache not working")
	}
}

func TestConfigCacheGet_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	mustWriteConfig(t, filepath.Join(dir, "example.com.conf"), Config{SMTP: SMTPConfig{Host: "smtp"}})

	cc := &ConfigCache{configs: make(map[string]*Config), dir: dir}

	_, err := cc.Get("EXAMPLE.COM")
	if err != nil {
		t.Fatalf("Get() with uppercase domain failed: %v", err)
	}
}

func TestConfigCacheGet_PathTraversal(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "configs")

	if err := os.Mkdir(inner, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a sentinel config outside the config dir that traversal would reach.
	mustWriteConfig(t, filepath.Join(outer, "escaped.conf"), Config{SMTP: SMTPConfig{Host: "escaped-host"}})

	cc := &ConfigCache{configs: make(map[string]*Config), dir: inner}

	// "../escaped" would construct inner/../escaped.conf = outer/escaped.conf without protection.
	got, err := cc.Get("../escaped")
	if err == nil && got.SMTP.Host == "escaped-host" {
		t.Error("path traversal succeeded - config loaded from outside configured dir")
	}
}

func FuzzConfigCacheGet(f *testing.F) {
	f.Add("example.com")
	f.Add("../escaped")
	f.Add("../../etc/passwd")
	f.Add("")
	f.Add("EXAMPLE.COM")
	f.Add(".")
	f.Add("..")

	f.Fuzz(func(t *testing.T, domain string) {
		dir := t.TempDir()
		cc := &ConfigCache{configs: make(map[string]*Config), dir: dir}
		_, _ = cc.Get(domain)
		// must not panic
	})
}

func FuzzConfigJSON(f *testing.F) {
	f.Add(`{"smtp":{"host":"localhost","port":25,"recipient":"a@b.com"}}`)
	f.Add(`{}`)
	f.Add(`null`)
	f.Add(`{"upstream":{"whitelist":["10.0.0.0/8"],"method":"direct"}}`)
	f.Add(`{"cors":{"origins":["https://example.com","*"]}}`)

	f.Fuzz(func(t *testing.T, input string) {
		var cfg Config
		_ = json.Unmarshal([]byte(input), &cfg)
		// must not panic
	})
}
