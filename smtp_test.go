package main

import (
	"strings"
	"testing"
)

func TestSanitizeHeader(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain text", "John Doe", "John Doe"},
		{"strips CR", "John\rDoe", "JohnDoe"},
		{"strips LF", "John\nDoe", "JohnDoe"},
		{"strips CRLF", "John\r\nDoe", "JohnDoe"},
		{"header injection via name", "foo\r\nBcc: attacker@evil.com", "fooBcc: attacker@evil.com"},
		{"header injection via email", "user@example.com\r\nBcc: attacker@evil.com", "user@example.comBcc: attacker@evil.com"},
		{"unicode preserved", "Ján Novák", "Ján Novák"},
		{"only newlines", "\r\n\r\n", ""},
		{"mixed whitespace", "foo \t bar", "foo \t bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeHeader(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeHeader(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func FuzzSanitizeHeader(f *testing.F) {
	f.Add("John Doe")
	f.Add("user@example.com\r\nBcc: attacker@evil.com")
	f.Add("Ján Novák")
	f.Add("\r\n\r\n")
	f.Add("Subject: real\r\nBcc: evil@example.com\r\nX-Injected: yes")

	f.Fuzz(func(t *testing.T, input string) {
		output := sanitizeHeader(input)

		if strings.ContainsAny(output, "\r\n") {
			t.Errorf("sanitizeHeader(%q) = %q: output contains CR or LF", input, output)
		}
	})
}
