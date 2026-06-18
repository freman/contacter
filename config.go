package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Network struct {
	*net.IPNet
}

type UpstreamConfig struct {
	Whitelist []Network `json:"whitelist"`
	Method    string    `json:"method"`
}

type SMTPConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Recipient string `json:"recipient"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	TLS       bool   `json:"tls"`      // implicit TLS (typically port 465)
	StartTLS  bool   `json:"starttls"` // upgrade via STARTTLS (typically port 587)
}

type SMTPTestConfig struct {
	Enabled bool   `json:"enabled"`
	Helo    string `json:"helo"`
	From    string `json:"from"`
}

type AbuseIPDBConfig struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key"`
}

type CacheConfig struct {
	File string `json:"file"`
}

type CORSSettings struct {
	Origins []string `json:"origins"`
}

type GlobalConfig struct {
	RateLimit RateLimitConfig `json:"rate_limit"`
}

type RateLimitConfig struct {
	Rate      float64 `json:"rate"`       // requests per second; 0 = default (1/300, i.e. one per 5 min)
	Burst     int     `json:"burst"`      // max burst; 0 = default (1)
	ExpiresIn int     `json:"expires_in"` // entry lifetime in seconds; 0 = default (1800)
}

type SpamFilterConfig struct {
	Field string `json:"field"` // form field name
	Type  string `json:"type"`  // "honeypot", "setvalue", or "token"
	Value string `json:"value"` // expected value (setvalue), HMAC secret (token), or ignored (honeypot)
}

type Config struct {
	Upstream       UpstreamConfig     `json:"upstream"`
	SMTP           SMTPConfig         `json:"smtp"`
	SMTPTest       SMTPTestConfig     `json:"smtptest"`
	AbuseIPDB      AbuseIPDBConfig    `json:"abuseipdb"`
	Cache          CacheConfig        `json:"cache"`
	CORS           CORSSettings       `json:"cors"`
	RateLimit      RateLimitConfig    `json:"rate_limit"`
	SpamProtection []SpamFilterConfig `json:"spam_protection"`
}

type ConfigCache struct {
	configs      map[string]*Config
	mu           sync.RWMutex
	dir          string
	globalConfig *GlobalConfig
	globalLoaded bool
	globalMu     sync.RWMutex
}

func NewConfigCache(configDir string) *ConfigCache {
	cc := &ConfigCache{
		configs: make(map[string]*Config),
		dir:     configDir,
	}

	go cc.watchConfigDir()

	return cc
}

func (cc *ConfigCache) Get(domain string) (*Config, error) {
	domain = strings.ToLower(domain)

	if strings.ContainsAny(domain, "/\\") || strings.Contains(domain, "..") || domain == "." {
		return nil, fmt.Errorf("invalid domain: %q", domain)
	}

	cc.mu.RLock()
	cachedConfig, exists := cc.configs[domain]
	cc.mu.RUnlock()

	if exists {
		return cachedConfig, nil
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	if config, exists := cc.configs[domain]; exists {
		return config, nil
	}

	domainConfigPath := filepath.Join(cc.dir, domain+".conf")

	var config Config

	if _, err := os.Stat(domainConfigPath); err == nil {
		data, err := os.ReadFile(domainConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read domain config %s: %v", domainConfigPath, err)
		}

		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal domain config %s: %v", domainConfigPath, err)
		}

		cc.configs[domain] = &config

		return &config, nil
	}

	defaultConfigPath := filepath.Join(cc.dir, "default.conf")

	if _, err := os.Stat(defaultConfigPath); err == nil {
		data, err := os.ReadFile(defaultConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read default config %s: %v", defaultConfigPath, err)
		}

		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal default config %s: %v", defaultConfigPath, err)
		}

		cc.configs[domain] = &config

		return &config, nil
	}

	return nil, fmt.Errorf("no config found in %s for domain %s (tried %s and default.conf)", cc.dir, domain, domainConfigPath)
}

func (cc *ConfigCache) watchConfigDir() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("failed to create config watcher", "err", err)

		return
	}
	defer watcher.Close()

	if err = watcher.Add(cc.dir); err != nil {
		slog.Error("failed to watch config directory", "dir", cc.dir, "err", err)

		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 && strings.HasSuffix(event.Name, ".conf") {
				slog.Info("config changed, reloading", "file", event.Name)

				filename := filepath.Base(event.Name)
				domain := strings.TrimSuffix(filename, ".conf")

				cc.mu.Lock()

				if domain == "default" {
					for dom := range cc.configs {
						delete(cc.configs, dom)
					}
				} else {
					delete(cc.configs, domain)
				}

				cc.mu.Unlock()

				if domain == "global" {
					cc.globalMu.Lock()
					cc.globalConfig = nil
					cc.globalLoaded = false
					cc.globalMu.Unlock()
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}

			slog.Error("config watcher error", "err", err)
		}
	}
}

func (cc *ConfigCache) GetGlobal() *GlobalConfig {
	cc.globalMu.RLock()
	loaded, cfg := cc.globalLoaded, cc.globalConfig
	cc.globalMu.RUnlock()

	if loaded {
		return cfg
	}

	cc.globalMu.Lock()
	defer cc.globalMu.Unlock()

	if cc.globalLoaded {
		return cc.globalConfig
	}

	cc.globalLoaded = true

	data, err := os.ReadFile(filepath.Join(cc.dir, "global.conf"))
	if err != nil {
		return nil
	}

	var gcfg GlobalConfig
	if err := json.Unmarshal(data, &gcfg); err != nil {
		slog.Warn("failed to parse global.conf", "err", err)

		return nil
	}

	cc.globalConfig = &gcfg

	return cc.globalConfig
}

func (n *Network) UnmarshalJSON(data []byte) error {
	var s string

	err := json.Unmarshal(data, &s)
	if err != nil {
		return err
	}

	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return fmt.Errorf("invalid IP address: %s", s)
		}

		if ip.To4() != nil {
			s += "/32"
		} else {
			s += "/128"
		}
	}

	_, n.IPNet, err = net.ParseCIDR(s)

	return err
}
