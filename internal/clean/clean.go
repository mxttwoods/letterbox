// Package clean prepares agent-written text for storage and display:
// it strips terminal escapes and invisible characters, and redacts
// strings that look like secrets.
package clean

import (
	"regexp"
	"strings"
	"unicode"
)

const redacted = "[redacted]"

var (
	// CSI and OSC terminal escape sequences.
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`)

	// Whole private-key blocks. An unterminated block is redacted to the end
	// of the text, since the key body is the secret, not the header.
	privateKeyRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)

	tokenRes = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),                                         // OpenAI, Anthropic
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`),                                    // GitHub
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`),                                  // GitHub fine-grained
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),                                   // AWS access key ID
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),                                  // Slack
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), // JWT
	}

	bearerRe = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/-]{20,}=*`)

	// key = value pairs where the key names a credential.
	assignRe = regexp.MustCompile(`(?i)\b((?:api[_-]?key|secret|token|password|passwd)["']?\s*[:=]\s*["']?)([^\s"']{12,})`)
)

// Text returns s with terminal escapes, control characters and invisible
// format characters removed. Newlines and tabs are kept.
func Text(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// Line is Text collapsed onto a single line.
func Line(s string) string {
	return strings.Join(strings.Fields(Text(s)), " ")
}

// Secrets redacts strings that look like credentials. It is best effort:
// it catches common token shapes, not every secret.
func Secrets(s string) string {
	s = privateKeyRe.ReplaceAllString(s, redacted+" private key")
	for _, re := range tokenRes {
		s = re.ReplaceAllString(s, redacted)
	}
	s = bearerRe.ReplaceAllString(s, "${1}"+redacted)
	s = assignRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := assignRe.FindStringSubmatch(m)
		if !looksRandom(parts[2]) {
			return m
		}
		return parts[1] + redacted
	})
	return s
}

// looksRandom reports whether v mixes letters and digits, which separates
// "password: hunter2hunter2" from prose like "token: unavailable-here".
func looksRandom(v string) bool {
	var letter, digit bool
	for _, r := range v {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return letter && digit
}
