package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"goroku/goroku/chatref"
	"goroku/goroku/webiface"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"go.uber.org/zap"
)

func (w *Web) cancelPendingAuthsLocked(clientID int64, all bool) {
	for token, auth := range w.pendingAuths {
		if !all && auth.ClientID != clientID {
			continue
		}
		delete(w.pendingAuths, token)
		decrementPendingAuthIP(w.pendingAuthsByIP, auth.IP)
		if auth.Cancelled != nil {
			auth.cancelMu.Do(func() { close(auth.Cancelled) })
		}
	}
}

func decrementPendingAuthIP(m map[string]int, ip string) {
	if ip == "" {
		return
	}
	count, ok := m[ip]
	if !ok {
		return
	}
	count--
	if count <= 0 {
		delete(m, ip)
	} else {
		m[ip] = count
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
		L().Info("send_tg_code rejected: empty or invalid phone", zap.Any("remote_addr", r.RemoteAddr))
		http.Error(wr, "Invalid phone number", http.StatusBadRequest)
		return
	}
	L().Info("send_tg_code started", zap.Any("phone", maskPhone(phone)), zap.Any("remote_addr", r.RemoteAddr))

	w.mu.Lock()
	if w.pendingClient != nil {
		if w.qrLogin.URL != "" || w.qrLogin.Done || w.qrTaskActive {
			if oldClient, ok := w.pendingClient.(TelegramClient); ok && oldClient != nil {
				_ = oldClient.Disconnect()
			}
			w.pendingClient = nil
			w.qrLogin = qrLoginState{}
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
			L().Warn("Telegram client connect failed for phone auth", zap.Error(err))
			http.Error(wr, "connect failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		L().Info("Telegram client connected; sending login code", zap.Any("phone", maskPhone(phone)))
		err := client.SendCodeRequest(phone)
		if err != nil {
			L().Warn("send code failed", zap.Any("phone", maskPhone(phone)), zap.Error(err))
			writeTelegramAuthError(wr, err)
			return
		}
		L().Info("login code sent", zap.Any("phone", maskPhone(phone)))
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
		IP:         ip,
	}

	w.mu.Lock()
	now := time.Now()
	for pendingToken, pending := range w.pendingAuths {
		if !now.Before(pending.Expiry) {
			delete(w.pendingAuths, pendingToken)
			decrementPendingAuthIP(w.pendingAuthsByIP, pending.IP)
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
	if w.pendingAuthsByIP[ip] >= maxPendingAuthsPerIP {
		w.mu.Unlock()
		http.Error(wr, "TOO_MANY_PENDING_AUTHS", http.StatusServiceUnavailable)
		return
	}
	w.pendingAuths[token] = auth
	w.pendingAuthsByIP[ip]++
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		if w.pendingAuths[token] == auth {
			delete(w.pendingAuths, token)
			decrementPendingAuthIP(w.pendingAuthsByIP, auth.IP)
		}
		w.mu.Unlock()
	}()

	msg := fmt.Sprintf("🪐🔐 <b>Click button below to confirm web application ops</b>\n\n<b>Client IP</b>: <code>%s</code>\n\n<i>If you did not request any codes, simply ignore this message</i>", html.EscapeString(clampIPForDiagnostic(ip)))
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
	w.sweepSessionsLocked()
	w.sessions[session] = WebSession{Token: session, CSRFToken: csrf, Expiry: time.Now().Add(sessionTTL)}
	delete(w.pendingAuths, token)
	decrementPendingAuthIP(w.pendingAuthsByIP, auth.IP)
	w.mu.Unlock()
	w.setSessionCookies(wr, r, session, csrf)
	return session, true, nil
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

func getInlineProvider(client webiface.TelegramClient) inlineBotProvider {
	if client == nil {
		return nil
	}
	provider := client.InlineProvider()
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
		decrementPendingAuthIP(w.pendingAuthsByIP, auth.IP)
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
		L().Info("tg_code rejected: malformed payload", zap.Any("remote_addr", r.RemoteAddr))
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
		L().Info("signing in with code", zap.Any("phone", maskPhone(phone)), zap.Any("has_password", password != ""))
		err := client.SignIn(phone, code, password)
		if err != nil {
			L().Warn("sign in failed", zap.Any("phone", maskPhone(phone)), zap.Error(err))
			writeTelegramAuthError(wr, err)
			return
		}
		L().Info("sign in succeeded", zap.Any("phone", maskPhone(phone)))
		if err := w.finishPendingLogin(client); err != nil {
			L().Warn("finish after tg_code failed", zap.Error(err))
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
		L().Warn("finish_login failed", zap.Error(err))
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
		w.qrLogin = qrLoginState{}
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
	L().Info("custom inline bot saved", zap.Any("username", username))
	writeString(wr, "OK")
}

func (w *Web) InitQRLoginHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	url, err := w.initQRLogin(r)
	if err != nil {
		L().Warn("QR login init failed", zap.Error(err))
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	writeString(wr, url)
}

func (w *Web) initQRLogin(r *http.Request) (string, error) {
	w.mu.Lock()
	if w.qrTaskActive {
		if w.qrLogin.URL != "" {
			url := w.qrLogin.URL
			w.mu.Unlock()
			return url, nil
		}
		w.mu.Unlock()
		return "", fmt.Errorf("QR login is already initializing")
	}
	if w.pendingClient != nil {
		if oldClient, ok := w.pendingClient.(TelegramClient); ok && oldClient != nil {
			_ = oldClient.Disconnect()
		}
		w.pendingClient = nil
		w.qrLogin = qrLoginState{}
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
		if w.qrLogin.URL == "" {
			w.qrTaskActive = false
		}
		w.mu.Unlock()
	}()

	if ok && client != nil {
		L().Info("QR login connect started", zap.Any("remote_addr", r.RemoteAddr))
		if err := client.Connect(); err != nil {
			return "", fmt.Errorf("connect failed: %v", err)
		}
		L().Info("QR login export token started")
		url, err := client.QRLogin()
		if err != nil {
			return "", err
		}
		w.mu.Lock()
		w.qrLogin = qrLoginState{URL: url}
		w.mu.Unlock()
		L().Info("QR login URL generated, len", zap.Any("len", len(url)))
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
					w.qrLogin = qrLoginState{Done: true}
					w.qrTaskActive = false
					w.mu.Unlock()
					L().Info("QR login completed, 2FA required")
					return
				}
				L().Warn("QR login poll error", zap.Error(err))
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "canceled") || strings.Contains(errStr, "closed") || strings.Contains(errStr, "dead") {
					L().Info("stopping QR login poll because client connection is inactive")
					return
				}
			} else if status == "SUCCESS" {
				if err := w.finishPendingLogin(client); err != nil {
					L().Warn("QR finish_login failed", zap.Error(err))
					return
				}
				w.mu.Lock()
				w.twoFANeeded = false
				w.qrLogin = qrLoginState{Done: true}
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

	if qr.URL != "" {
		wr.WriteHeader(http.StatusCreated) // 201 Created
		writeString(wr, qr.URL)
		return
	}
	if qr.Done {
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
		L().Warn("get_qr_url init failed", zap.Error(err))
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
		L().Warn("QR 2FA failed", zap.Error(err))
		http.Error(wr, err.Error(), http.StatusForbidden)
		return
	}
	L().Info("QR 2FA accepted")
	if err := w.finishPendingLogin(client); err != nil {
		L().Warn("QR 2FA finish_login failed", zap.Error(err))
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
