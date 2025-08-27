package main

import (
	"encoding/json"
	"fmt"
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
}

type SMTPTestConfig struct {
	Enabled bool   `json:"enabled"`
	Helo    string `json:"helo"`
	From    string `json:"from"`
}

type AbuseIPDBConfig struct {
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"api_key"`
	CacheFile string `json:"cache_file"`
}

type Config struct {
	Upstream  UpstreamConfig  `json:"upstream"`
	SMTP      SMTPConfig      `json:"smtp"`
	SMTPTest  SMTPTestConfig  `json:"smtptest"`
	AbuseIPDB AbuseIPDBConfig `json:"abuseipdb"`
}

type ConfigCache struct {
	configs map[string]*Config
	mu      sync.RWMutex
	dir     string
}

func NewConfigCache(configDir string) *ConfigCache {
	cc := &ConfigCache{
		configs: make(map[string]*Config),
		dir:     configDir,
	}

	// Start watching config directory
	go cc.watchConfigDir()

	return cc
}

func (cc *ConfigCache) Get(domain string) (*Config, error) {
	domain = strings.ToLower(domain)

	cc.mu.RLock()
	cachedConfig, exists := cc.configs[domain]
	cc.mu.RUnlock()
	if exists {
		return cachedConfig, nil
	}

	// Load config
	cc.mu.Lock()
	defer cc.mu.Unlock()
	// Recheck to avoid race
	if config, exists := cc.configs[domain]; exists {
		return config, nil
	}

	// Try domain-specific config
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

	// Try default config
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
		fmt.Printf("Failed to create config watcher: %v\n", err)
		return
	}
	defer watcher.Close()

	// Watch the config directory
	err = watcher.Add(cc.dir)
	if err != nil {
		fmt.Printf("Failed to watch config directory %s: %v\n", cc.dir, err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 && strings.HasSuffix(event.Name, ".conf") {
				fmt.Printf("Config file changed: %s, reloading config\n", event.Name)

				filename := filepath.Base(event.Name)
				domain := strings.TrimSuffix(filename, ".conf")

				// Clear cache for affected domain (or all domains if default.conf)
				cc.mu.Lock()
				if domain == "default" {
					// Clear all configs to force reload with default
					for dom := range cc.configs {
						delete(cc.configs, dom)
					}
				} else {
					delete(cc.configs, domain)
				}
				cc.mu.Unlock()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("Config watcher error: %v\n", err)
		}
	}
}

func (n *Network) UnmarshalJSON(data []byte) error {
	var s string
	err := json.Unmarshal(data, &s)
	if err != nil {
		return err
	}

	if !strings.Contains(s, "/") {
		s = s + "/32"
	}

	_, n.IPNet, err = net.ParseCIDR(s)
	return err
}
