package web

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/gotd/td/tgerr"
)

func parsePhone(phone string) string {
	var sb strings.Builder
	for _, r := range phone {
		if (r >= '0' && r <= '9') || r == '+' {
			sb.WriteRune(r)
		}
	}
	res := sb.String()
	if len(res) == 0 {
		return ""
	}
	return res
}
func randomToken(size int) (string, error) {
	return randomTokenFrom(rand.Reader, size)
}

func randomTokenFrom(reader io.Reader, size int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if size <= 0 {
		return "", fmt.Errorf("token size must be positive")
	}
	b := make([]byte, size)
	for i := range b {
		n, err := rand.Int(reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func clientIP(r *http.Request) string {
	raw := r.RemoteAddr
	if !trustProxyHeaders() {
		return normalizeClientIP(raw)
	}
	// Cloudflare-provided client IP is the most trustworthy when present.
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		raw = cf
		return normalizeClientIP(raw)
	}
	// Otherwise use the left-most entry of X-Forwarded-For. This assumes
	// the server is behind a trusted reverse proxy; without such a proxy
	// the header is trivially spoofable.
	if xfwd := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xfwd != "" {
		parts := strings.Split(xfwd, ",")
		for _, p := range parts {
			ip := strings.TrimSpace(p)
			if ip != "" {
				return normalizeClientIP(ip)
			}
		}
	}
	return normalizeClientIP(raw)
}

func normalizeClientIP(ip string) string {
	ip = strings.TrimSpace(ip)
	host, _, err := net.SplitHostPort(ip)
	if err == nil {
		ip = host
	}
	ip = strings.Trim(ip, "[]")
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	return addr.Unmap().String()
}
func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(phone)-4) + phone[len(phone)-4:]
}

func hasAPIToken(token string) bool {
	return strings.TrimSpace(token) != ""
}

func writeTelegramAuthError(wr http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	text := err.Error()
	if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		status = http.StatusUnauthorized
		text = "2FA Password required"
	} else if tgerr.Is(err, "PHONE_CODE_INVALID") || tgerr.Is(err, "PASSWORD_HASH_INVALID") {
		status = http.StatusForbidden
	} else if tgerr.Is(err, "PHONE_CODE_EXPIRED") {
		status = http.StatusNotFound
		text = "Code expired"
	} else if d, ok := tgerr.AsFloodWait(err); ok {
		status = 421
		text = renderFloodWait(d)
	}
	http.Error(wr, text, status)
}

func renderFloodWait(d time.Duration) string {
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	parts := ""
	if hours > 0 {
		parts += fmt.Sprintf("%d hour(-s) ", hours)
	}
	if minutes > 0 {
		parts += fmt.Sprintf("%d minute(-s) ", minutes)
	}
	parts += fmt.Sprintf("%d second(-s)", seconds)
	return "You got FloodWait for " + parts + ". Wait the specified amount of time and try again."
}
