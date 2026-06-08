package inline

import (
	"encoding/json"
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

const botIDPattern = `<a class="tm-row tm-row-link" href="/botfather/bot/(\d+)">` +
	`<img class="tm-row-pic tm-row-pic-user" src="[^"]+">` +
	`<div> <div class="tm-row-value">[^<]*</div>` +
	`<div class="tm-row-description">@%s</div> </div></a>`


var (
	hashPattern        = regexp.MustCompile(`Main\.init\(\s*['"]([^'"]+)['"]\s*\);?`)
	botCommandsPattern = regexp.MustCompile(`(?s)data-command=["']([^"']+)["'].*?class=["']tm-row-desc[^"']*["']>\s*([^<]+?)\s*</span>`)
	botBasePattern     = regexp.MustCompile(fmt.Sprintf(botIDPattern, `\w*_[0-9a-zA-Z]{6}_bot`))
)

func (im *InlineManager) getWebAppSession(webAppURL string) (*http.Client, string, error) {
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

	req, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
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
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("auth status code %d", resp.StatusCode)
	}

	reqGet, err := http.NewRequest("GET", baseURL, nil)
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
	defer respGet.Body.Close()

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

func (im *InlineManager) assertToken(client *http.Client, baseURL, hash string, createNewIfNeeded, revokeToken bool) (bool, error) {
	if im.token != "" {
		return true, nil
	}

	log.Println("[Inline] Bot token not found in db, searching in BotFather WebApp...")

	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	bodyText := string(bodyBytes)

	// Regexp to find bot ID
	var botID string
	var customBot string
	if dbTyped, ok := im.db.(interface {
		Get(string, string, interface{}) interface{}
	}); ok {
		if cb, ok := dbTyped.Get("goroku.inline", "custom_bot", "").(string); ok && cb != "" {
			customBot = strings.TrimPrefix(cb, "@")
		}
	}

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

			reqPost, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
			if err != nil {
				return false, err
			}
			reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
			reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			respPost, err := client.Do(reqPost)
			if err != nil {
				return false, err
			}
			defer respPost.Body.Close()

			var result struct {
				Ok    bool   `json:"ok"`
				Token string `json:"token"`
			}
			if err := json.NewDecoder(respPost.Body).Decode(&result); err == nil && result.Ok {
				token = result.Token
			}
		} else {
			botURL := fmt.Sprintf("%s/bot/%s", baseURL, botID)
			reqGet, err := http.NewRequest("GET", botURL, nil)
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
			defer respGet.Body.Close()

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
				Set(string, string, interface{}) bool
			}); ok {
				dbTyped.Set("goroku.inline", "bot_token", token)
			}
			im.token = token

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

				reqSet, _ := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
				reqSet.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
				reqSet.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if rs, err := client.Do(reqSet); err == nil {
					rs.Body.Close()
				}
			}

			im.BotID = 0 // Will get on get_me
			return true, nil
		}
	}

	if createNewIfNeeded {
		return im.createBot(client, baseURL, hash)
	}

	return false, fmt.Errorf("bot not found and createNewIfNeeded is false")
}

func (im *InlineManager) createBot(client *http.Client, baseURL, hash string) (bool, error) {
	log.Println("[Inline] Creating new inline helper bot...")

	var customBot string
	if dbTyped, ok := im.db.(interface {
		Get(string, string, interface{}) interface{}
	}); ok {
		if cb, ok := dbTyped.Get("goroku.inline", "custom_bot", "").(string); ok && cb != "" {
			customBot = strings.TrimPrefix(cb, "@")
		}
	}

	var username string
	latinMock := []string{"Goroku", "Helper", "Userbot", "MyBot"}
	
	if customBot != "" {
		username = customBot
	} else {
		rand.Seed(time.Now().UnixNano())
		uid := fmt.Sprintf("%d", rand.Intn(900000)+100000)
		genran := latinMock[rand.Intn(len(latinMock))]
		username = fmt.Sprintf("%s_%s_bot", genran, uid)
	}

	// Check if username is occupied
	for i := 0; i < 5; i++ {
		apiURL := baseURL + "/api?hash=" + hash
		data := url.Values{}
		data.Set("username", "@"+username)
		data.Set("method", "checkBotUsername")

		reqPost, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
		if err != nil {
			return false, err
		}
		reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
		reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		respPost, err := client.Do(reqPost)
		if err != nil {
			return false, err
		}
		defer respPost.Body.Close()

		var result struct {
			Ok bool `json:"ok"`
		}
		if err := json.NewDecoder(respPost.Body).Decode(&result); err == nil && result.Ok {
			break
		}

		// Generate new username if occupied
		uid := fmt.Sprintf("%d", rand.Intn(900000)+100000)
		genran := latinMock[rand.Intn(len(latinMock))]
		username = fmt.Sprintf("%s_%s_bot", genran, uid)
	}

	// Create actual bot
	apiURL := baseURL + "/api?hash=" + hash
	data := url.Values{}
	data.Set("title", "🪐 Goroku Bot")
	data.Set("username", "@"+username)
	data.Set("about", "Inline Bot helper for Goroku Userbot")
	data.Set("method", "createBot")

	reqCreate, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return false, err
	}
	reqCreate.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	reqCreate.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	respCreate, err := client.Do(reqCreate)
	if err != nil {
		return false, err
	}
	defer respCreate.Body.Close()

	var res struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(respCreate.Body).Decode(&res); err != nil || !res.Ok {
		return false, fmt.Errorf("bot creation failed: %s", res.Error)
	}

	return im.assertToken(client, baseURL, hash, false, false)
}

func (im *InlineManager) dpRevokeToken(client *http.Client, baseURL, hash string, alreadyInitialised bool) (bool, error) {
	if alreadyInitialised {
		im.Stop()
	}

	if dbTyped, ok := im.db.(interface {
		Set(string, string, interface{}) bool
	}); ok {
		dbTyped.Set("goroku.inline", "bot_token", nil)
	}
	im.token = ""

	return im.assertToken(client, baseURL, hash, true, true)
}

func (im *InlineManager) reassertToken(client *http.Client, baseURL, hash string) (bool, error) {
	ok, err := im.assertToken(client, baseURL, hash, true, true)
	if err != nil {
		im.initComplete = false
		return false, err
	}
	
	if ok {
		err = im.RegisterManager(false, true)
		return err == nil, err
	}
	return false, nil
}

func (im *InlineManager) checkBot(client *http.Client, baseURL, hash, username string) (bool, error) {
	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

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

	reqPost, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return false, err
	}
	reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	respPost, err := client.Do(reqPost)
	if err != nil {
		return false, err
	}
	defer respPost.Body.Close()

	var result struct {
		Ok bool `json:"ok"`
	}
	if err := json.NewDecoder(respPost.Body).Decode(&result); err == nil {
		return result.Ok, nil
	}
	return false, nil
}

func (im *InlineManager) setCommands(client *http.Client, baseURL, hash string, commands map[string]string) (bool, error) {
	if im.BotID == 0 {
		return false, fmt.Errorf("bot not initialized")
	}

	bid := fmt.Sprintf("%d", im.BotID)

	for cmd, desc := range commands {
		apiURL := baseURL + "/api?hash=" + hash
		data := url.Values{}
		data.Set("bid", bid)
		data.Set("lang_code", "")
		data.Set("scopes[]", "users")
		data.Set("command", cmd)
		data.Set("description", desc)
		data.Set("method", "setCommand")

		reqPost, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
		if err != nil {
			return false, err
		}
		reqPost.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
		reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		respPost, err := client.Do(reqPost)
		if err == nil {
			respPost.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}

	return true, nil
}

func (im *InlineManager) mainTokenManager(action int, optArgs map[string]interface{}) (interface{}, error) {
	clientInterface, ok := im.client.(interface {
		RequestWebView(peerUsername string, platform string, url string) (string, error)
	})
	if !ok {
		return nil, fmt.Errorf("client does not support RequestWebView")
	}

	webAppURL, err := clientInterface.RequestWebView("@botfather", "android", "https://webappinternal.telegram.org/botfather?")
	if err != nil {
		return nil, err
	}

	var httpClient *http.Client
	var hash string
	for i := 0; i < 5; i++ {
		time.Sleep(1500 * time.Millisecond)
		httpClient, hash, err = im.getWebAppSession(webAppURL)
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
		return im.assertToken(httpClient, baseURL, hash, createNewIfNeeded, revokeToken)
	case 2:
		return im.createBot(httpClient, baseURL, hash)
	case 3:
		alreadyInitialised := true
		if optArgs != nil {
			if v, exists := optArgs["already_initialised"]; exists {
				alreadyInitialised = v.(bool)
			}
		}
		return im.dpRevokeToken(httpClient, baseURL, hash, alreadyInitialised)
	case 4:
		return im.reassertToken(httpClient, baseURL, hash)
	case 5:
		var username string
		if optArgs != nil {
			if v, exists := optArgs["username"]; exists {
				username = v.(string)
			}
		}
		return im.checkBot(httpClient, baseURL, hash, username)
	case 6:
		var commands map[string]string
		if optArgs != nil {
			if v, exists := optArgs["commands"]; exists {
				commands = v.(map[string]string)
			}
		}
		return im.setCommands(httpClient, baseURL, hash, commands)
	}
	return nil, fmt.Errorf("unknown action: %d", action)
}

// Public wrapper methods mirroring Python's async functions
func (im *InlineManager) AssertToken(createNewIfNeeded, revokeToken bool) (bool, error) {
	args := map[string]interface{}{
		"create_new_if_needed": createNewIfNeeded,
		"revoke_token":         revokeToken,
	}
	res, err := im.mainTokenManager(1, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) CreateBot() (bool, error) {
	res, err := im.mainTokenManager(2, nil)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) DPRevokeToken(alreadyInitialised bool) (bool, error) {
	args := map[string]interface{}{
		"already_initialised": alreadyInitialised,
	}
	res, err := im.mainTokenManager(3, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) ReassertToken() (bool, error) {
	res, err := im.mainTokenManager(4, nil)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) CheckBot(username string) (bool, error) {
	args := map[string]interface{}{
		"username": username,
	}
	res, err := im.mainTokenManager(5, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

func (im *InlineManager) SetCommands(commands map[string]string) (bool, error) {
	args := map[string]interface{}{
		"commands": commands,
	}
	res, err := im.mainTokenManager(6, args)
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}
