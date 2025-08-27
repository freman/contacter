package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

type ContactDetails struct {
	Name    string `json:"name" form:"name"`
	Email   string `json:"email" form:"email"`
	Subject string `json:"subject" form:"subject"`
	Message string `json:"message" form:"message"`
}

type TemplateData struct {
	Success bool
	Error   string
}

func (c Contacter) handleContact(ec echo.Context) error {
	var domain = strings.ToLower(ec.Request().Host)
	config, err := c.configCache.Get(domain)
	if err != nil {
		ec.Logger().Errorf("Failed to load config for %s: %v", domain, err)
		return ec.Render(http.StatusInternalServerError, domain, TemplateData{Error: "Failed to load config"})
	}

	clientIP := ec.RealIP()

	// Check AbuseIPDB
	if config.AbuseIPDB.Enabled {
		if err := checkAbuseIPDB(clientIP); err != nil {
			return ec.Render(http.StatusOK, domain, TemplateData{Error: fmt.Sprintf("Access denied: %v", err)})
		}
	}

	if ec.Request().Method != http.MethodPost {
		// Show form
		return ec.Render(http.StatusOK, domain, TemplateData{})
	}

	// Parse form data
	var details ContactDetails
	if err := ec.Bind(&details); err != nil {
		return ec.Render(http.StatusOK, domain, TemplateData{Error: "Invalid form data"})
	}

	// Test SMTP server for recipient acceptance
	if config.SMTPTest.Enabled {
		if err := testRecipientSMTPServer(details.Email, config.SMTPTest); err != nil {
			return ec.Render(http.StatusOK, domain, TemplateData{Error: fmt.Sprintf("SMTP test failed: %v", err)})
		}
	}

	// Send email
	if err := sendEmail(EmailDetails{details, ec.RealIP(), ec.Request().UserAgent()}, config.SMTP); err != nil {
		return ec.Render(http.StatusOK, domain, TemplateData{Error: fmt.Sprintf("Failed to send email: %v", err)})
	}

	// Success (no email sent, only tested)
	return ec.Render(http.StatusOK, domain, TemplateData{Success: true})
}

func (c Contacter) handleStatic(ec echo.Context) error {
	// Get requested file path, stripping "/contact/" prefix
	filePath := strings.TrimPrefix(ec.Request().URL.Path, "/contact/")

	// Get domain
	domain := strings.ToLower(ec.Request().Host)

	// Try domain-specific file
	domainFile := filepath.Join("static", domain, filePath)
	if _, err := os.Stat(domainFile); err == nil {
		ec.Response().Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
		return ec.File(domainFile)
	}

	// Try default file
	defaultFile := filepath.Join("static", "default", filePath)
	if _, err := os.Stat(defaultFile); err == nil {
		ec.Response().Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
		return ec.File(defaultFile)
	}

	// File not found
	return ec.NoContent(http.StatusNotFound)
}
