package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v5"
)

func isXHR(ec *echo.Context) bool {
	return strings.Contains(ec.Request().Header.Get("Accept"), "application/json") ||
		ec.Request().Header.Get("X-Requested-With") == "XMLHttpRequest"
}

func respond(ec *echo.Context, domain string, status int, data TemplateData) error {
	if isXHR(ec) {
		if data.Error != "" {
			return ec.JSON(status, map[string]any{"error": data.Error})
		}

		for _, f := range data.SpamFields {
			if f.Type == "token" {
				return ec.JSON(status, map[string]any{"token": f.Value})
			}
		}

		return ec.JSON(status, map[string]any{"success": data.Success})
	}

	return ec.Render(status, domain, data)
}

// isUnder reports whether target is inside base (both should be cleaned paths).
func isUnder(base, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type ContactDetails struct {
	Name    string `json:"name" form:"name"`
	Email   string `json:"email" form:"email"`
	Subject string `json:"subject" form:"subject"`
	Message string `json:"message" form:"message"`
}

type TemplateData struct {
	Success    bool
	Error      string
	SpamFields []SpamField
	Prefill    *ContactDetails
}

func (c Contacter) handleContact(ec *echo.Context) error {
	domain := strings.ToLower(ec.Request().Host)

	config, err := c.configCache.Get(domain)
	if err != nil {
		slog.Error("failed to load config", "domain", domain, "err", err)

		return respond(ec, domain, http.StatusInternalServerError, TemplateData{Error: "Failed to load config"})
	}

	clientIP := ec.RealIP()

	if config.Cache.File != "" {
		if err := checkIPCache(clientIP, config.Cache.File); err != nil {
			return respond(ec, domain, http.StatusOK, TemplateData{Error: "Access denied"})
		}
	}

	if config.AbuseIPDB.Enabled {
		if err := checkAbuseIPDB(clientIP, config.AbuseIPDB, config.Cache.File); err != nil {
			msg := "Access denied"
			if errors.Is(err, errAbuseService) {
				msg = "Unable to verify request, please try again later"
			}

			return respond(ec, domain, http.StatusOK, TemplateData{Error: msg})
		}
	}

	if ec.Request().Method != http.MethodPost {
		var spamFields []SpamField

		for _, sc := range config.SpamProtection {
			filter, err := NewSpamFilter(sc)
			if err != nil {
				slog.Error("invalid spam filter config", "type", sc.Type, "field", sc.Field, "err", err)

				continue
			}

			field, err := filter.Field(ec)
			if err != nil {
				slog.Error("spam filter field generation failed", "type", sc.Type, "field", sc.Field, "err", err)

				continue
			}

			spamFields = append(spamFields, field)
		}

		return respond(ec, domain, http.StatusOK, TemplateData{SpamFields: spamFields})
	}

	var details ContactDetails
	if err := ec.Bind(&details); err != nil {
		return respond(ec, domain, http.StatusOK, TemplateData{Error: "Invalid form data"})
	}

	for _, sc := range config.SpamProtection {
		filter, err := NewSpamFilter(sc)
		if err != nil {
			slog.Error("invalid spam filter config", "type", sc.Type, "field", sc.Field, "err", err)

			continue
		}

		if err := filter.Validate(ec); err != nil {
			if errors.Is(err, errTokenExpired) {
				slog.Info("token expired", "ip", clientIP)

				return respond(ec, domain, http.StatusBadRequest, TemplateData{
					Error:   "Your session expired - please try again",
					Prefill: &details,
				})
			}

			slog.Info("spam filter rejected submission", "type", sc.Type, "field", sc.Field, "ip", clientIP, "err", err)

			if config.Cache.File != "" {
				recordIP(clientIP, sc.Type, 100, config.Cache.File)
			}

			return respond(ec, domain, http.StatusBadRequest, TemplateData{Error: "Submission rejected"})
		}
	}

	if config.SMTPTest.Enabled {
		if err := testRecipientSMTPServer(details.Email, config.SMTPTest); err != nil {
			return respond(ec, domain, http.StatusOK, TemplateData{Error: fmt.Sprintf("SMTP test failed: %v", err)})
		}
	}

	if err := sendEmail(EmailDetails{details, clientIP, ec.Request().UserAgent()}, config.SMTP); err != nil {
		return respond(ec, domain, http.StatusOK, TemplateData{Error: fmt.Sprintf("Failed to send email: %v", err)})
	}

	return respond(ec, domain, http.StatusOK, TemplateData{Success: true})
}

func (c Contacter) handleStatic(ec *echo.Context) error {
	filePath := strings.TrimPrefix(ec.Request().URL.Path, "/contact/")
	domain := strings.ToLower(ec.Request().Host)
	base := filepath.Clean(c.staticDir)

	domainFile := filepath.Join(base, domain, filePath)
	if isUnder(base, domainFile) {
		if _, err := os.Stat(domainFile); err == nil {
			ec.Response().Header().Set("Cache-Control", "public, max-age=3600")

			return ec.File(domainFile)
		}
	}

	defaultFile := filepath.Join(base, "default", filePath)
	if isUnder(base, defaultFile) {
		if _, err := os.Stat(defaultFile); err == nil {
			ec.Response().Header().Set("Cache-Control", "public, max-age=3600")

			return ec.File(defaultFile)
		}
	}

	return ec.NoContent(http.StatusNotFound)
}
