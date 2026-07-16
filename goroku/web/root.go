package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"goroku/goroku/chatref"
	"goroku/goroku/logger"
	"goroku/goroku/webiface"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"
)

// L returns the package-level zap logger.
func L() *zap.Logger { return logger.L() }

// writeString writes s to w and logs failures at debug level. It centralizes
// response writes so callers can ignore the error without losing observability.
func writeString(wr http.ResponseWriter, s string) {
	if _, err := wr.Write([]byte(s)); err != nil {
		L().Debug("failed to write response", zap.Error(err))
	}
}

type TelegramClient interface {
	webiface.TelegramClient
}

type WebConfig struct {
	ApiToken   string
	SetupToken string
	DataRoot   string
	Connection any // TODO: type when connection type is known
	Proxy      any // TODO: type when proxy type is known
	SaveConfig func(key string, value any) bool
	Restart    func()
	GetClient  func() webiface.TelegramClient
	OnLogin    func(client webiface.TelegramClient) error
}

type PendingAuth struct {
	Token      string
	Approved   chan struct{}
	Cancelled  chan struct{}
	ClientID   int64
	generation uint64
	approveMu  sync.Once
	cancelMu   sync.Once
	Expiry     time.Time
}

type WebSession struct {
	Token     string
	CSRFToken string
	Expiry    time.Time
}

const (
	sessionCookieName       = "session"
	csrfCookieName          = "csrf_token"
	csrfHeaderName          = "X-CSRF-Token"
	csrfFormField           = "_csrf"
	setupCookieName         = "setup_token"
	setupCompletedFileName  = "goroku-setup-completed"
	setupCompletedConfigKey = "web_setup_completed"
	sessionTTL              = 6 * time.Hour
	shortBodyLimit          = 8 * 1024
	webAuthTTL              = 60 * time.Second
	maxPendingAuths         = 64
	sessionTokenSize        = 32
	csrfTokenSize           = 32
)

var errSetupTokenRequired = errors.New("setup token required")

type inlineBotProvider interface {
	GetBotAPI() *tgbotapi.BotAPI
	PopWebAuthToken(token string) bool
}

type Web struct {
	mu             sync.RWMutex
	signInClients  map[string]webiface.TelegramClient
	pendingClient  webiface.TelegramClient
	qrLogin        any
	qrTaskActive   bool
	twoFANeeded    bool
	sessions       map[string]WebSession
	ratelimit      map[string][]int64
	apiToken       string
	setupToken     string
	dataRoot       string
	connection     any // TODO
	proxy          any // TODO
	saveConfig     func(key string, value any) bool
	restart        func()
	onLogin        func(client webiface.TelegramClient) error
	clientData     map[int64]RuntimeClient
	apiSetChan     chan struct{}
	clientsSetChan chan struct{}
	getClient      func() webiface.TelegramClient
	pendingAuths   map[string]*PendingAuth
	nextGeneration uint64
	authAccepting  bool
}

// RuntimeClient is one fully initialized client available to the web runtime.
type RuntimeClient struct {
	ID         int64
	Client     webiface.TelegramClient
	Loader     any
	Database   webiface.Database
	generation uint64
}

var (
	ErrInvalidClientID  = errors.New("web runtime client ID must be positive")
	ErrNilRuntimeClient = errors.New("web runtime client is nil")
	ErrDuplicateClient  = errors.New("web runtime client ID is already registered")
)

func NewWeb(cfg WebConfig) *Web {
	setupToken := strings.TrimSpace(cfg.SetupToken)
	// Do not re-arm a setup token after durable onboarding completion.
	if setupToken != "" && SetupCompleted(cfg.DataRoot) {
		setupToken = ""
		_ = os.Unsetenv("GOROKU_SETUP_TOKEN")
	}
	return &Web{
		signInClients:  make(map[string]webiface.TelegramClient),
		sessions:       make(map[string]WebSession),
		ratelimit:      make(map[string][]int64),
		apiToken:       cfg.ApiToken,
		setupToken:     setupToken,
		dataRoot:       cfg.DataRoot,
		connection:     cfg.Connection,
		proxy:          cfg.Proxy,
		saveConfig:     cfg.SaveConfig,
		restart:        cfg.Restart,
		onLogin:        cfg.OnLogin,
		clientData:     make(map[int64]RuntimeClient),
		apiSetChan:     make(chan struct{}),
		clientsSetChan: make(chan struct{}),
		getClient:      cfg.GetClient,
		pendingAuths:   make(map[string]*PendingAuth),
		authAccepting:  true,
	}
}

// SetupCompleted reports whether initial web setup was durably finished.
func SetupCompleted(dataRoot string) bool {
	if dataRoot == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dataRoot, setupCompletedFileName))
	return err == nil
}

// RegisterClient adds a fully initialized runtime. IDs are unique and are
// never overwritten implicitly.
func (w *Web) RegisterClient(runtime RuntimeClient) error {
	if runtime.ID <= 0 {
		return ErrInvalidClientID
	}
	if runtime.Client == nil {
		return ErrNilRuntimeClient
	}
	if runtime.Client.TGIDValue() != runtime.ID {
		return fmt.Errorf("%w: runtime ID %d does not match client ID %d", ErrInvalidClientID, runtime.ID, runtime.Client.TGIDValue())
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.clientData[runtime.ID]; exists {
		return fmt.Errorf("%w: %d", ErrDuplicateClient, runtime.ID)
	}
	w.nextGeneration++
	runtime.generation = w.nextGeneration
	w.clientData[runtime.ID] = runtime
	return nil
}

// UnregisterClient removes id from the runtime registry and reports whether it existed.
func (w *Web) UnregisterClient(id int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.clientData[id]; !exists {
		return false
	}
	delete(w.clientData, id)
	w.cancelPendingAuthsLocked(id, false)
	return true
}

func (w *Web) cancelPendingAuthsLocked(clientID int64, all bool) {
	for token, auth := range w.pendingAuths {
		if !all && auth.ClientID != clientID {
			continue
		}
		delete(w.pendingAuths, token)
		if auth.Cancelled != nil {
			auth.cancelMu.Do(func() { close(auth.Cancelled) })
		}
	}
}

func (w *Web) stopAuth() {
	w.mu.Lock()
	w.authAccepting = false
	w.cancelPendingAuthsLocked(0, true)
	w.mu.Unlock()
}

func (w *Web) startAuth() {
	w.mu.Lock()
	w.authAccepting = true
	w.mu.Unlock()
}

// ListClients returns a stable snapshot without exposing the registry map.
func (w *Web) ListClients() []RuntimeClient {
	w.mu.RLock()
	clients := make([]RuntimeClient, 0, len(w.clientData))
	for _, runtime := range w.clientData {
		clients = append(clients, runtime)
	}
	w.mu.RUnlock()
	sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
	return clients
}

func (w *Web) clientCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.clientData)
}

func (w *Web) checkSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return w.sessionForToken(cookie.Value) != nil
}

func (w *Web) sessionForToken(token string) *WebSession {
	w.mu.Lock()
	defer w.mu.Unlock()
	sess, ok := w.sessions[token]
	if !ok {
		return nil
	}
	if time.Now().After(sess.Expiry) {
		delete(w.sessions, token)
		return nil
	}
	copy := sess
	return &copy
}

func (w *Web) mintSessionTokens() (session string, csrf string, err error) {
	token, err := randomToken(sessionTokenSize)
	if err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	csrf, err = randomToken(csrfTokenSize)
	if err != nil {
		return "", "", fmt.Errorf("generate csrf token: %w", err)
	}
	return "goroku_session_" + token, csrf, nil
}

func (w *Web) createSession(wr http.ResponseWriter, r *http.Request) (string, error) {
	session, csrf, err := w.mintSessionTokens()
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	w.sessions[session] = WebSession{Token: session, CSRFToken: csrf, Expiry: time.Now().Add(sessionTTL)}
	w.mu.Unlock()
	w.setSessionCookies(wr, r, session, csrf)
	return session, nil
}

func setupTokenCandidates(r *http.Request) []string {
	candidates := []string{
		r.Header.Get("X-Goroku-Setup-Token"),
		r.URL.Query().Get("setup_token"),
	}
	if cookie, err := r.Cookie(setupCookieName); err == nil {
		candidates = append(candidates, cookie.Value)
	}
	return candidates
}

// exchangeSetupSession atomically validates the setup token, mints one session,
// and consumes the token so concurrent exchanges cannot mint multiple sessions.
func (w *Web) exchangeSetupSession(wr http.ResponseWriter, r *http.Request) (string, error) {
	session, csrf, err := w.mintSessionTokens()
	if err != nil {
		return "", err
	}
	candidates := setupTokenCandidates(r)

	w.mu.Lock()
	expected := w.setupToken
	if expected == "" {
		w.mu.Unlock()
		return "", errSetupTokenRequired
	}
	matched := false
	for _, candidate := range candidates {
		if constantTimeEqualString(strings.TrimSpace(candidate), expected) {
			matched = true
			break
		}
	}
	if !matched {
		w.mu.Unlock()
		return "", errSetupTokenRequired
	}
	w.setupToken = ""
	w.sessions[session] = WebSession{Token: session, CSRFToken: csrf, Expiry: time.Now().Add(sessionTTL)}
	w.mu.Unlock()

	_ = os.Unsetenv("GOROKU_SETUP_TOKEN")
	w.persistSetupCompleted()
	w.removeInitialSetupURL()
	w.setSessionCookies(wr, r, session, csrf)
	w.clearSetupCookie(wr, r)
	return session, nil
}

func (w *Web) rotateSession(wr http.ResponseWriter, r *http.Request, oldToken string) (string, error) {
	session, csrf, err := w.mintSessionTokens()
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	if oldToken != "" {
		delete(w.sessions, oldToken)
	}
	w.sessions[session] = WebSession{Token: session, CSRFToken: csrf, Expiry: time.Now().Add(sessionTTL)}
	w.mu.Unlock()
	w.setSessionCookies(wr, r, session, csrf)
	return session, nil
}

func (w *Web) setSessionCookies(wr http.ResponseWriter, r *http.Request, session, csrf string) {
	secure := isHTTPS(r)
	expires := time.Now().Add(sessionTTL)
	maxAge := int(sessionTTL.Seconds())
	http.SetCookie(wr, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   maxAge,
	})
	http.SetCookie(wr, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrf,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   maxAge,
	})
}

func (w *Web) clearSessionCookies(wr http.ResponseWriter, r *http.Request) {
	secure := isHTTPS(r)
	for _, name := range []string{sessionCookieName, csrfCookieName, setupCookieName} {
		httpOnly := name != csrfCookieName
		http.SetCookie(wr, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: httpOnly,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

func (w *Web) clearSetupCookie(wr http.ResponseWriter, r *http.Request) {
	http.SetCookie(wr, &http.Cookie{
		Name:     setupCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func (w *Web) consumeSetupToken() {
	w.mu.Lock()
	w.setupToken = ""
	w.mu.Unlock()
	_ = os.Unsetenv("GOROKU_SETUP_TOKEN")
	w.persistSetupCompleted()
	w.removeInitialSetupURL()
}

func (w *Web) persistSetupCompleted() {
	if w.dataRoot != "" {
		path := filepath.Join(w.dataRoot, setupCompletedFileName)
		if err := os.WriteFile(path, []byte("1\n"), 0o600); err != nil {
			L().Warn("failed to write setup completed marker", zap.Error(err))
		}
	}
	if w.saveConfig != nil {
		_ = w.saveConfig(setupCompletedConfigKey, true)
	}
}

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

func (w *Web) checkSetupToken(r *http.Request) bool {
	w.mu.RLock()
	expected := w.setupToken
	w.mu.RUnlock()
	if expected == "" {
		return false
	}
	for _, token := range setupTokenCandidates(r) {
		if constantTimeEqualString(strings.TrimSpace(token), expected) {
			return true
		}
	}
	return false
}

func (w *Web) rememberSetupToken(wr http.ResponseWriter, r *http.Request) {
	if !w.checkSetupToken(r) {
		return
	}
	w.mu.RLock()
	token := w.setupToken
	w.mu.RUnlock()
	if token == "" {
		return
	}
	http.SetCookie(wr, &http.Cookie{
		Name:     setupCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(time.Hour),
		MaxAge:   3600,
	})
}

func (w *Web) csrfTokenFromRequest(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get(csrfHeaderName)); token != "" {
		return token
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseForm(); err == nil {
			return strings.TrimSpace(r.Form.Get(csrfFormField))
		}
	}
	return ""
}

func (w *Web) validCSRF(r *http.Request, sess *WebSession) bool {
	if sess == nil || sess.CSRFToken == "" {
		return false
	}
	return constantTimeEqualString(w.csrfTokenFromRequest(r), sess.CSRFToken)
}

func readLimitedBody(wr http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	r.Body = http.MaxBytesReader(wr, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(wr, "Request body too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return body, true
}

func (w *Web) removeInitialSetupURL() {
	if w.dataRoot == "" {
		return
	}
	if err := os.Remove(filepath.Join(w.dataRoot, "goroku-setup-url.txt")); err != nil && !os.IsNotExist(err) {
		L().Warn("failed to remove initial setup URL file", zap.Error(err))
	}
}

func (w *Web) checkSessionMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Error(wr, "Unauthorized: Please log in using the Telegram Web Auth button first.", http.StatusUnauthorized)
			return
		}
		sess := w.sessionForToken(cookie.Value)
		if sess == nil {
			http.Error(wr, "Unauthorized: Please log in using the Telegram Web Auth button first.", http.StatusUnauthorized)
			return
		}
		if isStateChangingMethod(r.Method) {
			// Cookie browser sessions must present Origin or Referer; both missing fails closed.
			// CSRF is required either way for mutating methods.
			if !sameOrigin(r) {
				http.Error(wr, "Forbidden: cross-origin request", http.StatusForbidden)
				return
			}
			if !w.validCSRF(r, sess) {
				http.Error(wr, "Forbidden: CSRF token invalid", http.StatusForbidden)
				return
			}
		}
		next(wr, r)
	}
}

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

func (w *Web) getPlatformEmoji() string {
	if os.Getenv("LAVHOST") != "" {
		return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/victory-hand_270c-fe0f.png"
	} else if os.Getenv("DOCKER") != "" {
		return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/spouting-whale_1f433.png"
	}
	return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/waning-crescent-moon_1f318.png"
}

func extractBlock(tpl, blockName string) string {
	startTag := fmt.Sprintf("{%% block %s %%}", blockName)
	endTag := "{% endblock %}"
	startIdx := strings.Index(tpl, startTag)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startTag)
	endIdx := strings.Index(tpl[startIdx:], endTag)
	if endIdx == -1 {
		return ""
	}
	return tpl[startIdx : startIdx+endIdx]
}

func replaceBlock(tpl, blockName, content string) string {
	target := fmt.Sprintf("{%% block %s %%}{%% endblock %%}", blockName)
	return strings.ReplaceAll(tpl, target, content)
}

func replaceConditional(tpl, condition string, keepTrue bool) string {
	startTag := fmt.Sprintf("{%% if %s %%}", condition)
	elseTag := "{% else %}"
	endTag := "{% endif %}"

	for {
		startIdx := strings.Index(tpl, startTag)
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(tpl[startIdx:], endTag)
		if endIdx == -1 {
			break
		}
		endIdx += startIdx

		inner := tpl[startIdx+len(startTag) : endIdx]

		elseIdx := strings.Index(inner, elseTag)
		var truePart, falsePart string
		if elseIdx != -1 {
			truePart = inner[:elseIdx]
			falsePart = inner[elseIdx+len(elseTag):]
		} else {
			truePart = inner
			falsePart = ""
		}

		replacement := falsePart
		if keepTrue {
			replacement = truePart
		}

		tpl = tpl[:startIdx] + replacement + tpl[endIdx+len(endTag):]
	}
	return tpl
}

func (w *Web) sendMessage(client TelegramClient, chatID int64, text string) (any, error) {
	return client.SendMessage(chatref.ID(chatID), text)
}

func webResourceDir(dataRoot string) string {
	var candidates []string
	if envDir := strings.TrimSpace(os.Getenv("GOROKU_WEB_RESOURCES")); envDir != "" {
		candidates = append(candidates, envDir)
	}
	if dataRoot != "" {
		candidates = append(candidates, filepath.Join(dataRoot, "web-resources"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "web-resources"))
	}
	if execPath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(execPath), "web-resources"))
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(candidate, "base.jinja2")); err == nil {
			return candidate
		}
	}
	if dataRoot != "" {
		return filepath.Join(dataRoot, "web-resources")
	}
	return "web-resources"
}

func (w *Web) RootHandler(wr http.ResponseWriter, r *http.Request) {
	w.rememberSetupToken(wr, r)
	resourceDir := webResourceDir(w.dataRoot)
	baseBytes, err := os.ReadFile(filepath.Join(resourceDir, "base.jinja2"))
	if err != nil {
		http.Error(wr, "base template not found", http.StatusInternalServerError)
		return
	}

	rootBytes, err := os.ReadFile(filepath.Join(resourceDir, "root.jinja2"))
	if err != nil {
		http.Error(wr, "root template not found", http.StatusInternalServerError)
		return
	}

	baseStr := string(baseBytes)
	rootStr := string(rootBytes)

	headBlock := extractBlock(rootStr, "head")
	contentBlock := extractBlock(rootStr, "content")
	afterBlock := extractBlock(rootStr, "after")

	htmlContent := baseStr
	htmlContent = replaceBlock(htmlContent, "head", headBlock)
	htmlContent = replaceBlock(htmlContent, "content", contentBlock)
	htmlContent = replaceBlock(htmlContent, "after", afterBlock)

	htmlContent = strings.ReplaceAll(htmlContent, `{{ static("base.css") }}`, "static/base.css")
	htmlContent = strings.ReplaceAll(htmlContent, `{{ static("root.js") }}`, "static/root.js")

	platformEmoji := w.getPlatformEmoji()
	htmlContent = strings.ReplaceAll(htmlContent, `{{ platform_emoji }}`, platformEmoji)

	w.mu.RLock()
	apiToken := w.apiToken
	w.mu.RUnlock()
	tgDone := w.clientCount() > 0
	skipCreds := hasAPIToken(apiToken)
	lavhost := os.Getenv("LAVHOST") != ""

	if skipCreds {
		htmlContent = strings.ReplaceAll(htmlContent, `{{ skip_creds }}`, "True")
	} else {
		htmlContent = strings.ReplaceAll(htmlContent, `{{ skip_creds }}`, "False")
	}

	if !tgDone {
		htmlContent = replaceConditional(htmlContent, "not tg_done", true)
	} else {
		htmlContent = replaceConditional(htmlContent, "not tg_done", false)
	}

	htmlContent = replaceConditional(htmlContent, "skip_creds and not lavhost", skipCreds && !lavhost)

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeString(wr, htmlContent)
}

func (w *Web) SetTGApiHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, ok := readLimitedBody(wr, r, shortBodyLimit)
	if !ok {
		return
	}
	if len(body) < 36 {
		http.Error(wr, "API ID and HASH pair has invalid length", http.StatusBadRequest)
		return
	}

	text := string(body)
	apiHash := text[:32]
	apiIDRaw := strings.TrimSpace(text[32:])

	if matched, _ := regexp.MatchString(`^[0-9a-fA-F]{32}$`, apiHash); !matched {
		http.Error(wr, "API HASH must be 32 hex characters", http.StatusBadRequest)
		return
	}
	apiID, err := strconv.ParseInt(apiIDRaw, 10, 64)
	if err != nil || apiID <= 0 {
		http.Error(wr, "API ID must be a positive integer", http.StatusBadRequest)
		return
	}

	if w.saveConfig != nil {
		w.saveConfig("api_id", apiID)
		w.saveConfig("api_hash", apiHash)
	}
	w.mu.Lock()
	w.apiToken = apiHash
	w.mu.Unlock()
	L().Info("Telegram API credentials saved", zap.Int64("api_id", apiID))

	writeString(wr, "ok")
}

func (w *Web) SendTGCodeHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !w.checkEndpointRateLimit("send_tg_code", clientIP(r), 3, 5*time.Minute) {
		http.Error(wr, "RATE_LIMIT", http.StatusTooManyRequests)
		return
	}

	body, ok := readLimitedBody(wr, r, shortBodyLimit)
	if !ok {
		return
	}
	phone := parsePhone(strings.TrimSpace(string(body)))
	if phone == "" {
		L().Info("send_tg_code rejected empty or invalid phone from {0}", zap.Any("arg0", r.RemoteAddr))
		http.Error(wr, "Invalid phone number", http.StatusBadRequest)
		return
	}
	L().Info("send_tg_code started for phone={0} from={1}", zap.Any("arg0", maskPhone(phone)), zap.Any("arg1", r.RemoteAddr))

	w.mu.Lock()
	if w.pendingClient != nil {
		if w.qrLogin != nil || w.qrTaskActive {
			if oldClient, ok := w.pendingClient.(TelegramClient); ok && oldClient != nil {
				_ = oldClient.Disconnect()
			}
			w.pendingClient = nil
			w.qrLogin = nil
			w.qrTaskActive = false
			w.twoFANeeded = false
			L().Info("pending QR login client cleared for phone auth")
		} else {
			w.mu.Unlock()
			L().Info("send_tg_code rejected: auth already pending")
			http.Error(wr, "Already pending", http.StatusConflict)
			return
		}
	}
	if w.getClient != nil {
		L().Info("creating pending Telegram client for phone auth")
		w.pendingClient = w.getClient()
	}
	client, ok := w.pendingClient.(TelegramClient)
	w.mu.Unlock()

	if ok && client != nil {
		if err := client.Connect(); err != nil {
			L().Info("Telegram client connect failed for phone auth: {0}", zap.Any("arg0", err))
			http.Error(wr, "connect failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		L().Info("Telegram client connected, sending login code to {0}", zap.Any("arg0", maskPhone(phone)))
		err := client.SendCodeRequest(phone)
		if err != nil {
			L().Info("send code failed for {0}: {1}", zap.Any("arg0", maskPhone(phone)), zap.Any("arg1", err))
			writeTelegramAuthError(wr, err)
			return
		}
		L().Info("login code sent to {0}", zap.Any("arg0", maskPhone(phone)))
	} else {
		L().Info("send_tg_code failed: pending client unavailable")
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	writeString(wr, "ok")
}

func (w *Web) CheckSessionHandler(wr http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeString(wr, "0")
		return
	}
	sess := w.sessionForToken(cookie.Value)
	if sess == nil {
		writeString(wr, "0")
		return
	}
	w.setSessionCookies(wr, r, sess.Token, sess.CSRFToken)
	wr.Header().Set("X-CSRF-Token", sess.CSRFToken)
	writeString(wr, "1")
}

func (w *Web) WebAuthHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if sess := w.sessionForToken(cookie.Value); sess != nil {
			// Re-auth with an existing browser session is state-changing: require
			// same-origin and CSRF like other mutating cookie-session routes.
			if !sameOrigin(r) {
				http.Error(wr, "Forbidden: cross-origin request", http.StatusForbidden)
				return
			}
			if !w.validCSRF(r, sess) {
				http.Error(wr, "Forbidden: CSRF token invalid", http.StatusForbidden)
				return
			}
			session, err := w.rotateSession(wr, r, sess.Token)
			if err != nil {
				L().Error("failed to rotate session", zap.Error(err))
				http.Error(wr, "SESSION_UNAVAILABLE", http.StatusInternalServerError)
				return
			}
			writeString(wr, session)
			return
		}
	}

	clients := w.ListClients()
	if len(clients) == 0 {
		session, err := w.exchangeSetupSession(wr, r)
		if err != nil {
			if errors.Is(err, errSetupTokenRequired) {
				http.Error(wr, "SETUP_TOKEN_REQUIRED", http.StatusUnauthorized)
				return
			}
			L().Error("failed to create setup session", zap.Error(err))
			http.Error(wr, "SESSION_UNAVAILABLE", http.StatusInternalServerError)
			return
		}
		writeString(wr, session)
		return
	}

	ip := clientIP(r)
	if !w.checkEndpointRateLimit("web_auth", ip, 3, 3*time.Minute) {
		http.Error(wr, "RATE_LIMIT", http.StatusTooManyRequests)
		return
	}

	var selected RuntimeClient
	var client TelegramClient
	var inlineBot *tgbotapi.BotAPI
	var inlineProvider inlineBotProvider
	for _, runtimeClient := range clients {
		c := runtimeClient.Client
		if c == nil || c.TGIDValue() == 0 {
			continue
		}
		client = c
		selected = runtimeClient
		inlineProvider = getInlineProvider(c)
		if inlineProvider != nil {
			inlineBot = inlineProvider.GetBotAPI()
		}
		break
	}
	if client == nil {
		http.Error(wr, "Telegram client not ready", http.StatusServiceUnavailable)
		return
	}

	token, err := randomToken(8)
	if err != nil {
		L().Error("failed to generate web auth token", zap.Error(err))
		http.Error(wr, "AUTH_UNAVAILABLE", http.StatusInternalServerError)
		return
	}
	approvedChan := make(chan struct{})
	auth := &PendingAuth{
		Token:      token,
		Approved:   approvedChan,
		Cancelled:  make(chan struct{}),
		ClientID:   selected.ID,
		generation: selected.generation,
		Expiry:     time.Now().Add(webAuthTTL),
	}

	w.mu.Lock()
	now := time.Now()
	for pendingToken, pending := range w.pendingAuths {
		if !now.Before(pending.Expiry) {
			delete(w.pendingAuths, pendingToken)
			if pending.Cancelled != nil {
				pending.cancelMu.Do(func() { close(pending.Cancelled) })
			}
		}
	}
	if !w.authAccepting {
		w.mu.Unlock()
		http.Error(wr, "AUTH_CANCELLED", http.StatusServiceUnavailable)
		return
	}
	registered, exists := w.clientData[selected.ID]
	if !exists || registered.generation != selected.generation {
		w.mu.Unlock()
		http.Error(wr, "AUTH_CANCELLED", http.StatusServiceUnavailable)
		return
	}
	if len(w.pendingAuths) >= maxPendingAuths {
		w.mu.Unlock()
		http.Error(wr, "TOO_MANY_PENDING_AUTHS", http.StatusServiceUnavailable)
		return
	}
	w.pendingAuths[token] = auth
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		if w.pendingAuths[token] == auth {
			delete(w.pendingAuths, token)
		}
		w.mu.Unlock()
	}()

	msg := fmt.Sprintf("🪐🔐 <b>Click button below to confirm web application ops</b>\n\n<b>Client IP</b>: <code>%s</code>\n\n<i>If you did not request any codes, simply ignore this message</i>", html.EscapeString(ip))
	if inlineBot != nil {
		markup := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔓 Authorize user", "authorize_web_"+token)))
		cfg := tgbotapi.NewMessage(client.TGIDValue(), msg)
		cfg.ParseMode = tgbotapi.ModeHTML
		cfg.LinkPreviewOptions = tgbotapi.LinkPreviewOptions{IsDisabled: true}
		cfg.ReplyMarkup = markup
		if _, err := inlineBot.Send(cfg); err != nil {
			L().Warn("failed to send web auth request", zap.Error(err))
			http.Error(wr, "AUTH_NOTIFICATION_FAILED", http.StatusBadGateway)
			return
		}
	} else {
		fallback := fmt.Sprintf("%s\n\nTo approve, send the following command:\n<code>.approve_web %s</code>", msg, token)
		if _, err := client.SendMessage(chatref.Username("me"), fallback); err != nil {
			L().Warn("failed to send web auth request", zap.Error(err))
			http.Error(wr, "AUTH_NOTIFICATION_FAILED", http.StatusBadGateway)
			return
		}
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(webAuthTTL)
	defer timeout.Stop()
	for {
		select {
		case <-approvedChan:
			session, ok, err := w.createAuthorizedSession(wr, r, token, auth)
			if err != nil {
				L().Error("failed to create authorized session", zap.Error(err))
				http.Error(wr, "SESSION_UNAVAILABLE", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.Error(wr, "AUTH_CANCELLED", http.StatusServiceUnavailable)
				return
			}
			writeString(wr, session)
			return
		case <-ticker.C:
			if inlineProvider != nil && inlineProvider.PopWebAuthToken(token) {
				session, ok, err := w.createAuthorizedSession(wr, r, token, auth)
				if err != nil {
					L().Error("failed to create authorized session", zap.Error(err))
					http.Error(wr, "SESSION_UNAVAILABLE", http.StatusInternalServerError)
					return
				}
				if !ok {
					http.Error(wr, "AUTH_CANCELLED", http.StatusServiceUnavailable)
					return
				}
				writeString(wr, session)
				return
			}
		case <-timeout.C:
			http.Error(wr, "TIMEOUT", http.StatusRequestTimeout)
			return
		case <-r.Context().Done():
			return
		case <-auth.Cancelled:
			http.Error(wr, "AUTH_CANCELLED", http.StatusServiceUnavailable)
			return
		}
	}
}

func (w *Web) createAuthorizedSession(wr http.ResponseWriter, r *http.Request, token string, auth *PendingAuth) (string, bool, error) {
	session, csrf, err := w.mintSessionTokens()
	if err != nil {
		return "", false, err
	}

	var oldSession string
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		oldSession = cookie.Value
	}

	w.mu.Lock()
	pending := w.pendingAuths[token]
	runtime, registered := w.clientData[auth.ClientID]
	if pending != auth || !registered || runtime.generation != auth.generation || !time.Now().Before(auth.Expiry) {
		w.mu.Unlock()
		return "", false, nil
	}
	if oldSession != "" {
		delete(w.sessions, oldSession)
	}
	w.sessions[session] = WebSession{Token: session, CSRFToken: csrf, Expiry: time.Now().Add(sessionTTL)}
	delete(w.pendingAuths, token)
	w.mu.Unlock()
	w.setSessionCookies(wr, r, session, csrf)
	return session, true, nil
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

func (w *Web) checkEndpointRateLimit(endpoint, ips string, maxAttempts int, window time.Duration) bool {
	now := time.Now().Unix()
	ip := normalizeClientIP(ips)
	if ip == "" {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	key := endpoint + ":" + ip
	var recent []int64
	for _, ts := range w.ratelimit[key] {
		if now-ts < int64(window.Seconds()) {
			recent = append(recent, ts)
		}
	}
	if len(recent) >= maxAttempts {
		w.ratelimit[key] = recent
		return false
	}
	recent = append(recent, now)
	w.ratelimit[key] = recent
	return true
}

func getClientTGID(client webiface.TelegramClient) int64 {
	return client.TGIDValue()
}

func getInlineProvider(client any) inlineBotProvider {
	c, ok := client.(interface {
		InlineProvider() webiface.InlineProvider
	})
	if !ok {
		return nil
	}
	provider := c.InlineProvider()
	if provider == nil {
		return nil
	}
	return provider
}

func (w *Web) ApproveWebAuth(token string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if auth, exists := w.pendingAuths[token]; exists {
		runtime, registered := w.clientData[auth.ClientID]
		if registered && runtime.generation == auth.generation && time.Now().Before(auth.Expiry) {
			auth.approveMu.Do(func() { close(auth.Approved) })
			return true
		}
		delete(w.pendingAuths, token)
	}
	return false
}

func (w *Web) TGCodeHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !w.checkEndpointRateLimit("tg_code", clientIP(r), 5, 5*time.Minute) {
		http.Error(wr, "RATE_LIMIT", http.StatusTooManyRequests)
		return
	}

	body, ok := readLimitedBody(wr, r, shortBodyLimit)
	if !ok {
		return
	}
	text := string(body)
	split := strings.Split(text, "\n")
	if len(split) < 2 {
		L().Info("tg_code rejected malformed payload from {0}", zap.Any("arg0", r.RemoteAddr))
		http.Error(wr, "Invalid code payload", http.StatusBadRequest)
		return
	}

	code := split[0]
	phone := parsePhone(split[1])
	password := ""
	if len(split) > 2 {
		password = split[2]
	}

	var isOnlyDigits = true
	for _, r := range code {
		if r < '0' || r > '9' {
			isOnlyDigits = false
			break
		}
	}

	if (len(code) != 5 && password == "") || !isOnlyDigits || phone == "" {
		http.Error(wr, "Invalid phone or code format", http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	client, ok := w.pendingClient.(TelegramClient)
	w.mu.Unlock()

	if ok && client != nil {
		L().Info("signing in with code for phone={0}, has_password={1}", zap.Any("arg0", maskPhone(phone)), zap.Any("arg1", password != ""))
		err := client.SignIn(phone, code, password)
		if err != nil {
			L().Info("sign in failed for {0}: {1}", zap.Any("arg0", maskPhone(phone)), zap.Any("arg1", err))
			writeTelegramAuthError(wr, err)
			return
		}
		L().Info("sign in succeeded for {0}", zap.Any("arg0", maskPhone(phone)))
		if err := w.finishPendingLogin(client); err != nil {
			L().Info("finish after tg_code failed: {0}", zap.Any("arg0", err))
			http.Error(wr, err.Error(), http.StatusInternalServerError)
			return
		}
		w.clearSetupCookie(wr, r)
	} else {
		L().Info("tg_code failed: pending client unavailable")
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	writeString(wr, "SUCCESS")
}

func (w *Web) FinishLoginHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.mu.Lock()
	client := w.pendingClient
	w.mu.Unlock()

	if client == nil {
		http.Error(wr, "Telegram client not available", http.StatusBadRequest)
		return
	}

	if err := w.finishPendingLogin(client); err != nil {
		L().Info("finish_login failed: {0}", zap.Any("arg0", err))
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	w.clearSetupCookie(wr, r)
	L().Info("finish_login completed")
	writeString(wr, "ok")
}

func (w *Web) finishPendingLogin(client TelegramClient) error {
	if w.onLogin != nil {
		L().Info("finish_login started, registering pending Telegram client")
		if err := w.onLogin(client); err != nil {
			return err
		}
		w.mu.Lock()
		w.pendingClient = nil
		w.qrLogin = nil
		w.qrTaskActive = false
		w.twoFANeeded = false
		w.mu.Unlock()
		// Successful initial login force-consumes the one-time setup token.
		w.consumeSetupToken()
		return nil
	}

	if w.restart != nil {
		go func() {
			time.Sleep(1 * time.Second)
			w.restart()
		}()
	}
	w.consumeSetupToken()
	return nil
}

func (w *Web) CustomBotHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, ok := readLimitedBody(wr, r, shortBodyLimit)
	if !ok {
		return
	}
	username := strings.TrimSpace(string(body))
	username = strings.TrimPrefix(username, "@")
	if username != "" && (!strings.HasSuffix(strings.ToLower(username), "bot") || len(username) < 5) {
		http.Error(wr, "Bot username invalid", http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	client, ok := w.pendingClient.(TelegramClient)
	w.mu.Unlock()
	if !ok || client == nil {
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	if username != "" {
		exists, err := client.ResolveUsername(username)
		if err == nil && exists {
			owned, err := client.CheckBot(username)
			if err != nil || !owned {
				writeString(wr, "OCCUPIED")
				return
			}
		}
	}

	if w.saveConfig != nil {
		w.saveConfig("custom_bot", username)
	}
	L().Info("custom inline bot saved: {0}", zap.Any("arg0", username))
	writeString(wr, "OK")
}

func (w *Web) InitQRLoginHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	url, err := w.initQRLogin(r)
	if err != nil {
		L().Info("QR login init failed: {0}", zap.Any("arg0", err))
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	writeString(wr, url)
}

func (w *Web) initQRLogin(r *http.Request) (string, error) {
	w.mu.Lock()
	if w.qrTaskActive {
		if qrStr, ok := w.qrLogin.(string); ok && qrStr != "" {
			w.mu.Unlock()
			return qrStr, nil
		}
		w.mu.Unlock()
		return "", fmt.Errorf("QR login is already initializing")
	}
	if w.pendingClient != nil {
		if oldClient, ok := w.pendingClient.(TelegramClient); ok && oldClient != nil {
			_ = oldClient.Disconnect()
		}
		w.pendingClient = nil
		w.qrLogin = nil
		w.twoFANeeded = false
		L().Info("previous pending auth client cleared for new QR login")
	}
	w.qrTaskActive = true
	if w.pendingClient == nil && w.getClient != nil {
		L().Info("creating pending Telegram client for QR login")
		w.pendingClient = w.getClient()
	}
	client, ok := w.pendingClient.(TelegramClient)
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		if _, ok := w.qrLogin.(string); !ok {
			w.qrTaskActive = false
		}
		w.mu.Unlock()
	}()

	if ok && client != nil {
		L().Info("QR login connect started from={0}", zap.Any("arg0", r.RemoteAddr))
		if err := client.Connect(); err != nil {
			return "", fmt.Errorf("connect failed: %v", err)
		}
		L().Info("QR login export token started")
		url, err := client.QRLogin()
		if err != nil {
			return "", err
		}
		w.mu.Lock()
		w.qrLogin = url
		w.mu.Unlock()
		L().Info("QR login URL generated, len={0}", zap.Any("arg0", len(url)))
		go w.pollQRLogin(client)
		return url, nil
	}
	return "", fmt.Errorf("Telegram client not available")
}

func (w *Web) pollQRLogin(client TelegramClient) {
	L().Info("waiting for QR login completion")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			if w.pendingClient == client {
				w.qrTaskActive = false
			}
			w.mu.Unlock()
			L().Info("QR login poll timeout")
			return
		case <-ticker.C:
			w.mu.Lock()
			if w.pendingClient != client {
				w.mu.Unlock()
				L().Info("stopping QR login poll because pending client changed")
				return
			}
			w.mu.Unlock()

			status, err := client.QRLoginStatus()
			if err != nil {
				if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
					w.mu.Lock()
					w.twoFANeeded = true
					w.qrLogin = true
					w.qrTaskActive = false
					w.mu.Unlock()
					L().Info("QR login completed, 2FA required")
					return
				}
				L().Info("QR login poll error", zap.Error(err))
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "canceled") || strings.Contains(errStr, "closed") || strings.Contains(errStr, "dead") {
					L().Info("stopping QR login poll because client connection is inactive")
					return
				}
			} else if status == "SUCCESS" {
				if err := w.finishPendingLogin(client); err != nil {
					L().Info("QR finish_login failed", zap.Error(err))
					return
				}
				w.mu.Lock()
				w.twoFANeeded = false
				w.qrLogin = true
				w.qrTaskActive = false
				w.mu.Unlock()
				L().Info("QR login completed successfully")
				return
			}
		}
	}
}

func (w *Web) GetQRURLHandler(wr http.ResponseWriter, r *http.Request) {
	w.mu.RLock()
	qr := w.qrLogin
	twoFANeeded := w.twoFANeeded
	w.mu.RUnlock()

	if qrStr, ok := qr.(string); ok && qrStr != "" {
		wr.WriteHeader(http.StatusCreated) // 201 Created
		writeString(wr, qrStr)
		return
	}
	if qrDone, ok := qr.(bool); ok && qrDone {
		if twoFANeeded {
			wr.WriteHeader(http.StatusForbidden)
			writeString(wr, "2FA")
			return
		}
		writeString(wr, "SUCCESS")
		return
	}

	// Initializing QR login mutates state; require POST (GET is read-only).
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	L().Info("get_qr_url called before QR exists, initializing")
	url, err := w.initQRLogin(r)
	if err != nil {
		L().Info("get_qr_url init failed: {0}", zap.Any("arg0", err))
		http.Error(wr, "Internal Server Error: Unable to initialize QR login: "+err.Error(), http.StatusInternalServerError)
		return
	}
	wr.WriteHeader(http.StatusCreated)
	writeString(wr, url)
}

func (w *Web) QR2FAHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !w.checkEndpointRateLimit("qr_2fa", clientIP(r), 5, 5*time.Minute) {
		http.Error(wr, "RATE_LIMIT", http.StatusTooManyRequests)
		return
	}
	body, ok := readLimitedBody(wr, r, shortBodyLimit)
	if !ok {
		return
	}
	password := strings.TrimSpace(string(body))
	if password == "" {
		http.Error(wr, "Invalid 2FA password", http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	client, ok := w.pendingClient.(TelegramClient)
	w.mu.Unlock()
	if !ok || client == nil {
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	L().Info("QR 2FA password received, checking")
	if err := client.SignIn("", "", password); err != nil {
		L().Info("QR 2FA failed: {0}", zap.Any("arg0", err))
		http.Error(wr, err.Error(), http.StatusForbidden)
		return
	}
	L().Info("QR 2FA accepted")
	if err := w.finishPendingLogin(client); err != nil {
		L().Info("QR 2FA finish_login failed: {0}", zap.Any("arg0", err))
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	w.clearSetupCookie(wr, r)
	writeString(wr, "SUCCESS")
}

func (w *Web) LogoutHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		w.mu.Lock()
		delete(w.sessions, cookie.Value)
		w.mu.Unlock()
	}
	w.clearSessionCookies(wr, r)
	writeString(wr, "OK")
}

func (w *Web) CanAddHandler(wr http.ResponseWriter, r *http.Request) {
	writeString(wr, "Yes")
}

func (w *Web) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", w.RootHandler)
	mux.HandleFunc("/set_api", w.checkSessionMiddleware(w.SetTGApiHandler))
	mux.HandleFunc("/send_tg_code", w.checkSessionMiddleware(w.SendTGCodeHandler))
	mux.HandleFunc("/check_session", w.CheckSessionHandler)
	mux.HandleFunc("/web_auth", w.WebAuthHandler)
	mux.HandleFunc("/tg_code", w.checkSessionMiddleware(w.TGCodeHandler))
	mux.HandleFunc("/finish_login", w.checkSessionMiddleware(w.FinishLoginHandler))
	mux.HandleFunc("/custom_bot", w.checkSessionMiddleware(w.CustomBotHandler))
	mux.HandleFunc("/init_qr_login", w.checkSessionMiddleware(w.InitQRLoginHandler))
	mux.HandleFunc("/get_qr_url", w.checkSessionMiddleware(w.GetQRURLHandler))
	mux.HandleFunc("/qr_2fa", w.checkSessionMiddleware(w.QR2FAHandler))
	mux.HandleFunc("/logout", w.checkSessionMiddleware(w.LogoutHandler))
	mux.HandleFunc("/can_add", w.CanAddHandler)
}

func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(phone)-4) + phone[len(phone)-4:]
}

func hasAPIToken(token any) bool {
	if token == nil {
		return false
	}
	if s, ok := token.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return true
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
