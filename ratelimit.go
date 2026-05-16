package main

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const (
	defaultRate      = 1.0 / 300 // 1 request per 5 minutes
	defaultBurst     = 1
	defaultExpiresIn = 1800 // 30 minutes in seconds
)

type cachedLimiter struct {
	middleware echo.MiddlewareFunc
	rate       float64
	burst      int
	expiresIn  int
}

type DomainRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]cachedLimiter
	cache    *ConfigCache
}

func NewDomainRateLimiter(cc *ConfigCache) *DomainRateLimiter {
	return &DomainRateLimiter{
		limiters: make(map[string]cachedLimiter),
		cache:    cc,
	}
}

// Middleware returns a middleware that applies rate limiting in two optional
// layers: a global limit (from global.conf) checked first, then a per-domain
// limit (from the domain's rate_limit config). Either or both may be absent.
func (d *DomainRateLimiter) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			domain := strings.ToLower(c.Request().Host)

			handler := next

			if cfg, err := d.cache.Get(domain); err == nil && isNonZeroRateLimit(cfg.RateLimit) {
				handler = d.getOrCreate(domain, cfg.RateLimit)(handler)
			}

			if gcfg := d.cache.GetGlobal(); gcfg != nil && isNonZeroRateLimit(gcfg.RateLimit) {
				handler = d.getOrCreate("", gcfg.RateLimit)(handler)
			}

			return handler(c)
		}
	}
}

// isNonZeroRateLimit returns true if any field is set, so an empty rate_limit
// block in global.conf doesn't activate the global limiter.
func isNonZeroRateLimit(cfg RateLimitConfig) bool {
	return cfg.Rate != 0 || cfg.Burst != 0 || cfg.ExpiresIn != 0
}

// getOrCreate returns a cached rate limiter middleware for the given key.
// The empty string key "" is reserved for the global limiter.
func (d *DomainRateLimiter) getOrCreate(key string, cfg RateLimitConfig) echo.MiddlewareFunc {
	rate := cfg.Rate
	if rate == 0 {
		rate = defaultRate
	}

	burst := cfg.Burst
	if burst == 0 {
		burst = defaultBurst
	}

	expiresIn := cfg.ExpiresIn
	if expiresIn == 0 {
		expiresIn = defaultExpiresIn
	}

	d.mu.RLock()
	cl, ok := d.limiters[key]
	d.mu.RUnlock()

	if ok && cl.rate == rate && cl.burst == burst && cl.expiresIn == expiresIn {
		return cl.middleware
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if cl, ok = d.limiters[key]; ok && cl.rate == rate && cl.burst == burst && cl.expiresIn == expiresIn {
		return cl.middleware
	}

	mw := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:      rate,
			Burst:     burst,
			ExpiresIn: time.Duration(expiresIn) * time.Second,
		}),
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return c.RealIP(), nil
		},
		ErrorHandler: func(c *echo.Context, err error) error {
			return respond(c, strings.ToLower(c.Request().Host), http.StatusTooManyRequests, TemplateData{Error: "Too many requests, please try again later"})
		},
		DenyHandler: func(c *echo.Context, identifier string, err error) error {
			return respond(c, strings.ToLower(c.Request().Host), http.StatusTooManyRequests, TemplateData{Error: "Too many requests, please try again later"})
		},
	})

	d.limiters[key] = cachedLimiter{middleware: mw, rate: rate, burst: burst, expiresIn: expiresIn}

	return mw
}
