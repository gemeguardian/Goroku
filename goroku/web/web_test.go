package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebRootHandlerAndSetupToken(t *testing.T) {
	tempDir := t.TempDir()
	resourcesDir := filepath.Join(tempDir, "web-resources")
	err := os.Mkdir(resourcesDir, 0755)
	if err != nil {
		t.Fatalf("failed to create web-resources: %v", err)
	}

	baseTemplate := `<html><head>{% block head %}{% endblock %}</head><body>{% block content %}{% endblock %}{% block after %}{% endblock %}</body></html>`
	rootTemplate := `
	{% block head %}<title>Test Title</title>{% endblock %}
	{% block content %}<h1>Hello World</h1>{% endblock %}
	{% block after %}<script>console.log("after");</script>{% endblock %}
	`

	err = os.WriteFile(filepath.Join(resourcesDir, "base.jinja2"), []byte(baseTemplate), 0644)
	if err != nil {
		t.Fatalf("failed to write base.jinja2: %v", err)
	}
	err = os.WriteFile(filepath.Join(resourcesDir, "root.jinja2"), []byte(rootTemplate), 0644)
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
