package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"goroku/goroku/webiface"
)

type testInlineProvider struct{}

func (testInlineProvider) GetBotAPI() *tgbotapi.BotAPI       { return nil }
func (testInlineProvider) PopWebAuthToken(token string) bool { return token == "ok" }

type testWebClient struct {
	tgid     int64
	provider webiface.InlineProvider
}

func (c testWebClient) TGIDValue() int64                          { return c.tgid }
func (c testWebClient) InlineProvider() webiface.InlineProvider   { return c.provider }
func (c testWebClient) Connect() error                            { return nil }
func (c testWebClient) Disconnect() error                         { return nil }
func (c testWebClient) SendCodeRequest(phone string) error        { return nil }
func (c testWebClient) SignIn(phone, code, password string) error { return nil }
func (c testWebClient) QRLogin() (string, error)                  { return "", nil }
func (c testWebClient) QRLoginStatus() (string, error)            { return "", nil }
func (c testWebClient) SendMessage(chat webiface.ChatRef, message string) (any, error) {
	return nil, nil
}
func (c testWebClient) ResolveUsername(username string) (bool, error) { return false, nil }
func (c testWebClient) CheckBot(username string) (bool, error)        { return false, nil }

func TestWebRootHandlerAndSetupToken(t *testing.T) {
	tempDir := t.TempDir()
	resourcesDir := filepath.Join(tempDir, "web-resources")
	err := os.Mkdir(resourcesDir, 0750)
	if err != nil {
		t.Fatalf("failed to create web-resources: %v", err)
	}

	baseTemplate := `<html><head>{% block head %}{% endblock %}</head><body>{% block content %}{% endblock %}{% block after %}{% endblock %}</body></html>`
	rootTemplate := `
	{% block head %}<title>Test Title</title>{% endblock %}
	{% block content %}<h1>Hello World</h1>{% endblock %}
	{% block after %}<script>console.log("after");</script>{% endblock %}
	`

	err = os.WriteFile(filepath.Join(resourcesDir, "base.jinja2"), []byte(baseTemplate), 0600)
	if err != nil {
		t.Fatalf("failed to write base.jinja2: %v", err)
	}
	err = os.WriteFile(filepath.Join(resourcesDir, "root.jinja2"), []byte(rootTemplate), 0600)
	if err != nil {
		t.Fatalf("failed to write root.jinja2: %v", err)
	}

	cfg := WebConfig{
		SetupToken: "my-secret-token",
		DataRoot:   tempDir,
	}

	web := NewWeb(cfg)

	// Test 1: RootHandler renders template correctly
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	web.RootHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %q", contentType)
	}

	// Test 2: WebAuthHandler requires setup token when no clients are registered
	reqAuthNoToken := httptest.NewRequest("POST", "/web-auth", nil)
	wAuthNoToken := httptest.NewRecorder()

	web.WebAuthHandler(wAuthNoToken, reqAuthNoToken)

	respAuthNoToken := wAuthNoToken.Result()
	if respAuthNoToken.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", respAuthNoToken.StatusCode)
	}

	// Test 3: WebAuthHandler succeeds when correct setup token is provided via header
	reqAuthWithToken := httptest.NewRequest("POST", "/web-auth", nil)
	reqAuthWithToken.Header.Set("X-Goroku-Setup-Token", "my-secret-token")
	wAuthWithToken := httptest.NewRecorder()

	web.WebAuthHandler(wAuthWithToken, reqAuthWithToken)

	respAuthWithToken := wAuthWithToken.Result()
	if respAuthWithToken.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respAuthWithToken.StatusCode)
	}
}

func TestWebResourceDirUsesDataRoot(t *testing.T) {
	t.Setenv("GOROKU_WEB_RESOURCES", "")
	tempDir := t.TempDir()
	resourcesDir := filepath.Join(tempDir, "web-resources")
	if err := os.Mkdir(resourcesDir, 0750); err != nil {
		t.Fatalf("failed to create resources dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resourcesDir, "base.jinja2"), []byte("base"), 0600); err != nil {
		t.Fatalf("failed to write base template: %v", err)
	}

	if got := webResourceDir(tempDir); got != resourcesDir {
		t.Fatalf("expected %q, got %q", resourcesDir, got)
	}
}

func TestWebResourceDirPrefersEnv(t *testing.T) {
	tempDir := t.TempDir()
	resourcesDir := filepath.Join(tempDir, "custom-resources")
	if err := os.Mkdir(resourcesDir, 0750); err != nil {
		t.Fatalf("failed to create resources dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resourcesDir, "base.jinja2"), []byte("base"), 0600); err != nil {
		t.Fatalf("failed to write base template: %v", err)
	}
	t.Setenv("GOROKU_WEB_RESOURCES", resourcesDir)

	if got := webResourceDir("/does/not/exist"); got != resourcesDir {
		t.Fatalf("expected %q, got %q", resourcesDir, got)
	}
}

func TestCheckSetupTokenSources(t *testing.T) {
	web := NewWeb(WebConfig{SetupToken: "secret"})

	// No token
	req := httptest.NewRequest("GET", "/", nil)
	if web.checkSetupToken(req) {
		t.Error("Expected false for no token")
	}

	// Header
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Goroku-Setup-Token", "secret")
	if !web.checkSetupToken(req) {
		t.Error("Expected true for header token")
	}

	// Cookie
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "setup_token", Value: "secret"})
	if !web.checkSetupToken(req) {
		t.Error("Expected true for cookie token")
	}

	// Query parameter used by the first setup URL.
	req = httptest.NewRequest("GET", "/?setup_token=secret", nil)
	if !web.checkSetupToken(req) {
		t.Error("Expected true for query setup token")
	}

	// Wrong header token
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Goroku-Setup-Token", "wrong")
	if web.checkSetupToken(req) {
		t.Error("Expected false for wrong token")
	}

	// Empty setup token in config
	webEmpty := NewWeb(WebConfig{SetupToken: ""})
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Goroku-Setup-Token", "secret")
	if webEmpty.checkSetupToken(req) {
		t.Error("Expected false when setup token is empty")
	}
}

func TestReadLimitedBodyRejectsOversizedPayload(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 9)))
	w := httptest.NewRecorder()

	if _, ok := readLimitedBody(w, req, 8); ok {
		t.Fatal("oversized body should be rejected")
	}
	if w.Result().StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Result().StatusCode)
	}
}

func TestClientIPIgnoresProxyHeadersByDefault(t *testing.T) {
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := clientIP(req); got != "10.0.0.1:1234" {
		t.Fatalf("expected RemoteAddr, got %q", got)
	}
}

func TestClientIPUsesProxyHeadersWhenTrusted(t *testing.T) {
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "true")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.1")

	if got := clientIP(req); got != "203.0.113.99" {
		t.Fatalf("expected forwarded IP, got %q", got)
	}
}

func TestIsHTTPSIgnoresForwardedProtoByDefault(t *testing.T) {
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "")
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	if isHTTPS(req) {
		t.Fatal("X-Forwarded-Proto should be ignored unless proxy headers are trusted")
	}
}

func TestIsHTTPSUsesForwardedProtoWhenTrusted(t *testing.T) {
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "true")
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	if !isHTTPS(req) {
		t.Fatal("X-Forwarded-Proto should be used when proxy headers are trusted")
	}
}

func TestSameOriginRejectsCrossOrigin(t *testing.T) {
	req := httptest.NewRequest("POST", "https://example.com/set_api", nil)
	req.Header.Set("Origin", "https://evil.example")

	if sameOrigin(req) {
		t.Fatal("cross-origin request should be rejected")
	}
}

func TestSetSecurityHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	setSecurityHeaders(w)

	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected X-Frame-Options DENY, got %q", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("CSP should contain frame-ancestors 'none', got %q", got)
	}
}

func TestGetClientTGIDUsesInterface(t *testing.T) {
	if got := getClientTGID(testWebClient{tgid: 123}); got != 123 {
		t.Fatalf("expected TGID 123, got %d", got)
	}
	if got := getClientTGID(struct{ TGID int64 }{TGID: 456}); got != 0 {
		t.Fatalf("plain structs should not be inspected with reflection, got %d", got)
	}
}

func TestGetInlineProviderUsesInterface(t *testing.T) {
	provider := testInlineProvider{}
	got := getInlineProvider(testWebClient{provider: provider})
	if got == nil {
		t.Fatal("expected inline provider")
	}
	if !got.PopWebAuthToken("ok") {
		t.Fatal("expected provider method to be available")
	}
	if got := getInlineProvider(struct{ GorokuInline testInlineProvider }{GorokuInline: provider}); got != nil {
		t.Fatal("plain structs should not be inspected with reflection")
	}
}
