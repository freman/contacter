package main

import (
	"bytes"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"text/template"
	"time"
)

type EmailDetails struct {
	Contact   ContactDetails
	RemoteIP  string
	UserAgent string
}

func sendEmail(details EmailDetails, config SMTPConfig) error {
	var buf bytes.Buffer
	if err := emailTemplate.Execute(&buf, struct {
		Contact   ContactDetails
		RemoteIP  string
		UserAgent string
		To        string
		Date      string
	}{
		details.Contact,
		details.RemoteIP,
		details.UserAgent,
		config.Recipient,
		time.Now().Format(time.RFC3339),
	}); err != nil {
		return fmt.Errorf("failed to generate email from template: %w", err)
	}

	// Connect to SMTP server (no auth, no TLS)
	client, err := smtp.Dial(fmt.Sprintf("%s:%d", config.Host, config.Port))
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %v", err)
	}
	defer client.Close()

	// Set sender and recipient
	if err := client.Mail(details.Contact.Email); err != nil {
		return fmt.Errorf("MAIL FROM failed: %v", err)
	}
	if err := client.Rcpt(config.Recipient); err != nil {
		return fmt.Errorf("RCPT TO failed: %v", err)
	}

	// Send email data
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %v", err)
	}
	_, err = wc.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write email body: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close email data: %v", err)
	}

	// Quit SMTP session
	if err := client.Quit(); err != nil {
		return fmt.Errorf("QUIT failed: %v", err)
	}

	return nil
}

func testRecipientSMTPServer(email string, config SMTPTestConfig) error {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid email format")
	}
	domain := parts[1]

	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return fmt.Errorf("failed to resolve MX records for %s: %v", domain, err)
	}

	mxHost := strings.TrimSuffix(mxRecords[0].Host, ".")
	addr := mxHost + ":25"

	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %v", addr, err)
	}
	defer client.Close()

	if err := client.Hello(config.Helo); err != nil {
		return fmt.Errorf("HELO failed: %v", err)
	}

	if err := client.Mail(config.From); err != nil {
		return fmt.Errorf("MAIL FROM failed: %v", err)
	}

	if err := client.Rcpt(email); err != nil {
		return fmt.Errorf("RCPT TO failed: %v", err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("QUIT failed: %v", err)
	}

	return nil
}

const emailTemplateString = `From: {{.Contact.Name}} <{{ .Contact.Email }}>` + "\r" + `
To: {{ .To }}` + "\r" + `
Subject: {{ .Contact.Subject }}` + "\r" + `
Date: {{ .Date }}` + "\r" + `
` + "\r" + `
{{ .Contact.Message }}


---
{{ .RemoteIP }}
{{ .UserAgent }}
`

var emailTemplate = template.Must(template.New("email").Parse(emailTemplateString))
