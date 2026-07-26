package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

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

// maxSessions caps the session map after expired entries are swept.
const maxSessions = 1024

// sweepSessionsLocked drops expired sessions. w.mu must be held for writing.
// Expiry is otherwise only noticed when a specific token is looked up again, so
// sessions that are never revisited would accumulate for the process lifetime.
func (w *Web) sweepSessionsLocked() {
	now := time.Now()
	for token, sess := range w.sessions {
		if now.After(sess.Expiry) {
			delete(w.sessions, token)
		}
	}
	if len(w.sessions) < maxSessions {
		return
	}
	// Pathological case (more than maxSessions live, unexpired sessions): drop
	// the ones closest to expiring rather than letting the map grow unbounded.
	oldest, oldestExpiry := "", time.Time{}
	for len(w.sessions) >= maxSessions {
		oldest, oldestExpiry = "", time.Time{}
		for token, sess := range w.sessions {
			if oldest == "" || sess.Expiry.Before(oldestExpiry) {
				oldest, oldestExpiry = token, sess.Expiry
			}
		}
		delete(w.sessions, oldest)
	}
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
	w.sweepSessionsLocked()
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
	w.sweepSessionsLocked()
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
	w.sweepSessionsLocked()
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

// sameOrigin reports whether Origin/Referer match the request host.
// Missing both headers is not accepted for browser cookie session mutating requests.

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
