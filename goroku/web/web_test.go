package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goroku/goroku/webiface"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

type testInlineProvider struct{}

func (testInlineProvider) GetBotAPI() *tgbotapi.BotAPI       { return nil }
func (testInlineProvider) PopWebAuthToken(token string) bool { return token == "ok" }

type testWebClient struct {
	tgid     int64
	provider webiface.InlineProvider
}

type authTestClient struct {
	tgid     int64
	notified chan struct{}
	once     sync.Once
	sendErr  error
}

func (c *authTestClient) TGIDValue() int64                        { return c.tgid }
func (c *authTestClient) InlineProvider() webiface.InlineProvider { return nil }
func (c *authTestClient) Connect() error                          { return nil }
func (c *authTestClient) Disconnect() error                       { return nil }
func (c *authTestClient) SendCodeRequest(string) error            { return nil }
func (c *authTestClient) SignIn(string, string, string) error     { return nil }
func (c *authTestClient) QRLogin() (string, error)                { return "", nil }
func (c *authTestClient) QRLoginStatus() (string, error)          { return "", nil }
func (c *authTestClient) SendMessage(webiface.ChatRef, string) (any, error) {
	c.once.Do(func() { close(c.notified) })
	return nil, c.sendErr
}
func (c *authTestClient) ResolveUsername(string) (bool, error) { return false, nil }
func (c *authTestClient) CheckBot(string) (bool, error)        { return false, nil }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

type legacyWebDB struct{}

func (legacyWebDB) Get(string, string, any) (any, error) { return nil, nil }

type lifecycleListener struct {
	acceptCalled chan struct{}
	closed       chan struct{}
	acceptOnce   sync.Once
	closeOnce    sync.Once
}

func newLifecycleListener() *lifecycleListener {
	return &lifecycleListener{acceptCalled: make(chan struct{}), closed: make(chan struct{})}
}

func (l *lifecycleListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.acceptCalled) })
	<-l.closed
	return nil, net.ErrClosed
}

func (l *lifecycleListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *lifecycleListener) Addr() net.Addr { return lifecycleAddr("127.0.0.1:0") }

type lifecycleAddr string

func (a lifecycleAddr) Network() string { return "tcp" }
func (a lifecycleAddr) String() string  { return string(a) }

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

func TestLegacyAddLoaderUsesTypedRegistry(t *testing.T) {
	core := NewWebCore(WebConfig{})
	core.AddLoader(testWebClient{tgid: 42}, "loader", legacyWebDB{})
	clients := core.ListClients()
	if len(clients) != 1 || clients[0].ID != 42 || clients[0].Loader != "loader" {
		t.Fatalf("legacy registration = %#v", clients)
	}
	if DefaultFallbackTGID == 0 {
		t.Fatal("compatibility constant was removed")
	}
	core.AddLoader(testWebClient{}, "invalid", legacyWebDB{})
	if len(core.ListClients()) != 1 {
		t.Fatal("legacy AddLoader registered a fake fallback identity")
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

	if got := clientIP(req); got != "10.0.0.1" {
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

func TestClientIPRejectsInvalidTrustedHeader(t *testing.T) {
	t.Setenv("GOROKU_TRUST_PROXY_HEADERS", "true")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("CF-Connecting-IP", `<script>alert(1)</script>`)

	if got := clientIP(req); got != "" {
		t.Fatalf("invalid proxy IP must not reach auth messages, got %q", got)
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

func TestRandomTokenFailsClosed(t *testing.T) {
	if token, err := randomTokenFrom(failingReader{}, 8); err == nil || token != "" {
		t.Fatalf("expected entropy failure without a token, got token=%q err=%v", token, err)
	}
}

func TestSameOriginRejectsMissingOriginAndReferer(t *testing.T) {
	req := httptest.NewRequest("POST", "https://example.com/set_api", nil)
	req.Host = "example.com"
	if sameOrigin(req) {
		t.Fatal("missing Origin and Referer must fail closed")
	}
}

func testSessionCookies(t *testing.T, web *Web) (session, csrf string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	session, err := web.createSession(rec, req)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case sessionCookieName:
			session = c.Value
		case csrfCookieName:
			csrf = c.Value
		}
	}
	if session == "" || csrf == "" {
		t.Fatalf("missing session/csrf cookies: session=%q csrf=%q", session, csrf)
	}
	if c := web.sessionForToken(session); c == nil || c.CSRFToken != csrf {
		t.Fatalf("session store mismatch: %#v", c)
	}
	return session, csrf
}

func TestSetupTokenSingleUseAfterExchange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOROKU_SETUP_TOKEN", "setup-once")
	var saved map[string]any
	web := NewWeb(WebConfig{
		SetupToken: "setup-once",
		DataRoot:   dir,
		SaveConfig: func(key string, value any) bool {
			if saved == nil {
				saved = make(map[string]any)
			}
			saved[key] = value
			return true
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.Header.Set("X-Goroku-Setup-Token", "setup-once")
	rec := httptest.NewRecorder()
	web.WebAuthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first exchange status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "goroku_session_") {
		t.Fatalf("expected session token, got %q", body)
	}
	var gotCSRF, gotSetup string
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case csrfCookieName:
			gotCSRF = c.Value
		case setupCookieName:
			gotSetup = c.Value
		}
	}
	if gotCSRF == "" {
		t.Fatal("exchange must mint CSRF cookie")
	}
	if gotSetup != "" {
		t.Fatalf("setup cookie must be cleared after exchange, got %q", gotSetup)
	}
	if web.checkSetupToken(httptest.NewRequest(http.MethodGet, "/?setup_token=setup-once", nil)) {
		t.Fatal("setup token must be consumed after exchange")
	}
	if !SetupCompleted(dir) {
		t.Fatal("exchange must write durable setup-completed marker")
	}
	if saved == nil || saved[setupCompletedConfigKey] != true {
		t.Fatalf("exchange must persist config marker, got %#v", saved)
	}
	if os.Getenv("GOROKU_SETUP_TOKEN") != "" {
		t.Fatal("process env setup token must be unset after exchange")
	}

	reuse := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	reuse.Header.Set("X-Goroku-Setup-Token", "setup-once")
	reuseRec := httptest.NewRecorder()
	web.WebAuthHandler(reuseRec, reuse)
	if reuseRec.Code != http.StatusUnauthorized {
		t.Fatalf("reused setup token status=%d body=%q", reuseRec.Code, reuseRec.Body.String())
	}
}

func TestSetupTokenConsumedOnFinishLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOROKU_SETUP_TOKEN", "setup-login")
	web := NewWeb(WebConfig{
		SetupToken: "setup-login",
		DataRoot:   dir,
		OnLogin:    func(webiface.TelegramClient) error { return nil },
	})
	web.pendingClient = testWebClient{tgid: 7}
	if err := web.finishPendingLogin(testWebClient{tgid: 7}); err != nil {
		t.Fatalf("finishPendingLogin: %v", err)
	}
	if web.checkSetupToken(httptest.NewRequest(http.MethodGet, "/?setup_token=setup-login", nil)) {
		t.Fatal("setup token must be force-consumed on initial login")
	}
	if !SetupCompleted(dir) {
		t.Fatal("finish login must write durable setup-completed marker")
	}
	if os.Getenv("GOROKU_SETUP_TOKEN") != "" {
		t.Fatal("process env setup token must be unset after login")
	}
}

func TestSetupExchangeConcurrentSingleSession(t *testing.T) {
	web := NewWeb(WebConfig{SetupToken: "race-setup", DataRoot: t.TempDir()})
	const n = 32
	var (
		wg       sync.WaitGroup
		okCount  atomic.Int64
		sessions sync.Map
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
			req.Header.Set("X-Goroku-Setup-Token", "race-setup")
			rec := httptest.NewRecorder()
			web.WebAuthHandler(rec, req)
			if rec.Code == http.StatusOK {
				okCount.Add(1)
				sessions.Store(strings.TrimSpace(rec.Body.String()), struct{}{})
			}
		}()
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("expected exactly one successful exchange, got %d", okCount.Load())
	}
	web.mu.RLock()
	defer web.mu.RUnlock()
	if len(web.sessions) != 1 {
		t.Fatalf("expected one session in store, got %d", len(web.sessions))
	}
	count := 0
	sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 1 {
		t.Fatalf("expected one distinct session body, got %d", count)
	}
}

func TestSetupTokenNotRearmedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewWeb(WebConfig{SetupToken: "restart-secret", DataRoot: dir})
	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.Header.Set("X-Goroku-Setup-Token", "restart-secret")
	rec := httptest.NewRecorder()
	first.WebAuthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first exchange status=%d", rec.Code)
	}

	// Simulate process restart with leftover env token and durable marker present.
	t.Setenv("GOROKU_SETUP_TOKEN", "restart-secret")
	second := NewWeb(WebConfig{SetupToken: "restart-secret", DataRoot: dir})
	if second.setupToken != "" {
		t.Fatal("NewWeb must not re-arm setup token when setup completed marker exists")
	}
	reuse := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	reuse.Header.Set("X-Goroku-Setup-Token", "restart-secret")
	reuseRec := httptest.NewRecorder()
	second.WebAuthHandler(reuseRec, reuse)
	if reuseRec.Code != http.StatusUnauthorized {
		t.Fatalf("restart re-arm exchange status=%d body=%q", reuseRec.Code, reuseRec.Body.String())
	}
	if os.Getenv("GOROKU_SETUP_TOKEN") != "" {
		t.Fatal("NewWeb should unset leftover env setup token when setup completed")
	}
}

func TestCSRFRequiredOnMutatingRoutes(t *testing.T) {
	web := NewWeb(WebConfig{})
	session, csrf := testSessionCookies(t, web)
	handler := web.checkSessionMiddleware(func(wr http.ResponseWriter, r *http.Request) {
		writeString(wr, "ok")
	})

	// Missing CSRF
	req := httptest.NewRequest(http.MethodPost, "/set_api", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", rec.Code)
	}

	// Wrong CSRF
	req = httptest.NewRequest(http.MethodPost, "/set_api", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set(csrfHeaderName, "wrong-token-value-012345678901234")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong CSRF status=%d", rec.Code)
	}

	// Valid CSRF header
	req = httptest.NewRequest(http.MethodPost, "/set_api", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set(csrfHeaderName, csrf)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("valid CSRF status=%d body=%q", rec.Code, rec.Body.String())
	}

	// GET remains CSRF-exempt
	req = httptest.NewRequest(http.MethodGet, "/set_api", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET should be CSRF-exempt, status=%d", rec.Code)
	}
}

func TestMutatingRejectsMissingOriginAndRefererWithSessionCookie(t *testing.T) {
	web := NewWeb(WebConfig{})
	session, csrf := testSessionCookies(t, web)
	handler := web.checkSessionMiddleware(func(wr http.ResponseWriter, r *http.Request) {
		writeString(wr, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/set_api", nil)
	req.Host = "example.com"
	req.Header.Set(csrfHeaderName, csrf)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing Origin+Referer status=%d", rec.Code)
	}
}

func TestSessionExpiry(t *testing.T) {
	web := NewWeb(WebConfig{})
	session, csrf := testSessionCookies(t, web)
	web.mu.Lock()
	sess := web.sessions[session]
	sess.Expiry = time.Now().Add(-time.Second)
	web.sessions[session] = sess
	web.mu.Unlock()

	if web.sessionForToken(session) != nil {
		t.Fatal("expired session must be rejected")
	}
	handler := web.checkSessionMiddleware(func(wr http.ResponseWriter, r *http.Request) {
		writeString(wr, "ok")
	})
	req := httptest.NewRequest(http.MethodPost, "/set_api", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set(csrfHeaderName, csrf)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status=%d", rec.Code)
	}
}

func TestSessionRotationOnReauth(t *testing.T) {
	web := NewWeb(WebConfig{})
	oldSession, oldCSRF := testSessionCookies(t, web)

	// Missing Origin/Referer must fail closed for cookie-session re-auth.
	missingOrigin := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	missingOrigin.Host = "example.com"
	missingOrigin.Header.Set(csrfHeaderName, oldCSRF)
	missingOrigin.AddCookie(&http.Cookie{Name: sessionCookieName, Value: oldSession})
	missingRec := httptest.NewRecorder()
	web.WebAuthHandler(missingRec, missingOrigin)
	if missingRec.Code != http.StatusForbidden {
		t.Fatalf("reauth without origin status=%d", missingRec.Code)
	}

	// Missing CSRF must fail.
	missingCSRF := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	missingCSRF.Host = "example.com"
	missingCSRF.Header.Set("Origin", "http://example.com")
	missingCSRF.AddCookie(&http.Cookie{Name: sessionCookieName, Value: oldSession})
	missingCSRFRec := httptest.NewRecorder()
	web.WebAuthHandler(missingCSRFRec, missingCSRF)
	if missingCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("reauth without CSRF status=%d", missingCSRFRec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set(csrfHeaderName, oldCSRF)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: oldSession})
	rec := httptest.NewRecorder()
	web.WebAuthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reauth status=%d body=%q", rec.Code, rec.Body.String())
	}
	newSession := strings.TrimSpace(rec.Body.String())
	if newSession == "" || newSession == oldSession {
		t.Fatalf("session was not rotated: old=%q new=%q", oldSession, newSession)
	}
	if web.sessionForToken(oldSession) != nil {
		t.Fatal("old session token must be invalidated")
	}
	newSess := web.sessionForToken(newSession)
	if newSess == nil {
		t.Fatal("new session missing")
	}
	if newSess.CSRFToken == "" || newSess.CSRFToken == oldCSRF {
		t.Fatalf("CSRF was not rotated: old=%q new=%q", oldCSRF, newSess.CSRFToken)
	}
}

func TestFinishLoginAndInitQRRequirePOST(t *testing.T) {
	web := NewWeb(WebConfig{})
	session, csrf := testSessionCookies(t, web)

	for _, path := range []string{"/finish_login", "/init_qr_login"} {
		var handler http.HandlerFunc
		switch path {
		case "/finish_login":
			handler = web.checkSessionMiddleware(web.FinishLoginHandler)
		case "/init_qr_login":
			handler = web.checkSessionMiddleware(web.InitQRLoginHandler)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "example.com"
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set(csrfHeaderName, csrf)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s GET status=%d", path, rec.Code)
		}
	}

	// get_qr_url may not mutate on GET when no QR state exists
	req := httptest.NewRequest(http.MethodGet, "/get_qr_url", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set(csrfHeaderName, csrf)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	web.checkSessionMiddleware(web.GetQRURLHandler)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get_qr_url GET mutate path status=%d", rec.Code)
	}
}

func TestLogoutInvalidatesSessionAndCSRF(t *testing.T) {
	web := NewWeb(WebConfig{})
	session, csrf := testSessionCookies(t, web)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set(csrfHeaderName, csrf)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	web.checkSessionMiddleware(web.LogoutHandler)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status=%d", rec.Code)
	}
	if web.sessionForToken(session) != nil {
		t.Fatal("logout must invalidate session")
	}
}

func TestCheckSessionExposesCSRF(t *testing.T) {
	web := NewWeb(WebConfig{})
	session, csrf := testSessionCookies(t, web)
	req := httptest.NewRequest(http.MethodPost, "/check_session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	web.CheckSessionHandler(rec, req)
	if rec.Body.String() != "1" {
		t.Fatalf("check_session body=%q", rec.Body.String())
	}
	if got := rec.Header().Get(csrfHeaderName); got != csrf {
		t.Fatalf("CSRF header=%q want %q", got, csrf)
	}
	foundCookie := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName && c.Value == csrf && !c.HttpOnly {
			foundCookie = true
		}
	}
	if !foundCookie {
		t.Fatal("csrf cookie must be present and non-HttpOnly")
	}
}

func TestSetupTokenNotFullSessionSubstitute(t *testing.T) {
	web := NewWeb(WebConfig{SetupToken: "setup-only"})
	handler := web.checkSessionMiddleware(func(wr http.ResponseWriter, r *http.Request) {
		writeString(wr, "ok")
	})
	req := httptest.NewRequest(http.MethodPost, "/set_api", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("X-Goroku-Setup-Token", "setup-only")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup token must not authorize mutating routes, status=%d", rec.Code)
	}
}

func TestRegisterClientRejectsZeroID(t *testing.T) {
	core := NewWebCore(WebConfig{})
	err := core.RegisterClient(RuntimeClient{Client: testWebClient{tgid: 42}})
	if !errors.Is(err, ErrInvalidClientID) {
		t.Fatalf("expected invalid ID error, got %v", err)
	}
	if clients := core.ListClients(); len(clients) != 0 {
		t.Fatalf("invalid client was registered: %#v", clients)
	}
}

func TestRegisterClientRejectsDuplicateID(t *testing.T) {
	web := NewWeb(WebConfig{})
	first := RuntimeClient{ID: 42, Client: testWebClient{tgid: 42}, Loader: "first"}
	if err := web.RegisterClient(first); err != nil {
		t.Fatalf("register first client: %v", err)
	}
	err := web.RegisterClient(RuntimeClient{ID: 42, Client: testWebClient{tgid: 42}, Loader: "replacement"})
	if !errors.Is(err, ErrDuplicateClient) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	clients := web.ListClients()
	if len(clients) != 1 || clients[0].Loader != "first" {
		t.Fatalf("duplicate registration overwrote original: %#v", clients)
	}
}

func TestListClientsReturnsStableSnapshot(t *testing.T) {
	web := NewWeb(WebConfig{})
	for _, id := range []int64{3, 1, 2} {
		if err := web.RegisterClient(RuntimeClient{ID: id, Client: testWebClient{tgid: id}}); err != nil {
			t.Fatalf("register %d: %v", id, err)
		}
	}
	snapshot := web.ListClients()
	for i, id := range []int64{1, 2, 3} {
		if snapshot[i].ID != id {
			t.Fatalf("snapshot is not ordered by ID: %#v", snapshot)
		}
	}
	snapshot[0] = RuntimeClient{}
	if clients := web.ListClients(); clients[0].ID != 1 {
		t.Fatalf("caller modified registry through snapshot: %#v", clients)
	}
}

func TestUnregisterClientCancelsAuthWait(t *testing.T) {
	web := NewWeb(WebConfig{})
	client := &authTestClient{tgid: 42, notified: make(chan struct{})}
	if err := web.RegisterClient(RuntimeClient{ID: client.tgid, Client: client}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		web.WebAuthHandler(recorder, req)
		close(done)
	}()

	select {
	case <-client.notified:
	case <-time.After(time.Second):
		t.Fatal("auth notification was not sent")
	}
	web.mu.RLock()
	var token string
	for pendingToken := range web.pendingAuths {
		token = pendingToken
	}
	web.mu.RUnlock()
	if token == "" {
		t.Fatal("auth token was not pending")
	}
	if !web.UnregisterClient(client.tgid) {
		t.Fatal("client was not unregistered during auth")
	}
	if web.ApproveWebAuth(token) {
		t.Fatal("approval succeeded for an unregistered client")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after client unregistration")
	}

	web.mu.RLock()
	defer web.mu.RUnlock()
	if len(web.pendingAuths) != 0 {
		t.Fatalf("pending auth leaked after unregistration: %d", len(web.pendingAuths))
	}
	if len(web.sessions) != 0 {
		t.Fatalf("unregistered client created a session: %d", len(web.sessions))
	}
}

func TestStopCancelsAuthWaitWhenServerIsNotRunning(t *testing.T) {
	core := NewWebCore(WebConfig{})
	client := &authTestClient{tgid: 42, notified: make(chan struct{})}
	if err := core.RegisterClient(RuntimeClient{ID: client.tgid, Client: client}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.RemoteAddr = "192.0.2.13:1234"
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		core.WebAuthHandler(recorder, req)
		close(done)
	}()

	select {
	case <-client.notified:
	case <-time.After(time.Second):
		t.Fatal("auth notification was not sent")
	}
	core.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the auth wait")
	}

	core.mu.RLock()
	defer core.mu.RUnlock()
	if len(core.pendingAuths) != 0 || len(core.sessions) != 0 {
		t.Fatalf("Stop left auth state: pending=%d sessions=%d", len(core.pendingAuths), len(core.sessions))
	}
}

func TestStopDuringStartPreventsLateServer(t *testing.T) {
	core := NewWebCore(WebConfig{})
	listener := newLifecycleListener()
	listenEntered := make(chan struct{})
	releaseListen := make(chan struct{})
	stopLinearized := make(chan struct{})
	core.listen = func(ctx context.Context, _ string, _ string) (net.Listener, error) {
		close(listenEntered)
		<-ctx.Done()
		close(stopLinearized)
		<-releaseListen
		return listener, nil
	}

	core.StartAsync(0, false)
	<-listenEntered
	stopped := make(chan struct{})
	go func() {
		core.Stop()
		close(stopped)
	}()
	<-stopLinearized

	select {
	case <-stopped:
		t.Fatal("Stop returned while startup was still blocked")
	default:
	}
	close(releaseListen)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not wait for cancelled startup")
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("listener returned after cancellation was not closed")
	}
	select {
	case <-listener.acceptCalled:
		t.Fatal("server accepted on a listener created after Stop")
	default:
	}

	core.serverMu.RLock()
	defer core.serverMu.RUnlock()
	if core.state != webStopped || core.running || core.server != nil || core.proxypasser != nil {
		t.Fatalf("unexpected stopped state: state=%d running=%v server=%v proxy=%v", core.state, core.running, core.server, core.proxypasser)
	}
}

func TestCloseTimeoutDuringStartupIsRetryable(t *testing.T) {
	core := NewWebCore(WebConfig{})
	listenEntered := make(chan struct{})
	releaseListen := make(chan struct{})
	core.listen = func(ctx context.Context, _, _ string) (net.Listener, error) {
		close(listenEntered)
		<-ctx.Done()
		<-releaseListen
		return nil, ctx.Err()
	}
	core.StartAsync(0, false)
	<-listenEntered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := core.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v, want deadline", err)
	}
	close(releaseListen)
	if err := core.Close(context.Background()); err != nil {
		t.Fatalf("retry Close error = %v", err)
	}
	core.serverMu.RLock()
	defer core.serverMu.RUnlock()
	if core.state != webStopped || core.running {
		t.Fatalf("state after retry = %d, running=%v", core.state, core.running)
	}
}

func TestStartContextFailedListenIsObservableAndRetryable(t *testing.T) {
	core := NewWebCore(WebConfig{})
	cause := errors.New("bind failed")
	listener := newLifecycleListener()
	var calls int
	core.listen = func(context.Context, string, string) (net.Listener, error) {
		calls++
		if calls == 1 {
			return nil, cause
		}
		return listener, nil
	}
	if err := core.StartContext(context.Background(), 0, false); !errors.Is(err, cause) {
		t.Fatalf("first StartContext error = %v, want bind failure", err)
	}
	core.serverMu.RLock()
	state := core.state
	running := core.running
	core.serverMu.RUnlock()
	if state != webStopped || running {
		t.Fatalf("failed start state = %d, running=%v", state, running)
	}
	if err := core.StartContext(context.Background(), 0, false); err != nil {
		t.Fatalf("retry StartContext error = %v", err)
	}
	select {
	case <-listener.acceptCalled:
	case <-time.After(time.Second):
		t.Fatal("retry did not start serving")
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStartContextSharesFailedGeneration(t *testing.T) {
	core := NewWebCore(WebConfig{})
	cause := errors.New("shared bind failure")
	listenStarted := make(chan struct{})
	releaseListen := make(chan struct{})
	var listenCalls atomic.Int32
	core.listen = func(context.Context, string, string) (net.Listener, error) {
		if listenCalls.Add(1) == 1 {
			close(listenStarted)
		}
		<-releaseListen
		return nil, cause
	}

	const callers = 16
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- core.StartContext(context.Background(), 0, false) }()
	}
	<-listenStarted
	deadline := time.Now().Add(time.Second)
	for {
		core.serverMu.RLock()
		generation := core.startGeneration
		waiters := 0
		if generation != nil {
			waiters = generation.waiters
		}
		core.serverMu.RUnlock()
		if waiters == callers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup waiters = %d, want %d", waiters, callers)
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseListen)
	for i := 0; i < callers; i++ {
		if err := <-results; !errors.Is(err, cause) {
			t.Fatalf("StartContext error = %v, want shared bind failure", err)
		}
	}
	if got := listenCalls.Load(); got != 1 {
		t.Fatalf("listen calls = %d, want 1", got)
	}
	core.serverMu.RLock()
	state := core.state
	core.serverMu.RUnlock()
	if state != webStopped {
		t.Fatalf("state after shared failure = %d", state)
	}
}

func TestNewWebCoreHasNoGlobalInstance(t *testing.T) {
	first := NewWebCore(WebConfig{})
	second := NewWebCore(WebConfig{})
	if first == nil || second == nil {
		t.Fatal("NewWebCore returned nil")
	}
	if first == second {
		t.Fatal("NewWebCore must return independent cores")
	}
	// Cores must not share mutable server lifecycle state.
	first.port = 1
	if second.port == 1 {
		t.Fatal("WebCore instances share Server state")
	}
}

func TestStartAsyncStopHasNoLateListener(t *testing.T) {
	core := NewWebCore(WebConfig{})
	listenCalls := make(chan struct{}, 2)
	core.listen = func(ctx context.Context, _, _ string) (net.Listener, error) {
		listenCalls <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	core.StartAsync(0, true)
	core.Stop()
	if got := len(listenCalls); got > 1 {
		t.Fatalf("listener created %d times", got)
	}
	select {
	case <-listenCalls:
	default:
	}
	select {
	case <-listenCalls:
		t.Fatal("listener creation occurred after Stop returned")
	default:
	}

	core.Stop()
}

func TestWebCoreRepeatedStartStop(t *testing.T) {
	core := NewWebCore(WebConfig{})
	listeners := make(chan *lifecycleListener, 2)
	core.listen = func(context.Context, string, string) (net.Listener, error) {
		listener := newLifecycleListener()
		listeners <- listener
		return listener, nil
	}

	for i := 0; i < 2; i++ {
		core.StartAsync(0, false)
		listener := <-listeners
		select {
		case <-listener.acceptCalled:
		case <-time.After(time.Second):
			t.Fatalf("start %d did not serve", i+1)
		}
		core.Stop()
		core.Stop()
		select {
		case <-listener.closed:
		default:
			t.Fatalf("stop %d did not close listener", i+1)
		}
	}
}

func TestWebAuthNotificationErrorCleansPending(t *testing.T) {
	web := NewWeb(WebConfig{})
	client := &authTestClient{tgid: 42, notified: make(chan struct{}), sendErr: errors.New("send failed")}
	if err := web.RegisterClient(RuntimeClient{ID: client.tgid, Client: client}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.RemoteAddr = "192.0.2.11:1234"
	recorder := httptest.NewRecorder()

	web.WebAuthHandler(recorder, req)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected notification failure, got %d", recorder.Code)
	}
	web.mu.RLock()
	defer web.mu.RUnlock()
	if len(web.pendingAuths) != 0 {
		t.Fatalf("pending auth leaked after send error: %d", len(web.pendingAuths))
	}
}

func TestWebAuthPendingIsBounded(t *testing.T) {
	web := NewWeb(WebConfig{})
	client := &authTestClient{tgid: 42, notified: make(chan struct{})}
	if err := web.RegisterClient(RuntimeClient{ID: client.tgid, Client: client}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	for i := 0; i < maxPendingAuths; i++ {
		token := string(rune('a' + i))
		web.pendingAuths[token] = &PendingAuth{Token: token, Approved: make(chan struct{}), Expiry: time.Now().Add(time.Minute)}
	}
	req := httptest.NewRequest(http.MethodPost, "/web_auth", nil)
	req.RemoteAddr = "192.0.2.12:1234"
	recorder := httptest.NewRecorder()

	web.WebAuthHandler(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected bounded pending rejection, got %d", recorder.Code)
	}
	if len(web.pendingAuths) != maxPendingAuths {
		t.Fatalf("pending auth count changed: %d", len(web.pendingAuths))
	}
}

func TestRegisterClientRejectsMismatchedClientID(t *testing.T) {
	web := NewWeb(WebConfig{})
	client := &authTestClient{tgid: 0, notified: make(chan struct{})}
	err := web.RegisterClient(RuntimeClient{ID: 1, Client: client})
	if !errors.Is(err, ErrInvalidClientID) {
		t.Fatalf("expected mismatched ID rejection, got %v", err)
	}
	if clients := web.ListClients(); len(clients) != 0 {
		t.Fatalf("mismatched client was registered: %#v", clients)
	}
}

func TestHTTPServerSecurityLimits(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if server.ReadTimeout == 0 || server.ReadHeaderTimeout == 0 || server.WriteTimeout == 0 || server.IdleTimeout == 0 {
		t.Fatalf("all HTTP timeouts must be configured: %#v", server)
	}
	if server.MaxHeaderBytes <= 0 {
		t.Fatal("MaxHeaderBytes must be configured")
	}
	if !strings.HasPrefix(server.Addr, "127.0.0.1:") {
		t.Fatalf("default test bind is not localhost: %q", server.Addr)
	}
}

func TestGetURLDoesNotStartDisabledTunnel(t *testing.T) {
	core := NewWebCore(WebConfig{})
	core.port = 8080
	core.proxyPass = false
	tunnel := NewSSHTunnelWithProviders(8080, nil, nil)
	core.proxypasser = &ProxyPasser{tunnels: []*SSHTunnel{tunnel}}

	if got := core.GetURL(true); got != "http://127.0.0.1:8080" {
		t.Fatalf("expected local URL, got %q", got)
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	if tunnel.cancel != nil {
		t.Fatal("disabled proxy pass started an SSH tunnel")
	}
}

func TestStoppedProxyPasserDoesNotStartTunnel(t *testing.T) {
	tunnel := NewSSHTunnelWithProviders(8080, nil, nil)
	proxy := &ProxyPasser{tunnels: []*SSHTunnel{tunnel}}
	proxy.Stop()

	if got := proxy.GetURL(time.Second); got != "" {
		t.Fatalf("stopped proxy returned URL %q", got)
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	if tunnel.cancel != nil {
		t.Fatal("stopped proxy started an SSH tunnel")
	}
}

func TestRuntimeRegistryConcurrentAccess(t *testing.T) {
	core := NewWebCore(WebConfig{})
	if err := core.RegisterClient(RuntimeClient{ID: 1000, Client: testWebClient{tgid: 1000}}); err != nil {
		t.Fatalf("register auth client: %v", err)
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 50; i++ {
		wg.Add(3)
		go func(id int64) {
			defer wg.Done()
			if err := core.RegisterClient(RuntimeClient{ID: id, Client: testWebClient{tgid: id}}); err != nil {
				t.Errorf("RegisterClient(%d): %v", id, err)
				return
			}
			core.UnregisterClient(id)
		}(i)
		go func() {
			defer wg.Done()
			for range core.ListClients() {
			}
		}()
		go func(id int64) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(http.MethodPost, "/web_auth", nil).WithContext(ctx)
			req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", id)
			core.WebAuthHandler(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()
	clients := core.ListClients()
	if len(clients) != 1 || clients[0].ID != 1000 {
		t.Fatalf("unexpected final registry snapshot: %#v", clients)
	}
}

func TestHealthEndpointsHaveNoSecrets(t *testing.T) {
	dir := t.TempDir()
	web := NewWeb(WebConfig{DataRoot: dir})
	if err := web.RegisterClient(RuntimeClient{ID: 42, Client: testWebClient{tgid: 42}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	web.HealthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) && !strings.Contains(body, `"status": "ok"`) {
		t.Fatalf("health body missing status: %q", body)
	}
	if !strings.Contains(body, `"clients":1`) && !strings.Contains(body, `"clients": 1`) {
		t.Fatalf("health body missing clients: %q", body)
	}
	for _, secretish := range []string{"token", "password", "api_hash", "session"} {
		if strings.Contains(strings.ToLower(body), secretish) {
			t.Fatalf("health body must not contain %q: %q", secretish, body)
		}
	}

	hz := httptest.NewRecorder()
	web.HealthzHandler(hz, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if hz.Code != http.StatusOK || strings.TrimSpace(hz.Body.String()) != "ok" {
		t.Fatalf("healthz=%d %q", hz.Code, hz.Body.String())
	}
	rz := httptest.NewRecorder()
	web.ReadyzHandler(rz, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rz.Code != http.StatusOK || strings.TrimSpace(rz.Body.String()) != "ok" {
		t.Fatalf("readyz=%d %q", rz.Code, rz.Body.String())
	}
}
