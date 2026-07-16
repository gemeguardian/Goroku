package web

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (trustProxyHeaders() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

func trustProxyHeaders() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GOROKU_TRUST_PROXY_HEADERS")))
	return value == "1" || value == "true" || value == "yes"
}

func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func constantTimeEqualString(a, b string) bool {
	ab := []byte(a)
	bb := []byte(b)
	if len(ab) != len(bb) {
		_ = subtle.ConstantTimeCompare(ab, ab)
		return false
	}
	return subtle.ConstantTimeCompare(ab, bb) == 1
}

// sameOrigin reports whether Origin/Referer match the request host.
// Missing both headers is not accepted for browser cookie session mutating requests.
func sameOrigin(r *http.Request) bool {
	matched := false
	present := false
	for _, header := range []string{"Origin", "Referer"} {
		raw := strings.TrimSpace(r.Header.Get(header))
		if raw == "" {
			continue
		}
		present = true
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return false
		}
		if !strings.EqualFold(u.Host, r.Host) {
			return false
		}
		matched = true
	}
	return present && matched
}
