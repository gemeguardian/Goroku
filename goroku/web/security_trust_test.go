package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPSpoofedXFFNoTrustedProxies(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.7:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.1")

	if got := clientIP(req); got != "198.51.100.7" {
		t.Fatalf("spoofed XFF without trusted CIDRs must use RemoteAddr, got %q", got)
	}
}

func TestClientIPTrustedProxyRightmostUntrusted(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.2")

	if got := clientIP(req); got != "203.0.113.99" {
		t.Fatalf("expected right-most untrusted hop, got %q", got)
	}
}

func TestClientIPPeerOutsideCIDRIgnoresXFF(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.7:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := clientIP(req); got != "198.51.100.7" {
		t.Fatalf("peer outside CIDR must ignore XFF, got %q", got)
	}
}

func TestClientIPNormalizesIPv4WithPort(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:54321"

	if got := clientIP(req); got != "192.0.2.1" {
		t.Fatalf("expected normalized IPv4 without port, got %q", got)
	}
}

func TestClientIPNormalizesIPv6WithPort(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[fd00::1]:54321"

	if got := clientIP(req); got != "fd00::1" {
		t.Fatalf("expected normalized IPv6 without port/brackets, got %q", got)
	}
}

func TestClientIPAllTrustedXFFReturnsLeftmost(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "10.0.0.0/8,10.0.0.0/16")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "10.0.0.5, 10.0.0.6")

	if got := clientIP(req); got != "10.0.0.5" {
		t.Fatalf("all-trusted XFF must return left-most, got %q", got)
	}
}

func TestClientIPDeprecatedTrustProxyHeadersWithoutCIDRs(t *testing.T) {
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "true")
	t.Setenv("GOROKU_TRUSTED_PROXIES", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.7:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := clientIP(req); got != "198.51.100.7" {
		t.Fatalf("deprecated GOROKU_TRUST_PROXY_HEADERS without CIDRs must not trust headers, got %q", got)
	}
}

func TestTrustedProxyPeerIPv6CIDR(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "fd00::/8")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[fd00::1]:1234"
	req.Header.Set("X-Forwarded-For", "2001:db8::1")

	if got := clientIP(req); got != "2001:db8::1" {
		t.Fatalf("IPv6 trusted proxy should extract right-most untrusted hop, got %q", got)
	}
}

func TestTrustedProxyPeerInvalidCIDRIsFailClosed(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "not-a-cidr,10.0.0.0/8")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := clientIP(req); got != "203.0.113.99" {
		t.Fatalf("valid CIDR in mixed list should still work, got %q", got)
	}
}

func TestClampIPForDiagnosticShortIPUnchanged(t *testing.T) {
	ip := "192.0.2.1"
	if got := clampIPForDiagnostic(ip); got != ip {
		t.Fatalf("short IP must be unchanged, got %q", got)
	}
}

func TestClampIPForDiagnosticLongValueClamped(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := clampIPForDiagnostic(long)
	runes := []rune(got)
	if len(runes) != 65 {
		t.Fatalf("expected 64 runes + ellipsis = 65, got %d", len(runes))
	}
	if string(runes[64:]) != "…" {
		t.Fatalf("expected trailing ellipsis, got %q", string(runes[64:]))
	}
}

func TestClampIPForDiagnosticBoundaryExactly64(t *testing.T) {
	ip := strings.Repeat("a", 64)
	if got := clampIPForDiagnostic(ip); got != ip {
		t.Fatalf("IP of exactly 64 runes must be unchanged, got len=%d", len([]rune(got)))
	}
}

func TestClampIPForDiagnosticDoesNotPanicOnEmpty(t *testing.T) {
	if got := clampIPForDiagnostic(""); got != "" {
		t.Fatalf("empty IP must return empty, got %q", got)
	}
}

func TestRightmostUntrustedHopEmptyXFF(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "10.0.0.0/8")
	if got := rightmostUntrustedHop(""); got != "" {
		t.Fatalf("empty XFF must return empty, got %q", got)
	}
}

func TestRightmostUntrustedHopSingleUntrusted(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "10.0.0.0/8")
	if got := rightmostUntrustedHop("203.0.113.99"); got != "203.0.113.99" {
		t.Fatalf("single untrusted IP must be returned, got %q", got)
	}
}

func TestIsHTTPSRejectsForwardedProtoFromUntrustedPeer(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.RemoteAddr = "198.51.100.7:5000"
	req.Header.Set("X-Forwarded-Proto", "https")

	if isHTTPS(req) {
		t.Fatal("X-Forwarded-Proto from untrusted peer must be ignored")
	}
}

func TestTrustedProxyPeerNoCIDRFailClosed(t *testing.T) {
	t.Setenv("GOROKU_TRUSTED_PROXIES", "")
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	if trustedProxyPeer(req) {
		t.Fatal("trustedProxyPeer must return false when no CIDRs are configured")
	}
}
