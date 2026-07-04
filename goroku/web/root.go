package web

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	Token     string
	Approved  chan struct{}
	approveMu sync.Once
	Expiry    time.Time
}

type WebSession struct {
	Token  string
	Expiry time.Time
}

const (
	sessionCookieName = "session"
	sessionTTL        = 6 * time.Hour
)

type inlineBotProvider interface {
	GetBotAPI() *tgbotapi.BotAPI
	PopWebAuthToken(token string) bool
}

type Web struct {
	mu             sync.Mutex
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
	clientData     map[int64][]any
	apiSetChan     chan struct{}
	clientsSetChan chan struct{}
	getClient      func() webiface.TelegramClient
	pendingAuths   map[string]*PendingAuth
	pendingAuthsMu sync.Mutex
}

func NewWeb(cfg WebConfig) *Web {
	return &Web{
		signInClients:  make(map[string]webiface.TelegramClient),
		sessions:       make(map[string]WebSession),
		ratelimit:      make(map[string][]int64),
		apiToken:       cfg.ApiToken,
		setupToken:     strings.TrimSpace(cfg.SetupToken),
		dataRoot:       cfg.DataRoot,
		connection:     cfg.Connection,
		proxy:          cfg.Proxy,
		saveConfig:     cfg.SaveConfig,
		restart:        cfg.Restart,
		onLogin:        cfg.OnLogin,
		clientData:     make(map[int64][]any),
		apiSetChan:     make(chan struct{}),
		clientsSetChan: make(chan struct{}),
		getClient:      cfg.GetClient,
		pendingAuths:   make(map[string]*PendingAuth),
	}
}

func (w *Web) checkSession(r *http.Request) bool {
	w.mu.Lock()
	clientsCount := len(w.clientData)
	w.mu.Unlock()
	if clientsCount == 0 && w.checkSetupToken(r) {
		return true
	}

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
	return &sess
}

func (w *Web) createSession(wr http.ResponseWriter, r *http.Request) string {
	session := "goroku_session_" + randomToken(32)
	w.mu.Lock()
	w.sessions[session] = WebSession{Token: session, Expiry: time.Now().Add(sessionTTL)}
	w.mu.Unlock()
	w.setSessionCookies(wr, r, session)
	return session
}

func (w *Web) setSessionCookies(wr http.ResponseWriter, r *http.Request, session string) {
	secure := isHTTPS(r)
	http.SetCookie(wr, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (w *Web) clearSessionCookies(wr http.ResponseWriter, r *http.Request) {
	secure := isHTTPS(r)
	for _, name := range []string{sessionCookieName, "setup_token"} {
		http.SetCookie(wr, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (w *Web) checkSetupToken(r *http.Request) bool {
	if w.setupToken == "" {
		return false
	}
	candidates := []string{
		r.Header.Get("X-Goroku-Setup-Token"),
	}
	if cookie, err := r.Cookie("setup_token"); err == nil {
		candidates = append(candidates, cookie.Value)
	}
	for _, token := range candidates {
		if strings.TrimSpace(token) == w.setupToken {
			return true
		}
	}
	return false
}

func (w *Web) rememberSetupToken(wr http.ResponseWriter, r *http.Request) {
	if w.setupToken == "" || !w.checkSetupToken(r) {
		return
	}
	http.SetCookie(wr, &http.Cookie{
		Name:     "setup_token",
		Value:    w.setupToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(time.Hour),
		MaxAge:   3600,
	})
}

func (w *Web) checkSessionMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(wr http.ResponseWriter, r *http.Request) {
		if !w.checkSession(r) {
			http.Error(wr, "Unauthorized: Please log in using the Telegram Web Auth button first.", http.StatusUnauthorized)
			return
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

func (w *Web) RootHandler(wr http.ResponseWriter, r *http.Request) {
	w.rememberSetupToken(wr, r)
	baseBytes, err := os.ReadFile("web-resources/base.jinja2")
	if err != nil {
		baseBytes, err = os.ReadFile(filepath.Join(w.dataRoot, "web-resources/base.jinja2"))
	}
	if err != nil {
		http.Error(wr, "base template not found", http.StatusInternalServerError)
		return
	}

	rootBytes, err := os.ReadFile("web-resources/root.jinja2")
	if err != nil {
		rootBytes, err = os.ReadFile(filepath.Join(w.dataRoot, "web-resources/root.jinja2"))
	}
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

	skipCreds := hasAPIToken(w.apiToken)
	tgDone := len(w.clientData) > 0
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

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) < 36 {
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

	body, _ := io.ReadAll(r.Body)
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
	if w.checkSession(r) {
		writeString(wr, "1")
	} else {
		writeString(wr, "0")
	}
}

func (w *Web) WebAuthHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.checkSession(r) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if sess := w.sessionForToken(cookie.Value); sess != nil {
				w.setSessionCookies(wr, r, sess.Token)
				writeString(wr, cookie.Value)
				return
			}
		}
	}

	w.mu.Lock()
	clientsCount := len(w.clientData)
	w.mu.Unlock()

	if clientsCount == 0 {
		if !w.checkSetupToken(r) {
			http.Error(wr, "SETUP_TOKEN_REQUIRED", http.StatusUnauthorized)
			return
		}
		session := w.createSession(wr, r)
		writeString(wr, session)
		return
	}

	ips := r.Header.Get("X-FORWARDED-FOR")
	if ips == "" {
		ips = r.Header.Get("CF-Connecting-IP")
	}
	if ips == "" {
		ips = r.RemoteAddr
	}
	if !w.checkEndpointRateLimit("web_auth", ips, 3, 3*time.Minute) {
		http.Error(wr, "RATE_LIMIT", http.StatusTooManyRequests)
		return
	}

	token := randomToken(8)
	approvedChan := make(chan struct{})
	auth := &PendingAuth{
		Token:    token,
		Approved: approvedChan,
		Expiry:   time.Now().Add(60 * time.Second),
	}

	w.pendingAuthsMu.Lock()
	w.pendingAuths[token] = auth
	w.pendingAuthsMu.Unlock()

	w.mu.Lock()
	var client TelegramClient
	var inlineBot *tgbotapi.BotAPI
	var inlineProvider inlineBotProvider
	for _, data := range w.clientData {
		if len(data) > 1 {
			if c, ok := data[1].(TelegramClient); ok {
				client = c
				inlineProvider = getInlineProvider(c)
				if inlineProvider != nil {
					inlineBot = inlineProvider.GetBotAPI()
				}
				break
			}
		}
	}
	w.mu.Unlock()

	if client != nil {
		msg := fmt.Sprintf("🪐🔐 <b>Click button below to confirm web application ops</b>\n\n<b>Client IP</b>: <code>%s</code>\n\n<i>If you did not request any codes, simply ignore this message</i>", ips)
		if inlineBot != nil {
			markup := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔓 Authorize user", "authorize_web_"+token)))
			cfg := tgbotapi.NewMessage(0, msg)
			cfg.ChatID = getClientTGID(client)
			cfg.ParseMode = tgbotapi.ModeHTML
			cfg.LinkPreviewOptions = tgbotapi.LinkPreviewOptions{IsDisabled: true}
			cfg.ReplyMarkup = markup
			_, _ = inlineBot.Send(cfg)
		} else {
			fallback := fmt.Sprintf("%s\n\nTo approve, send the following command:\n<code>.approve_web %s</code>", msg, token)
			_, _ = client.SendMessage(chatref.Username("me"), fallback)
		}
	} else {
		http.Error(wr, "Telegram client not ready", http.StatusInternalServerError)
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)
	for {
		select {
		case <-approvedChan:
			w.pendingAuthsMu.Lock()
			delete(w.pendingAuths, token)
			w.pendingAuthsMu.Unlock()

			session := w.createSession(wr, r)

			writeString(wr, session)
			return
		case <-ticker.C:
			if inlineProvider != nil && inlineProvider.PopWebAuthToken(token) {
				w.pendingAuthsMu.Lock()
				delete(w.pendingAuths, token)
				w.pendingAuthsMu.Unlock()

				session := w.createSession(wr, r)

				writeString(wr, session)
				return
			}
		case <-timeout:
			w.pendingAuthsMu.Lock()
			delete(w.pendingAuths, token)
			w.pendingAuthsMu.Unlock()

			http.Error(wr, "TIMEOUT", http.StatusRequestTimeout)
			return
		}
	}
}

func randomToken(size int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			b[i] = alphabet[0]
			continue
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

func clientIP(r *http.Request) string {
	// Cloudflare-provided client IP is the most trustworthy when present.
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	// Otherwise use the left-most entry of X-Forwarded-For. This assumes
	// the server is behind a trusted reverse proxy; without such a proxy
	// the header is trivially spoofable.
	if xfwd := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xfwd != "" {
		parts := strings.Split(xfwd, ",")
		for _, p := range parts {
			ip := strings.TrimSpace(p)
			if ip != "" {
				return ip
			}
		}
	}
	return r.RemoteAddr
}

func normalizeClientIP(ip string) string {
	// Strip port if present.
	host, _, err := net.SplitHostPort(ip)
	if err == nil {
		ip = host
	}
	return ip
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

func getClientTGID(client any) int64 {
	v := reflect.ValueOf(client)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName("TGID")
	if f.IsValid() && f.Kind() == reflect.Int64 {
		return f.Int()
	}
	return 0
}

func getInlineProvider(client any) inlineBotProvider {
	v := reflect.ValueOf(client)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("GorokuInline")
	if !f.IsValid() || f.IsNil() {
		return nil
	}
	provider, ok := f.Interface().(inlineBotProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider
}

func (w *Web) ApproveWebAuth(token string) bool {
	w.pendingAuthsMu.Lock()
	defer w.pendingAuthsMu.Unlock()
	if auth, exists := w.pendingAuths[token]; exists {
		if time.Now().Before(auth.Expiry) {
			auth.approveMu.Do(func() { close(auth.Approved) })
			return true
		}
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

	body, _ := io.ReadAll(r.Body)
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
	} else {
		L().Info("tg_code failed: pending client unavailable")
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	writeString(wr, "SUCCESS")
}

func (w *Web) FinishLoginHandler(wr http.ResponseWriter, r *http.Request) {
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
	L().Info("finish_login completed")
	writeString(wr, "ok")
	return

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
		return nil
	}

	if w.restart != nil {
		go func() {
			time.Sleep(1 * time.Second)
			w.restart()
		}()
	}
	return nil
}

func (w *Web) CustomBotHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
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
	w.mu.Lock()
	qr := w.qrLogin
	w.mu.Unlock()

	if qrStr, ok := qr.(string); ok && qrStr != "" {
		wr.WriteHeader(http.StatusCreated) // 201 Created
		writeString(wr, qrStr)
		return
	}
	if qrDone, ok := qr.(bool); ok && qrDone {
		if w.twoFANeeded {
			wr.WriteHeader(http.StatusForbidden)
			writeString(wr, "2FA")
			return
		}
		writeString(wr, "SUCCESS")
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
	body, _ := io.ReadAll(r.Body)
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
