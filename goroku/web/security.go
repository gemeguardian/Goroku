package web

import (
	"crypto/subtle"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
)

var (
	trustedProxiesMu        sync.Mutex
	trustedProxiesRaw       string
	trustedProxiesPrefixes  []netip.Prefix
	trustProxyHeadersWarned sync.Once
)

func trustedProxies() []netip.Prefix {
	raw := strings.TrimSpace(os.Getenv("GOROKU_TRUSTED_PROXIES"))
	trustedProxiesMu.Lock()
	defer trustedProxiesMu.Unlock()
	if raw == trustedProxiesRaw {
		return trustedProxiesPrefixes
	}
	trustedProxiesRaw = raw
	trustedProxiesPrefixes = nil
	if raw == "" {
		return nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			L().Warn("invalid CIDR in GOROKU_TRUSTED_PROXIES, skipping", zap.String("cidr", part), zap.Error(err))
			continue
		}
		trustedProxiesPrefixes = append(trustedProxiesPrefixes, prefix)
	}
	return trustedProxiesPrefixes
}

func ipInTrustedProxies(ip string, prefixes []netip.Prefix) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func trustedProxyPeer(r *http.Request) bool {
	prefixes := trustedProxies()
	if len(prefixes) == 0 {
		warnDeprecatedTrustProxyHeadersOnce()
		return false
	}
	host := normalizeClientIP(r.RemoteAddr)
	if host == "" {
		return false
	}
	return ipInTrustedProxies(host, prefixes)
}

func warnDeprecatedTrustProxyHeadersOnce() {
	if !trustProxyHeadersEnv() {
		return
	}
	if strings.TrimSpace(os.Getenv("GOROKU_TRUSTED_PROXIES")) != "" {
		return
	}
	trustProxyHeadersWarned.Do(func() {
		L().Warn("GOROKU_TRUST_PROXY_HEADERS is deprecated; forwarding headers will NOT be trusted without explicit GOROKU_TRUSTED_PROXIES CIDRs")
	})
}

func trustProxyHeadersEnv() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GOROKU_TRUST_PROXY_HEADERS")))
	return value == "1" || value == "true" || value == "yes"
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (trustedProxyPeer(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

func rightmostUntrustedHop(xff string) string {
	prefixes := trustedProxies()
	parts := strings.Split(xff, ",")
	var ips []string
	for _, p := range parts {
		ip := strings.TrimSpace(p)
		if ip != "" {
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return ""
	}
	for i := len(ips) - 1; i >= 0; i-- {
		normalized := normalizeClientIP(ips[i])
		if normalized == "" {
			continue
		}
		if !ipInTrustedProxies(normalized, prefixes) {
			return normalized
		}
	}
	for _, ip := range ips {
		if normalized := normalizeClientIP(ip); normalized != "" {
			return normalized
		}
	}
	return ""
}

func clampIPForDiagnostic(ip string) string {
	const maxRunes = 64
	runes := []rune(ip)
	if len(runes) <= maxRunes {
		return ip
	}
	return string(runes[:maxRunes]) + "…"
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
