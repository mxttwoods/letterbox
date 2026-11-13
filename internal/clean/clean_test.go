package clean

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "hello world", "hello world"},
		{"keeps newlines and tabs", "a\n\tb", "a\n\tb"},
		{"crlf", "a\r\nb", "a\nb"},
		{"ansi colour", "\x1b[31mred\x1b[0m text", "red text"},
		{"osc title", "\x1b]0;evil\x07ok", "ok"},
		{"bare escape and bell", "a\x1bb\x07c", "abc"},
		{"zero width and bidi", "a​b‮c", "abc"},
		{"trims", "  x \n", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.in); got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLine(t *testing.T) {
	if got := Line("a\n  b\tc "); got != "a b c" {
		t.Errorf("Line = %q", got)
	}
}

func TestSecretsRedacts(t *testing.T) {
	// Fake credentials, each split in two so that secret scanners reading
	// this file don't mistake them for real ones.
	secrets := []string{
		"sk-" + "ant-api03-abcdefghijklmnopqrstuvwxyz012345",
		"sk-" + "proj-abcdefghijklmnopqrstuvwx",
		"ghp" + "_abcdefghijklmnopqrstuvwxyz0123456789",
		"github" + "_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz",
		"AKIA" + "IOSFODNN7EXAMPLE",
		"xoxb" + "-123456789012-abcdefghijkl",
		"eyJ" + "hbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop",
	}
	for _, s := range secrets {
		got := Secrets("the key was " + s + " in the env file")
		if strings.Contains(got, s) {
			t.Errorf("secret %q survived: %q", s, got)
		}
		if !strings.Contains(got, "the key was") || !strings.Contains(got, "in the env file") {
			t.Errorf("surrounding text lost: %q", got)
		}
	}
}

func TestSecretsPrivateKeyBlock(t *testing.T) {
	body := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC"
	in := "before\n-----BEGIN RSA " + "PRIVATE KEY-----\n" + body + "\n-----END RSA " + "PRIVATE KEY-----\nafter"
	got := Secrets(in)
	if strings.Contains(got, body) {
		t.Errorf("key body survived: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("surrounding text lost: %q", got)
	}

	// A block with no END line still loses its body.
	got = Secrets("-----BEGIN OPENSSH " + "PRIVATE KEY-----\n" + body)
	if strings.Contains(got, body) {
		t.Errorf("unterminated key body survived: %q", got)
	}
}

func TestSecretsAssignmentsAndBearer(t *testing.T) {
	tests := []struct{ in, gone string }{
		{`API_KEY=abc123def456ghi789`, "abc123def456ghi789"},
		{`"password": "hunter2hunter2xx"`, "hunter2hunter2xx"},
		{`Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456`, "abcdefghijklmnopqrstuvwxyz123456"},
	}
	for _, tt := range tests {
		if got := Secrets(tt.in); strings.Contains(got, tt.gone) {
			t.Errorf("Secrets(%q) = %q, still contains the value", tt.in, got)
		}
	}
}

func TestSecretsLeavesProseAlone(t *testing.T) {
	for _, s := range []string{
		"The token: unavailable-in-this-session error came back twice.",
		"password = required-by-the-form",
		"I skipped the task because the instructions were unclear.",
		"max_tokens was set to 4096",
	} {
		if got := Secrets(s); got != s {
			t.Errorf("Secrets changed prose: %q -> %q", s, got)
		}
	}
}
