package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

const tokenMaxAge = 10 * time.Minute

var (
	errTokenExpired  = errors.New("token expired")
	errTokenReplayed = errors.New("token already used")
)

// SpamField carries the data a template needs to render one hidden form field.
// Type is passed through so templates can render honeypot fields differently
// (e.g. wrapped in a CSS-hidden div) from plain hidden inputs.
type SpamField struct {
	Name  string
	Value string
	Type  string
}

// SpamFilter is the common interface for all spam protection strategies.
// Field is called on GET to produce the hidden field value for the form.
// Validate is called on POST to check the submitted value.
type SpamFilter interface {
	Field(c *echo.Context) (SpamField, error)
	Validate(c *echo.Context) error
}

// NewSpamFilter constructs a SpamFilter from config.
// type "honeypot"  - field must be empty on submit; value is ignored
// type "setvalue"  - field must equal value on submit; JS sets it on the client
// type "token"     - field carries an HMAC-signed timestamp; value is the secret key
func NewSpamFilter(cfg SpamFilterConfig) (SpamFilter, error) {
	switch cfg.Type {
	case "honeypot":
		return &honeypotFilter{field: cfg.Field}, nil
	case "setvalue":
		if cfg.Value == "" {
			return nil, fmt.Errorf("setvalue filter %q requires a non-empty value", cfg.Field)
		}

		return &setValueFilter{field: cfg.Field, value: cfg.Value}, nil
	case "token":
		if cfg.Value == "" {
			return nil, fmt.Errorf("token filter %q requires a secret in the value field", cfg.Field)
		}

		return &tokenFilter{field: cfg.Field, secret: cfg.Value}, nil
	default:
		return nil, fmt.Errorf("unknown spam filter type: %q", cfg.Type)
	}
}

// --- honeypot ---

type honeypotFilter struct {
	field string
}

func (h *honeypotFilter) Field(_ *echo.Context) (SpamField, error) {
	return SpamField{Name: h.field, Value: "", Type: "honeypot"}, nil
}

func (h *honeypotFilter) Validate(c *echo.Context) error {
	if c.FormValue(h.field) != "" {
		return fmt.Errorf("honeypot field %q was filled", h.field)
	}

	return nil
}

// --- setvalue ---

type setValueFilter struct {
	field string
	value string
}

func (s *setValueFilter) Field(_ *echo.Context) (SpamField, error) {
	return SpamField{Name: s.field, Value: s.value, Type: "setvalue"}, nil
}

func (s *setValueFilter) Validate(c *echo.Context) error {
	if c.FormValue(s.field) != s.value {
		return fmt.Errorf("setvalue field %q has wrong value", s.field)
	}

	return nil
}

// --- token ---

// tokenStore tracks used tokens for their remaining lifetime to prevent replay.
type tokenStore struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func (ts *tokenStore) markUsed(token string, expiry time.Time) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	now := time.Now()
	for k, exp := range ts.entries {
		if now.After(exp) {
			delete(ts.entries, k)
		}
	}

	if _, seen := ts.entries[token]; seen {
		return false
	}

	ts.entries[token] = expiry

	return true
}

var usedTokens = &tokenStore{entries: make(map[string]time.Time)}

type tokenFilter struct {
	field  string
	secret string
}

func (t *tokenFilter) Field(_ *echo.Context) (SpamField, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(t.secret))
	mac.Write([]byte(ts))
	token := ts + "." + hex.EncodeToString(mac.Sum(nil))

	return SpamField{Name: t.field, Value: token, Type: "token"}, nil
}

func (t *tokenFilter) Validate(c *echo.Context) error {
	token := c.FormValue(t.field)
	parts := strings.SplitN(token, ".", 2)

	if len(parts) != 2 {
		return fmt.Errorf("token field %q missing or malformed", t.field)
	}

	ts, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, []byte(t.secret))
	mac.Write([]byte(ts))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return fmt.Errorf("token field %q has invalid signature", t.field)
	}

	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("token field %q has unparseable timestamp", t.field)
	}

	issued := time.Unix(tsInt, 0)
	expiry := issued.Add(tokenMaxAge)

	if time.Now().After(expiry) {
		return errTokenExpired
	}

	if !usedTokens.markUsed(token, expiry) {
		return errTokenReplayed
	}

	return nil
}

