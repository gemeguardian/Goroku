package web

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tgerr"
)

type TelegramClient interface {
	Connect() error
	Disconnect() error
	SendCodeRequest(phone string) error
	SignIn(phone, code, password string) error
	QRLogin() (string, error)
	QRLoginStatus() (string, error)
	SendMessage(chat interface{}, message string) (interface{}, error)
	ResolveUsername(username string) (bool, error)
	CheckBot(username string) (bool, error)
}

type WebConfig struct {
	ApiToken   interface{}
	SetupToken string
	DataRoot   string
	Connection interface{}
	Proxy      interface{}
	SaveConfig func(key string, value interface{}) bool
	Restart    func()
	GetClient  func() interface{}
	OnLogin    func(client interface{}) error
}

type PendingAuth struct {
	Token    string
	Approved chan struct{}
	Expiry   time.Time
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
	signInClients  map[string]interface{}
	pendingClient  interface{}
	qrLogin        interface{}
	qrTaskActive   bool
	twoFANeeded    bool
	sessions       map[string]WebSession
	ratelimit      map[string][]int64
	apiToken       interface{}
	setupToken     string
	dataRoot       string
	connection     interface{}
	proxy          interface{}
	saveConfig     func(key string, value interface{}) bool
	restart        func()
	onLogin        func(client interface{}) error
	clientData     map[int64]interface{}
	apiSetChan     chan struct{}
	clientsSetChan chan struct{}
	getClient      func() interface{}
	pendingAuths   map[string]*PendingAuth
	pendingAuthsMu sync.Mutex
}

func NewWeb(cfg WebConfig) *Web {
	return &Web{
		signInClients:  make(map[string]interface{}),
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
		clientData:     make(map[int64]interface{}),
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
		r.URL.Query().Get("token"),
		r.URL.Query().Get("setup_token"),
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
	wr.Write([]byte(htmlContent))
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
	apiID := text[32:]

	if w.saveConfig != nil {
		w.saveConfig("api_id", apiID)
		w.saveConfig("api_hash", apiHash)
	}
	w.mu.Lock()
	w.apiToken = apiHash
	w.mu.Unlock()
	log.Printf("web: Telegram API credentials saved, api_id=%s\n", apiID)

	wr.Write([]byte("ok"))
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
		log.Printf("web: send_tg_code rejected empty or invalid phone from %s\n", r.RemoteAddr)
		http.Error(wr, "Invalid phone number", http.StatusBadRequest)
		return
	}
	log.Printf("web: send_tg_code started for phone=%s from=%s\n", maskPhone(phone), r.RemoteAddr)

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
			log.Printf("web: pending QR login client cleared for phone auth\n")
		} else {
			w.mu.Unlock()
			log.Printf("web: send_tg_code rejected: auth already pending\n")
			http.Error(wr, "Already pending", http.StatusConflict)
			return
		}
	}
	if w.getClient != nil {
		log.Printf("web: creating pending Telegram client for phone auth\n")
		w.pendingClient = w.getClient()
	}
	client, ok := w.pendingClient.(TelegramClient)
	w.mu.Unlock()

	if ok && client != nil {
		if err := client.Connect(); err != nil {
			log.Printf("web: Telegram client connect failed for phone auth: %v\n", err)
			http.Error(wr, "connect failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("web: Telegram client connected, sending login code to %s\n", maskPhone(phone))
		err := client.SendCodeRequest(phone)
		if err != nil {
			log.Printf("web: send code failed for %s: %v\n", maskPhone(phone), err)
			writeTelegramAuthError(wr, err)
			return
		}
		log.Printf("web: login code sent to %s\n", maskPhone(phone))
	} else {
		log.Printf("web: send_tg_code failed: pending client unavailable\n")
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	wr.Write([]byte("ok"))
}

func (w *Web) CheckSessionHandler(wr http.ResponseWriter, r *http.Request) {
	if w.checkSession(r) {
		wr.Write([]byte("1"))
	} else {
		wr.Write([]byte("0"))
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
				wr.Write([]byte(cookie.Value))
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
		wr.Write([]byte(session))
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
		if slice, ok := data.([]interface{}); ok && len(slice) > 1 {
			if c, ok := slice[1].(TelegramClient); ok {
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
			_, _ = client.SendMessage("me", fallback)
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

			wr.Write([]byte(session))
			return
		case <-ticker.C:
			if inlineProvider != nil && inlineProvider.PopWebAuthToken(token) {
				w.pendingAuthsMu.Lock()
				delete(w.pendingAuths, token)
				w.pendingAuthsMu.Unlock()

				session := w.createSession(wr, r)

				wr.Write([]byte(session))
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
	ips := r.Header.Get("X-FORWARDED-FOR")
	if ips == "" {
		ips = r.Header.Get("CF-Connecting-IP")
	}
	if ips == "" {
		ips = r.RemoteAddr
	}
	return ips
}

func (w *Web) checkEndpointRateLimit(endpoint, ips string, maxAttempts int, window time.Duration) bool {
	now := time.Now().Unix()
	ipRe := regexp.MustCompile(`[0-9]{1,3}(?:\.[0-9]{1,3}){3}`)
	found := ipRe.FindAllString(ips, -1)
	if len(found) == 0 {
		found = []string{ips}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ip := range found {
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
	}
	return true
}

func getClientTGID(client interface{}) int64 {
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

func getInlineProvider(client interface{}) inlineBotProvider {
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
			select {
			case <-auth.Approved:
			default:
				close(auth.Approved)
			}
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
		log.Printf("web: tg_code rejected malformed payload from %s\n", r.RemoteAddr)
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
		log.Printf("web: signing in with code for phone=%s, has_password=%t\n", maskPhone(phone), password != "")
		err := client.SignIn(phone, code, password)
		if err != nil {
			log.Printf("web: sign in failed for %s: %v\n", maskPhone(phone), err)
			writeTelegramAuthError(wr, err)
			return
		}
		log.Printf("web: sign in succeeded for %s\n", maskPhone(phone))
		if err := w.finishPendingLogin(client); err != nil {
			log.Printf("web: finish after tg_code failed: %v\n", err)
			http.Error(wr, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		log.Printf("web: tg_code failed: pending client unavailable\n")
		http.Error(wr, "Telegram client not available", http.StatusInternalServerError)
		return
	}

	wr.Write([]byte("SUCCESS"))
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
		log.Printf("web: finish_login failed: %v\n", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("web: finish_login completed\n")
	wr.Write([]byte("ok"))
	return

}

func (w *Web) finishPendingLogin(client interface{}) error {
	if w.onLogin != nil {
		log.Printf("web: finish_login started, registering pending Telegram client\n")
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
				wr.Write([]byte("OCCUPIED"))
				return
			}
		}
	}

	if w.saveConfig != nil {
		w.saveConfig("custom_bot", username)
	}
	log.Printf("web: custom inline bot saved: %s\n", username)
	wr.Write([]byte("OK"))
}

func (w *Web) InitQRLoginHandler(wr http.ResponseWriter, r *http.Request) {
	url, err := w.initQRLogin(r)
	if err != nil {
		log.Printf("web: QR login init failed: %v\n", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	wr.Write([]byte(url))
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
		log.Printf("web: previous pending auth client cleared for new QR login\n")
	}
	w.qrTaskActive = true
	if w.pendingClient == nil && w.getClient != nil {
		log.Printf("web: creating pending Telegram client for QR login\n")
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
		log.Printf("web: QR login connect started from=%s\n", r.RemoteAddr)
		if err := client.Connect(); err != nil {
			return "", fmt.Errorf("connect failed: %v", err)
		}
		log.Printf("web: QR login export token started\n")
		url, err := client.QRLogin()
		if err != nil {
			return "", err
		}
		w.mu.Lock()
		w.qrLogin = url
		w.mu.Unlock()
		log.Printf("web: QR login URL generated, len=%d\n", len(url))
		go w.pollQRLogin(client)
		return url, nil
	}
	return "", fmt.Errorf("Telegram client not available")
}

func (w *Web) pollQRLogin(client TelegramClient) {
	log.Printf("web: waiting for QR login completion\n")
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		if w.pendingClient != client {
			w.mu.Unlock()
			log.Printf("web: stopping QR login poll because pending client changed\n")
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
				log.Printf("web: QR login completed, 2FA required\n")
				return
			}
			log.Printf("web: QR login poll error: %v\n", err)
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "canceled") || strings.Contains(errStr, "closed") || strings.Contains(errStr, "dead") {
				log.Printf("web: stopping QR login poll because client connection is inactive\n")
				return
			}
		} else if status == "SUCCESS" {
			if err := w.finishPendingLogin(client); err != nil {
				log.Printf("web: QR finish_login failed: %v\n", err)
				return
			}
			w.mu.Lock()
			w.twoFANeeded = false
			w.qrLogin = true
			w.qrTaskActive = false
			w.mu.Unlock()
			log.Printf("web: QR login completed successfully\n")
			return
		}
		time.Sleep(2 * time.Second)
	}

	w.mu.Lock()
	if w.pendingClient == client {
		w.qrTaskActive = false
	}
	w.mu.Unlock()
	log.Printf("web: QR login poll timeout\n")
}

func (w *Web) GetQRURLHandler(wr http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	qr := w.qrLogin
	w.mu.Unlock()

	if qrStr, ok := qr.(string); ok && qrStr != "" {
		wr.WriteHeader(http.StatusCreated) // 201 Created
		wr.Write([]byte(qrStr))
		return
	}
	if qrDone, ok := qr.(bool); ok && qrDone {
		if w.twoFANeeded {
			wr.WriteHeader(http.StatusForbidden)
			wr.Write([]byte("2FA"))
			return
		}
		wr.Write([]byte("SUCCESS"))
		return
	}

	log.Printf("web: get_qr_url called before QR exists, initializing\n")
	url, err := w.initQRLogin(r)
	if err != nil {
		log.Printf("web: get_qr_url init failed: %v\n", err)
		http.Error(wr, "Internal Server Error: Unable to initialize QR login: "+err.Error(), http.StatusInternalServerError)
		return
	}
	wr.WriteHeader(http.StatusCreated)
	wr.Write([]byte(url))
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

	log.Printf("web: QR 2FA password received, checking\n")
	if err := client.SignIn("", "", password); err != nil {
		log.Printf("web: QR 2FA failed: %v\n", err)
		http.Error(wr, err.Error(), http.StatusForbidden)
		return
	}
	log.Printf("web: QR 2FA accepted\n")
	if err := w.finishPendingLogin(client); err != nil {
		log.Printf("web: QR 2FA finish_login failed: %v\n", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	wr.Write([]byte("SUCCESS"))
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
	wr.Write([]byte("OK"))
}

func (w *Web) CanAddHandler(wr http.ResponseWriter, r *http.Request) {
	wr.Write([]byte("Yes"))
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

func hasAPIToken(token interface{}) bool {
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
