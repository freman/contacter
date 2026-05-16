package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
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

func dialSMTP(config SMTPConfig) (*smtp.Client, error) {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	if config.TLS {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: config.Host})
		if err != nil {
			return nil, fmt.Errorf("TLS dial failed: %w", err)
		}

		client, err := smtp.NewClient(conn, config.Host)
		if err != nil {
			conn.Close()

			return nil, fmt.Errorf("SMTP handshake failed: %w", err)
		}

		return client, nil
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	if config.StartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: config.Host}); err != nil {
			client.Close()

			return nil, fmt.Errorf("STARTTLS failed: %w", err)
		}
	}

	return client, nil
}

func sendEmail(details EmailDetails, config SMTPConfig) error {
	senderAddr, err := mail.ParseAddress(details.Contact.Email)
	if err != nil {
		return fmt.Errorf("invalid sender address: %w", err)
	}

	details.Contact.Name = sanitizeHeader(details.Contact.Name)
	details.Contact.Email = sanitizeHeader(details.Contact.Email)
	details.Contact.Subject = sanitizeHeader(details.Contact.Subject)

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

	client, err := dialSMTP(config)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer client.Close()

	if config.Username != "" {
		auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth failed: %w", err)
		}
	}

	if err := client.Mail(senderAddr.Address); err != nil {
		return fmt.Errorf("MAIL FROM failed: %v", err)
	}

	if err := client.Rcpt(config.Recipient); err != nil {
		return fmt.Errorf("RCPT TO failed: %v", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %v", err)
	}

	if _, err = wc.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("failed to write email body: %v", err)
	}

	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close email data: %v", err)
	}

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

func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}

		return r
	}, s)
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
