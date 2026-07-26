package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// cdnHosts are external origins that must not appear anywhere in the served
// onboarding HTML once R4.1 is complete.
var cdnHosts = []string{
	"cdnjs.cloudflare.com",
	"cdn.jsdelivr.net",
	"unpkg.com",
	"lottiefiles.com",
	"fonts.googleapis.com",
	"fonts.gstatic.com",
	"css.gg",
	"raw.githubusercontent.com",
	"static.dan.tatar",
}

// renderOnboarding serves the onboarding root page from the embedded assets
// (DataRoot points at an empty temp dir so no disk web-resources override is
// present) and returns the raw HTML.
func renderOnboarding(t *testing.T) string {
	t.Helper()
	web := NewWeb(WebConfig{DataRoot: t.TempDir()})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	web.RootHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("onboarding status=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html, got %q", ct)
	}
	return rec.Body.String()
}

func TestOnboardingHasNoCDNURLs(t *testing.T) {
	body := renderOnboarding(t)
	for _, host := range cdnHosts {
		if strings.Contains(body, host) {
			t.Fatalf("onboarding HTML still references external CDN %q:\n%s", host, body)
		}
	}
}

func TestOnboardingCSPDisallowsExternalOrigins(t *testing.T) {
	web := NewWeb(WebConfig{DataRoot: t.TempDir()})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	setSecurityHeaders(rec)
	web.RootHandler(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header is missing")
	}
	for _, host := range cdnHosts {
		if strings.Contains(csp, host) {
			t.Fatalf("CSP still allows external origin %q: %q", host, csp)
		}
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("CSP must start from default-src 'self': %q", csp)
	}
}

func TestOnboardingStaticAssetsAreLocal(t *testing.T) {
	body := renderOnboarding(t)
	// No attribute may point at an absolute or protocol-relative external URL.
	for _, needle := range []string{`src="http`, `src="//`, `href="http`, `href="//`, `src='http`, `src='//`, `href='http`, `href='//`} {
		if strings.Contains(body, needle) {
			t.Fatalf("onboarding references a non-local asset (%q):\n%s", needle, body)
		}
	}
	// Sanity: the vendored local scripts must be referenced.
	for _, local := range []string{`src="static/jquery.min.js"`, `src="static/sweetalert2.min.js"`, `src="static/qr-code-styling.min.js"`, `src="static/lottie-shim.js"`} {
		if !strings.Contains(body, local) {
			t.Fatalf("onboarding missing local asset reference %q", local)
		}
	}
}

func TestOnboardingAccessibility(t *testing.T) {
	body := renderOnboarding(t)
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse onboarding HTML: %v", err)
	}

	var (
		buttons       int
		inputs        []string
		labelFor      = map[string]bool{}
		h1Count       int
		hasLiveRegion bool
	)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "button":
				buttons++
			case "h1":
				h1Count++
			case "input":
				id := ""
				for _, a := range n.Attr {
					if a.Key == "id" {
						id = a.Val
					}
				}
				inputs = append(inputs, id)
			case "label":
				for _, a := range n.Attr {
					if a.Key == "for" {
						labelFor[a.Val] = true
					}
				}
			}
			for _, a := range n.Attr {
				if a.Key == "role" && (a.Val == "status" || a.Val == "alert") {
					hasLiveRegion = true
				}
				if a.Key == "aria-live" {
					hasLiveRegion = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if buttons == 0 {
		t.Fatal("onboarding must use semantic <button> elements, found none")
	}
	if h1Count == 0 {
		t.Error("onboarding must have a top-level <h1> heading")
	}
	if !hasLiveRegion {
		t.Error("onboarding must expose status messages via role=status/aria-live")
	}
	for _, id := range inputs {
		if id == "" {
			t.Error("an <input> has no id and therefore cannot be labelled")
			continue
		}
		if !labelFor[id] {
			t.Errorf("input %q has no associated <label for=%q>", id, id)
		}
	}
}

func TestOnboardingDocumentLang(t *testing.T) {
	body := renderOnboarding(t)
	if !strings.Contains(body, `<html lang="en">`) {
		t.Fatalf("onboarding <html> must declare lang, got:\n%s", body)
	}
}

// TestOnboardingTemplateTokensResolved guards the Go-side jinja emulation in
// static_ui.go: after RootHandler runs, no template tokens ({% %} / {{ }}) may
// remain and the platform_emoji placeholder must resolve to a local static path.
func TestOnboardingTemplateTokensResolved(t *testing.T) {
	body := renderOnboarding(t)
	for _, token := range []string{"{%", "%}", "{{", "}}"} {
		if strings.Contains(body, token) {
			t.Fatalf("unresolved template token %q in served HTML:\n%s", token, body)
		}
	}
	if strings.Contains(body, "platform_emoji") {
		t.Fatalf("platform_emoji placeholder was not substituted:\n%s", body)
	}
	if !strings.Contains(body, "static/platform-") {
		t.Fatalf("platform emoji must resolve to a local static path, got:\n%s", body)
	}
}

// TestOnboardingStaticServedFromEmbed exercises the /static/ handler against the
// embedded asset tree (no disk override) and asserts the vendored libraries and
// the localized root.js are reachable offline and CDN-free.
func TestOnboardingStaticServedFromEmbed(t *testing.T) {
	web := NewWeb(WebConfig{DataRoot: t.TempDir()})
	handler := web.staticHandler()

	for _, path := range []string{
		"/static/root.js",
		"/static/jquery.min.js",
		"/static/sweetalert2.min.js",
		"/static/qr-code-styling.min.js",
		"/static/lottie-shim.js",
		"/static/base.css",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s served empty body", path)
		}
		for _, host := range cdnHosts {
			if strings.Contains(rec.Body.String(), host) {
				t.Fatalf("%s still references external CDN %q", path, host)
			}
		}
	}

	// root.js must carry the RU/EN string map for R4.2 localization.
	req := httptest.NewRequest(http.MethodGet, "/static/root.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	js := rec.Body.String()
	for _, marker := range []string{"var STRINGS", `"en"`, `"ru"`, "applyI18n", "goroku_lang"} {
		if !strings.Contains(js, marker) {
			t.Fatalf("root.js missing localization marker %q", marker)
		}
	}
}
