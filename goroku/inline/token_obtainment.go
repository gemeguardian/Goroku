package inline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const botIDPattern = `(?s)<a[^>]*href="/botfather/bot/(\d+)"[^>]*>(?:[^<]|<[^/]|</[^a]|</[aA][^>])*@%s.*?</a>`

var (
	hashPattern        = regexp.MustCompile(`Main\.init\(\s*['"]([^'"]+)['"]\s*\);?`)
	botCommandsPattern = regexp.MustCompile(`(?s)data-command=["']([^"']+)["'].*?class=["']tm-row-desc[^"']*["']>\s*([^<]+?)\s*</span>`)
	botBasePattern     = regexp.MustCompile(fmt.Sprintf(botIDPattern, `\w*_[0-9a-zA-Z]{6}_bot`))
)

func (im *InlineManager) getWebAppSession(ctx context.Context, webAppURL string) (*http.Client, string, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
	}

	u, err := url.Parse(webAppURL)
	if err != nil {
		return nil, "", err
	}

	var decodedData string
	parts := strings.Split(webAppURL, "tgWebAppData=")
	if len(parts) > 1 {
		subParts := strings.Split(parts[1], "&tgWebAppVersion")
		decoded, err := url.QueryUnescape(subParts[0])
		if err == nil {
			decodedData = decoded
		} else {
			decodedData = subParts[0]
		}
	} else {
		decodedData = u.Query().Get("tgWebAppData")
	}

	baseURL := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)

	apiURL := baseURL + "/api?hash=-"
	data := url.Values{}
	data.Set("_auth", decodedData)
	data.Set("method", "auth")

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Referer", "https://webappinternal.telegram.org/botfather")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "stel_ln=ru")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("auth status code %d", resp.StatusCode)
	}

	reqGet, err := http.NewRequestWithContext(ctx, "GET", baseURL, nil)
	if err != nil {
		return nil, "", err
	}
	reqGet.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	reqGet.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	reqGet.Header.Set("Referer", "https://webappinternal.telegram.org/botfather")
	reqGet.Header.Set("Cookie", "stel_ln=ru")

	respGet, err := client.Do(reqGet)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = respGet.Body.Close() }()

	bodyBytes, err := io.ReadAll(respGet.Body)
	if err != nil {
		return nil, "", err
	}

	bodyText := string(bodyBytes)
	matches := hashPattern.FindStringSubmatch(bodyText)
	if len(matches) < 2 {
		return nil, "", fmt.Errorf("hash not found in page body")
	}

	return client, matches[1], nil
}

func (im *InlineManager) assertToken(ctx context.Context, client *http.Client, baseURL, hash string, createNewIfNeeded, revokeToken bool) (bool, error) {
	if im.tokenValue() != "" {
		return true, nil
	}
	token, err := im.getToken()
	if err != nil {
		return false, err
	}
	if token != "" {
		im.setToken(token)
		return true, nil
	}
	customBot, err := im.getCustomBot()
	if err != nil {
		return false, err
	}
	return im.assertTokenWithCustomBot(ctx, client, baseURL, hash, createNewIfNeeded, revokeToken, customBot)
}

func (im *InlineManager) assertTokenWithCustomBot(ctx context.Context, client *http.Client, baseURL, hash string, createNewIfNeeded, revokeToken bool, customBot string) (bool, error) {

	log.Println("[Inline] Bot token not found in db, searching in BotFather WebApp...")

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	bodyText := string(bodyBytes)

	// Regexp to find bot ID
	var botID string
	var botIDRegex *regexp.Regexp
	if customBot != "" {
		botIDRegex = regexp.MustCompile(fmt.Sprintf(botIDPattern, regexp.QuoteMeta(customBot)))
	} else {
		botIDRegex = botBasePattern
	}

	matches := botIDRegex.FindStringSubmatch(bodyText)
	if len(matches) > 1 {
		botID = matches[1]
	}

	if botID != "" {
		var token string
		if revokeToken {
			apiURL := baseURL + "/api?hash=" + hash
			data := url.Values{}
			data.Set("bid", botID)
			data.Set("method", "revokeAccessToken")

			reqPost, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
			if err != nil {
				return false, err
			}
			reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
			reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			respPost, err := client.Do(reqPost)
			if err != nil {
				return false, err
			}
			defer func() { _ = respPost.Body.Close() }()

			var result struct {
				Ok    bool   `json:"ok"`
				Token string `json:"token"`
			}
			if err := json.NewDecoder(respPost.Body).Decode(&result); err == nil && result.Ok {
				token = result.Token
			}
		} else {
			botURL := fmt.Sprintf("%s/bot/%s", baseURL, botID)
			reqGet, err := http.NewRequestWithContext(ctx, "GET", botURL, nil)
			if err != nil {
				return false, err
			}
			reqGet.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
			reqGet.Header.Set("x-aj-referer", "https://webappinternal.telegram.org/botfather")
			reqGet.Header.Set("x-requested-with", "XMLHttpRequest")

			respGet, err := client.Do(reqGet)
			if err != nil {
				return false, err
			}
			defer func() { _ = respGet.Body.Close() }()

			var result struct {
				H string `json:"h"`
			}
			if err := json.NewDecoder(respGet.Body).Decode(&result); err == nil {
				tokenRegex := regexp.MustCompile(`(\d+:[A-Za-z0-9\-_]{35})`)
				tokenMatches := tokenRegex.FindStringSubmatch(result.H)
				if len(tokenMatches) > 1 {
					token = tokenMatches[1]
				}
			}
		}

		if token != "" {
			if dbTyped, ok := im.db.(interface {
				Set(string, string, any) error
			}); ok {
				if err := dbTyped.Set("goroku.inline", "bot_token", token); err != nil {
					return false, err
				}
			}
			im.setToken(token)

			// Set settings
			settings := map[string]string{
				"settings[inline]": "true",
				"settings[inph]":   "user@goroku:~$",
				"settings[infdb]":  "1",
			}
			for key, val := range settings {
				apiURL := baseURL + "/api?hash=" + hash
				data := url.Values{}
				data.Set("bid", botID)
				data.Set("method", "changeSettings")
				data.Set(key, val)

				reqSet, _ := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
				reqSet.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
				reqSet.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if rs, err := client.Do(reqSet); err == nil {
					_ = rs.Body.Close()
				}
			}

			im.mu.Lock()
			im.BotID = 0 // Will get on get_me
			im.mu.Unlock()
			return true, nil
		}
	}

	if createNewIfNeeded {
		return im.createBotWithCustomBot(ctx, client, baseURL, hash, customBot)
	}

	return false, fmt.Errorf("bot not found and createNewIfNeeded is false")
}

func (im *InlineManager) createBot(ctx context.Context, client *http.Client, baseURL, hash string) (bool, error) {
	customBot, err := im.getCustomBot()
	if err != nil {
		return false, err
	}
	return im.createBotWithCustomBot(ctx, client, baseURL, hash, customBot)
}

func (im *InlineManager) getCustomBot() (string, error) {
	if im.db == nil {
		return "", nil
	}
	raw, err := im.db.Get("goroku.inline", "custom_bot", "")
	if err != nil {
		return "", fmt.Errorf("read goroku.inline.custom_bot: %w", err)
	}
	if customBot, ok := raw.(string); ok {
		return strings.TrimPrefix(customBot, "@"), nil
	}
	return "", nil
}

func (im *InlineManager) createBotWithCustomBot(ctx context.Context, client *http.Client, baseURL, hash, customBot string) (bool, error) {
	log.Println("[Inline] Creating new inline helper bot...")

	var username string
	latinMock := []string{"Goroku", "Helper", "Userbot", "MyBot"}

	if customBot != "" {
		username = customBot
	} else {
		uid := fmt.Sprintf("%d", rand.Intn(900000)+100000) //nolint:gosec
		genran := latinMock[rand.Intn(len(latinMock))]     //nolint:gosec
		username = fmt.Sprintf("%s_%s_bot", genran, uid)
	}

	// Check if username is occupied
	for i := 0; i < 5; i++ {
		apiURL := baseURL + "/api?hash=" + hash
		data := url.Values{}
		data.Set("username", "@"+username)
		data.Set("method", "checkBotUsername")

		reqPost, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
		if err != nil {
			return false, err
		}
		reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
		reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		respPost, err := client.Do(reqPost)
		if err != nil {
			return false, err
		}
		defer func() { _ = respPost.Body.Close() }()

		var result struct {
			Ok bool `json:"ok"`
		}
		if err := json.NewDecoder(respPost.Body).Decode(&result); err == nil && result.Ok {
			break
		}

		// Generate new username if occupied
		uid := fmt.Sprintf("%d", rand.Intn(900000)+100000) //nolint:gosec
		genran := latinMock[rand.Intn(len(latinMock))]     //nolint:gosec
		username = fmt.Sprintf("%s_%s_bot", genran, uid)
	}

	// Create actual bot
	apiURL := baseURL + "/api?hash=" + hash
	data := url.Values{}
	data.Set("title", "🪐 Goroku Bot")
	data.Set("username", "@"+username)
	data.Set("about", "Inline Bot helper for Goroku Userbot")
	data.Set("method", "createBot")

	reqCreate, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return false, err
	}
	reqCreate.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	reqCreate.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	respCreate, err := client.Do(reqCreate)
	if err != nil {
		return false, err
	}
	defer func() { _ = respCreate.Body.Close() }()

	var res struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(respCreate.Body).Decode(&res); err != nil || !res.Ok {
		return false, fmt.Errorf("bot creation failed: %s", res.Error)
	}

	return im.assertTokenWithCustomBot(ctx, client, baseURL, hash, false, false, customBot)
}

func (im *InlineManager) dpRevokeToken(ctx context.Context, client *http.Client, baseURL, hash string, alreadyInitialised bool) (bool, error) {
	if dbTyped, ok := im.db.(interface {
		Set(string, string, any) error
	}); ok {
		if err := dbTyped.Set("goroku.inline", "bot_token", nil); err != nil {
			return false, err
		}
	}
	im.setToken("")

	return im.assertToken(ctx, client, baseURL, hash, true, true)
}

func (im *InlineManager) reassertToken(ctx context.Context, client *http.Client, baseURL, hash string) (bool, error) {
	if dbTyped, ok := im.db.(interface {
		Set(string, string, any) error
	}); ok {
		if err := dbTyped.Set("goroku.inline", "bot_token", nil); err != nil {
			return false, err
		}
	}
	im.setToken("")
	ok, err := im.assertToken(ctx, client, baseURL, hash, true, true)
	if err != nil {
		im.setInitComplete(false)
		return false, err
	}

	return ok, nil
}

func (im *InlineManager) checkBot(ctx context.Context, client *http.Client, baseURL, hash, username string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	bodyText := string(bodyBytes)

	username = strings.TrimPrefix(username, "@")
	botIDRegex := regexp.MustCompile(fmt.Sprintf(botIDPattern, regexp.QuoteMeta(username)))
	matches := botIDRegex.FindStringSubmatch(bodyText)
	if len(matches) > 1 {
		return true, nil
	}

	// Check if username is valid/available via API check
	apiURL := baseURL + "/api?hash=" + hash
	data := url.Values{}
	data.Set("username", "@"+username)
	data.Set("method", "checkBotUsername")

	reqPost, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return false, err
	}
	reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	respPost, err := client.Do(reqPost)
	if err != nil {
		return false, err
	}
	defer func() { _ = respPost.Body.Close() }()

	var result struct {
		Ok bool `json:"ok"`
	}
	if err := json.NewDecoder(respPost.Body).Decode(&result); err == nil {
		return result.Ok, nil
	}
	return false, nil
}

func (im *InlineManager) setCommands(ctx context.Context, client *http.Client, baseURL, hash string, commands map[string]string) (bool, error) {
	botID := im.BotIDVal()
	if botID == 0 {
		return false, fmt.Errorf("bot not initialized")
	}

	bid := fmt.Sprintf("%d", botID)

	for cmd, desc := range commands {
		apiURL := baseURL + "/api?hash=" + hash
		data := url.Values{}
		data.Set("bid", bid)
		data.Set("lang_code", "")
		data.Set("scopes[]", "users")
		data.Set("command", cmd)
		data.Set("description", desc)
		data.Set("method", "setCommand")

		reqPost, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
		if err != nil {
			return false, err
		}
		reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
		reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		respPost, err := client.Do(reqPost)
		if err == nil {
			_ = respPost.Body.Close()
		}
		if err := sleepContext(ctx, time.Second); err != nil {
			return false, err
		}
	}

	return true, nil
}

func (im *InlineManager) mainTokenManager(ctx context.Context, action int, optArgs map[string]any) (any, error) {
	var customBot string
	switch action {
	case 1:
		if im.tokenValue() != "" {
			return true, nil
		}
		token, err := im.getToken()
		if err != nil {
			return nil, err
		}
		if token != "" {
			im.setToken(token)
			return true, nil
		}
		customBot, err = im.getCustomBot()
		if err != nil {
			return nil, err
		}
	case 2, 3:
		var err error
		customBot, err = im.getCustomBot()
		if err != nil {
			return nil, err
		}
	case 4:
		token, err := im.getToken()
		if err != nil {
			return nil, err
		}
		if token == "" {
			customBot, err = im.getCustomBot()
			if err != nil {
				return nil, err
			}
		}
	}

	webAppURL, err := im.requestWebView(ctx, "@botfather", "android", "https://webappinternal.telegram.org/botfather?")
	if err != nil {
		return nil, err
	}

	var httpClient *http.Client
	var hash string
	for i := 0; i < 5; i++ {
		if err := sleepContext(ctx, 1500*time.Millisecond); err != nil {
			return nil, err
		}
		httpClient, hash, err = im.getWebAppSession(ctx, webAppURL)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("WebApp is not available: %w", err)
	}
	defer httpClient.CloseIdleConnections()

	u, _ := url.Parse(webAppURL)
	baseURL := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)

	switch action {
	case 1:
		createNewIfNeeded := true
		revokeToken := false
		if optArgs != nil {
			if v, exists := optArgs["create_new_if_needed"]; exists {
				createNewIfNeeded = v.(bool)
			}
			if v, exists := optArgs["revoke_token"]; exists {
				revokeToken = v.(bool)
			}
		}
		return im.assertTokenWithCustomBot(ctx, httpClient, baseURL, hash, createNewIfNeeded, revokeToken, customBot)
	case 2:
		return im.createBotWithCustomBot(ctx, httpClient, baseURL, hash, customBot)
	case 3:
		alreadyInitialised := true
		if optArgs != nil {
			if v, exists := optArgs["already_initialised"]; exists {
				alreadyInitialised = v.(bool)
			}
		}
		return im.dpRevokeToken(ctx, httpClient, baseURL, hash, alreadyInitialised)
	case 4:
		return im.reassertToken(ctx, httpClient, baseURL, hash)
	case 5:
		var username string
		if optArgs != nil {
			if v, exists := optArgs["username"]; exists {
				username = v.(string)
			}
		}
		return im.checkBot(ctx, httpClient, baseURL, hash, username)
	case 6:
		var commands map[string]string
		if optArgs != nil {
			if v, exists := optArgs["commands"]; exists {
				commands = v.(map[string]string)
			}
		}
		return im.setCommands(ctx, httpClient, baseURL, hash, commands)
	}
	return nil, fmt.Errorf("unknown action: %d", action)
}

func (im *InlineManager) runTokenManager(ctx context.Context, action int, optArgs map[string]any) (any, uint64, error) {
	im.tokenTxnMu.Lock()
	defer im.tokenTxnMu.Unlock()
	result, err := im.mainTokenManager(ctx, action, optArgs)
	return result, im.tokenRevisionValue(), err
}

type webViewJob struct {
	ctx    context.Context
	client interface {
		RequestWebView(peerUsername string, platform string, url string) (string, error)
	}
	peer     string
	platform string
	url      string
	result   chan webViewResult
}

type webViewResult struct {
	url string
	err error
}

var errWebViewBusy = errors.New("RequestWebView executor is busy")

func (im *InlineManager) requestWebView(ctx context.Context, peer, platform, webURL string) (string, error) {
	if client, ok := im.client.(interface {
		RequestWebViewContext(context.Context, string, string, string) (string, error)
	}); ok {
		return client.RequestWebViewContext(ctx, peer, platform, webURL)
	}
	client, ok := im.client.(interface {
		RequestWebView(string, string, string) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("client does not support RequestWebView")
	}

	// The legacy API cannot cancel an in-flight external call. One bounded
	// executor isolates that residual: Close can finish, and a permanently
	// blocked dependency consumes at most this single worker and queue slot.
	im.webViewOnce.Do(func() {
		im.webViewJobs = make(chan webViewJob, defaultWebViewQueueCapacity)
		go func() {
			for job := range im.webViewJobs {
				if job.ctx.Err() != nil {
					continue
				}
				url, err := job.client.RequestWebView(job.peer, job.platform, job.url)
				job.result <- webViewResult{url: url, err: err}
			}
		}()
	})
	job := webViewJob{ctx: ctx, client: client, peer: peer, platform: platform, url: webURL, result: make(chan webViewResult, 1)}
	select {
	case im.webViewJobs <- job:
	default:
		return "", errWebViewBusy
	}
	select {
	case result := <-job.result:
		return result.url, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Public wrapper methods mirroring Python's async functions
func (im *InlineManager) AssertToken(createNewIfNeeded, revokeToken bool) (bool, error) {
	generation, ctx, err := im.claimIntake()
	if err != nil {
		return false, err
	}
	defer generation.release()
	return im.assertTokenContext(ctx, createNewIfNeeded, revokeToken)
}

func (im *InlineManager) assertTokenContext(ctx context.Context, createNewIfNeeded, revokeToken bool) (bool, error) {
	args := map[string]any{
		"create_new_if_needed": createNewIfNeeded,
		"revoke_token":         revokeToken,
	}
	res, _, err := im.runTokenManager(ctx, 1, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) CreateBot() (bool, error) {
	generation, ctx, claimErr := im.claimIntake()
	if claimErr != nil {
		return false, claimErr
	}
	defer generation.release()
	res, _, err := im.runTokenManager(ctx, 2, nil)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) DPRevokeToken(alreadyInitialised bool) (bool, error) {
	generation, _, claimErr := im.claimIntake()
	if claimErr != nil {
		return false, claimErr
	}
	defer generation.release()
	// The generation remains live until the completed transaction schedules its
	// replacement, while Close can still cancel a blocked transaction.
	args := map[string]any{
		"already_initialised": alreadyInitialised,
	}
	ctx := im.lifecycleContext()
	res, revision, err := im.runTokenManager(ctx, 3, args)
	if err != nil {
		return false, err
	}
	ok := res.(bool)
	if ok && alreadyInitialised {
		im.restartAfter(generation, revision)
	}
	return ok, nil
}

func (im *InlineManager) ReassertToken() (bool, error) {
	generation, _, claimErr := im.claimIntake()
	if claimErr != nil {
		return false, claimErr
	}
	defer generation.release()
	res, revision, err := im.runTokenManager(im.lifecycleContext(), 4, nil)
	if err != nil {
		return false, err
	}
	ok := res.(bool)
	if ok {
		im.restartAfter(generation, revision)
	}
	return ok, nil
}

func (im *InlineManager) lifecycleContext() context.Context {
	im.mu.RLock()
	ctx := im.lifecycleCtx
	im.mu.RUnlock()
	if ctx == nil {
		return im.generationContext()
	}
	return ctx
}

func (im *InlineManager) CheckBot(username string) (bool, error) {
	generation, ctx, claimErr := im.claimIntake()
	if claimErr != nil {
		return false, claimErr
	}
	defer generation.release()
	args := map[string]any{
		"username": username,
	}
	res, _, err := im.runTokenManager(ctx, 5, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) SetCommands(commands map[string]string) (bool, error) {
	generation, ctx, claimErr := im.claimIntake()
	if claimErr != nil {
		return false, claimErr
	}
	defer generation.release()
	args := map[string]any{
		"commands": commands,
	}
	res, _, err := im.runTokenManager(ctx, 6, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}
