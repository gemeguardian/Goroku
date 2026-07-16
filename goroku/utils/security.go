package utils

import (
	"os"
	"regexp"
	"strings"
)

var sensitivePatterns = []*regexp.Regexp{
	// Telegram bot tokens: 123456789:AA...
	regexp.MustCompile(`\b\d{5,}:[A-Za-z0-9_-]{30,}\b`),
	// Common key=value secret forms.
	regexp.MustCompile(`(?i)\b(api[_-]?hash|api[_-]?key|secret|token|password|passwd|setup[_-]?token)\s*[:=]\s*['"]?[^\s'"&<>]{6,}`),
	// Redis/Postgres URLs often include credentials.
	regexp.MustCompile(`(?i)\b(redis|postgres|postgresql|mysql|mongodb)://[^\s<>"]+`),
	// Long hex/base64-ish credentials.
	regexp.MustCompile(`\b[a-fA-F0-9]{32,}\b`),
}

func CensorSensitive(text string, extras ...string) string {
	for _, secret := range extras {
		secret = strings.TrimSpace(secret)
		if len(secret) >= 6 {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}

	for _, envKey := range []string{
		"REDIS_URL", "DATABASE_URL", "GOROKU_SETUP_TOKEN",
		"api_hash", "API_HASH", "BOT_TOKEN", "TOKEN",
	} {
		if val := strings.TrimSpace(os.Getenv(envKey)); len(val) >= 6 {
			text = strings.ReplaceAll(text, val, "[REDACTED]")
		}
	}

	for _, re := range sensitivePatterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

func CensorSensitiveWithPhones(text string, phones []string, extras ...string) string {
	text = CensorSensitive(text, extras...)
	for _, phone := range phones {
		var digits strings.Builder
		for _, r := range phone {
			if r >= '0' && r <= '9' {
				digits.WriteRune(r)
			}
		}
		if digits.Len() < 7 {
			continue
		}

		parts := strings.Split(digits.String(), "")
		matches := regexp.MustCompile(`\+?`+strings.Join(parts, `[\t ().-]*`)).FindAllStringIndex(text, -1)
		if len(matches) == 0 {
			continue
		}

		var censored strings.Builder
		last := 0
		for _, match := range matches {
			start, end := match[0], match[1]
			if (start > 0 && text[start-1] >= '0' && text[start-1] <= '9') ||
				(end < len(text) && text[end] >= '0' && text[end] <= '9') {
				continue
			}
			censored.WriteString(text[last:start])
			censored.WriteString("[REDACTED]")
			last = end
		}
		if last > 0 {
			censored.WriteString(text[last:])
			text = censored.String()
		}
	}
	return text
}

func SecureFile(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		_ = os.Chmod(path, 0600)
	}
}
