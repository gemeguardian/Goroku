package goroku

import (
	"bufio"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"goroku/goroku/web"

	"github.com/gotd/td/tg"
)

var (
	BaseDir    string
	BasePath   string
	ConfigPath string
)

func init() {
	if os.Getenv("DOCKER") != "" {
		BaseDir = "/data"
	} else if cwd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(cwd, "go.mod")); statErr == nil {
			BaseDir = cwd
		} else if execPath, execErr := os.Executable(); execErr == nil {
			BaseDir = filepath.Dir(execPath)
		} else {
			BaseDir = cwd
		}
	} else if execPath, err := os.Executable(); err == nil {
		BaseDir = filepath.Dir(execPath)
	} else {
		BaseDir = "."
	}
	BasePath = BaseDir
	ConfigPath = filepath.Join(BaseDir, "config.json")
}

type Goroku struct {
	OmitLog     bool
	APIID       int64
	APIHash     string
	Port        int
	DisableWeb  bool
	NoGit       bool
	QRLogin     bool
	NoAuth      bool
	Sandbox     bool
	ProxyHost   string
	ProxyPort   int
	ProxySecret string
	ProxyPass   string
	Clients     []*CustomTelegramClient
	DBs         []*Database
	Loaders     []*Modules
	Web         *web.WebCore
}

func NewGoroku() *Goroku {
	return &Goroku{
		Clients: make([]*CustomTelegramClient, 0),
		DBs:     make([]*Database, 0),
		Loaders: make([]*Modules, 0),
	}
}

func GetConfigKey(key string) interface{} {
	content, err := os.ReadFile(ConfigPath)
	if err != nil {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		return nil
	}
	return data[key]
}

func SaveConfigKey(key string, value interface{}) bool {
	var data map[string]interface{}
	content, err := os.ReadFile(ConfigPath)
	if err == nil {
		json.Unmarshal(content, &data)
	}
	if data == nil {
		data = make(map[string]interface{})
	}
	data[key] = value
	bytes, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return false
	}
	err = os.WriteFile(ConfigPath, bytes, 0600)
	utils.SecureFile(ConfigPath)
	return err == nil
}

func randomSetupToken() string {
	buf := make([]byte, 24)
	if _, err := crand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (h *Goroku) ParseArguments() {
	rootFlag := flag.Bool("root", false, "Allow running as root")
	portFlag := flag.Int("port", 8080, "Port for web panel")
	noWebFlag := flag.Bool("no-web", false, "Disable web setup dashboard")
	noGitFlag := flag.Bool("no-git", false, "Disable git operations")
	qrLoginFlag := flag.Bool("qr-login", false, "Use QR code login")
	noAuthFlag := flag.Bool("no-auth", false, "Skip interactive auth")
	sandboxFlag := flag.Bool("sandbox", false, "Sandbox mode: disable restarts")
	dataRootFlag := flag.String("data-root", "", "Custom path to data directory")
	proxyHostFlag := flag.String("proxy-host", "", "MTProto proxy host")
	proxyPortFlag := flag.Int("proxy-port", 0, "MTProto proxy port")
	proxySecretFlag := flag.String("proxy-secret", "", "MTProto proxy secret")
	proxyPassFlag := flag.String("proxy-pass", "", "MTProto proxy password")
	flag.Parse()

	_ = rootFlag
	h.Port = *portFlag
	h.DisableWeb = *noWebFlag
	h.NoGit = *noGitFlag
	h.QRLogin = *qrLoginFlag
	h.NoAuth = *noAuthFlag
	h.Sandbox = *sandboxFlag
	h.ProxyHost = *proxyHostFlag
	h.ProxyPort = *proxyPortFlag
	h.ProxySecret = *proxySecretFlag
	h.ProxyPass = *proxyPassFlag

	// Override BaseDir if --data-root is specified
	if *dataRootFlag != "" {
		BaseDir = *dataRootFlag
		BasePath = BaseDir
		ConfigPath = filepath.Join(BaseDir, "config.json")
	}

	// Propagate no-git to env for modules that check it
	if h.NoGit {
		os.Setenv("GOROKU_NO_GIT", "1")
	}
}

func Main(customModules []Module) {
	h := NewGoroku()
	h.ParseArguments()
	utils.SecureFile(ConfigPath)

	fmt.Println("🪐 Starting Goroku Go Userbot...")

	// Retrieve API ID and HASH
	apiIDVal := GetConfigKey("api_id")
	apiHashVal := GetConfigKey("api_hash")

	if apiIDVal == nil || apiHashVal == nil {
		envID := os.Getenv("api_id")
		envHash := os.Getenv("api_hash")
		if envID != "" && envHash != "" {
			id, _ := strconv.ParseInt(envID, 10, 64)
			h.APIID = id
			h.APIHash = envHash
		} else {
			if h.DisableWeb {
				fmt.Println("No API ID or HASH found in config or environment.")
				for h.APIID == 0 {
					input := promptInput("Enter Telegram API ID: ")
					id, err := strconv.ParseInt(input, 10, 64)
					if err == nil && id > 0 {
						h.APIID = id
					} else {
						fmt.Println("Invalid API ID. Please enter a valid number.")
					}
				}
				for h.APIHash == "" {
					h.APIHash = promptInput("Enter Telegram API HASH: ")
				}
				SaveConfigKey("api_id", h.APIID)
				SaveConfigKey("api_hash", h.APIHash)
			} else {
				fmt.Println("No API ID or HASH found in config or environment. Starting web dashboard for setup...")
			}
		}
	} else {
		switch v := apiIDVal.(type) {
		case float64:
			h.APIID = int64(v)
		case string:
			id, _ := strconv.ParseInt(v, 10, 64)
			h.APIID = id
		}
		h.APIHash = fmt.Sprintf("%v", apiHashVal)
	}

	InitLogging()

	// Check for temporary session goroku-0.session
	zeroSession := filepath.Join(BaseDir, "goroku-0.session")
	if _, err := os.Stat(zeroSession); err == nil && h.APIID != 0 && h.APIHash != "" {
		log.Println("Found pending goroku-0.session, resolving real TGID...")
		client := NewCustomTelegramClient(0)
		client.APIID = h.APIID
		client.APIHash = h.APIHash
		client.SessionPath = zeroSession
		if err := client.Connect(); err == nil {
			realID := client.TGID
			client.Disconnect()
			time.Sleep(500 * time.Millisecond) // wait for gotd file release
			newPath := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", realID))
			_ = os.Rename(zeroSession, newPath)
			utils.SecureFile(newPath)
			log.Printf("Successfully renamed goroku-0.session to goroku-%d.session\n", realID)
		} else {
			log.Printf("Failed to connect with goroku-0.session: %v\n", err)
		}
	}

	// Initialize WebCore
	if !h.DisableWeb {
		setupToken := strings.TrimSpace(os.Getenv("GOROKU_SETUP_TOKEN"))
		if setupToken == "" {
			setupToken = randomSetupToken()
			os.Setenv("GOROKU_SETUP_TOKEN", setupToken)
		}
		apiToken := interface{}(nil)
		if h.APIID != 0 && h.APIHash != "" {
			apiToken = h.APIHash
		}
		h.Web = web.NewWebCore(web.WebConfig{
			ApiToken:   apiToken,
			SetupToken: setupToken,
			DataRoot:   BaseDir,
			SaveConfig: SaveConfigKey,
			Restart:    Restart,
			OnLogin: func(client interface{}) error {
				return h.finishWebLogin(client, customModules)
			},
			GetClient: func() interface{} {
				apiID := h.APIID
				apiHash := h.APIHash

				if apiID == 0 {
					if val := GetConfigKey("api_id"); val != nil {
						switch v := val.(type) {
						case float64:
							apiID = int64(v)
						case string:
							apiID, _ = strconv.ParseInt(v, 10, 64)
						}
					}
				}
				if apiHash == "" {
					if val := GetConfigKey("api_hash"); val != nil {
						apiHash = fmt.Sprintf("%v", val)
					}
				}

				c := NewCustomTelegramClient(0)
				c.APIID = apiID
				c.APIHash = apiHash
				return c
			},
		})
		h.Web.SetPort(h.Port)
		go h.Web.Start(h.Port, true)
		setupURL := h.Web.GetURL(true)
		hasExistingSessions := false
		for _, pattern := range []string{
			filepath.Join(BaseDir, "goroku-*.session"),
			filepath.Join(BaseDir, "heroku-*.session"),
			filepath.Join(BaseDir, "hikka-*.session"),
		} {
			files, _ := filepath.Glob(pattern)
			for _, f := range files {
				base := filepath.Base(f)
				if base != "goroku-0.session" && base != "hikka-0.session" {
					hasExistingSessions = true
					break
				}
			}
			if hasExistingSessions {
				break
			}
		}
		if !hasExistingSessions {
			sep := "?"
			if strings.Contains(setupURL, "?") {
				sep = "&"
			}
			setupURL = setupURL + sep + "setup_token=" + setupToken
		}
		log.Printf("🔎 Web mode ready. URL: %s\n", setupURL)
	}

	// Scan sessions — goroku-*.session, heroku-*.session, and hikka-*.session formats
	sessionPatterns := []string{
		filepath.Join(BaseDir, "goroku-*.session"),
		filepath.Join(BaseDir, "heroku-*.session"),
		filepath.Join(BaseDir, "hikka-*.session"),
	}

	var activeSessions []string
	for _, pattern := range sessionPatterns {
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			base := filepath.Base(f)
			if base == "goroku-0.session" || base == "hikka-0.session" {
				continue
			}
			utils.SecureFile(f)
			activeSessions = append(activeSessions, f)
		}
	}

	if len(activeSessions) == 0 {
		if h.DisableWeb {
			h.startCliLogin(customModules)
		} else {
			log.Println("No active sessions found. Please use the Web dashboard to log in.")
		}
	} else {
		for _, sessionFile := range activeSessions {
			tgID, err := getTGIDFromSessionPath(sessionFile)
			if err != nil {
				log.Printf("Skip invalid session file %s: %v\n", sessionFile, err)
				continue
			}
			log.Printf("Booting userbot for client ID: %d...\n", tgID)
			client, err := h.initClient(tgID, sessionFile, customModules)
			if err != nil {
				log.Printf("Failed to init client %d: %v\n", tgID, err)
				if strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
					HandleAuthKeyUnregistered(tgID, sessionFile)
				}
				continue
			}
			if h.Web != nil {
				loader := h.Loaders[len(h.Loaders)-1]
				db := h.DBs[len(h.DBs)-1]
				h.Web.AddLoader(client, loader, db)
			}
		}
	}

	// Set up graceful shutdown on SIGTERM/SIGINT
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		sig := <-sigCh
		log.Printf("Received signal %v, initiating graceful shutdown...\n", sig)
		os.Exit(0)
	}()

	// Keep running
	select {}
}

func (h *Goroku) initClient(tgID int64, sessionPath string, customModules []Module) (*CustomTelegramClient, error) {
	utils.SecureFile(sessionPath)
	db := NewDatabase(tgID)
	redisURI := os.Getenv("REDIS_URL")
	if redisURI == "" {
		if val := GetConfigKey("redis_uri"); val != nil {
			redisURI = fmt.Sprintf("%v", val)
		}
	}
	db.Init(redisURI)

	client := NewCustomTelegramClient(tgID)
	client.APIID = h.APIID
	client.APIHash = h.APIHash
	client.SessionPath = sessionPath
	client.GorokuDB = db
	db.client = client

	if err := client.Connect(); err != nil {
		return nil, err
	}

	loader := NewModules(client, db)
	client.Loader = loader

	inlineMgr := inline.NewInlineManager(client, db, loader)
	client.GorokuInline = inlineMgr

	h.registerBuiltInModules(loader)
	for _, mod := range customModules {
		if err := loader.RegisterModule(cloneModule(mod)); err != nil {
			log.Printf("Failed to register module %s: %v\n", mod.Name(), err)
		}
	}

	disp := NewCommandDispatcher(loader, client, db)
	loader.SetDispatcher(disp)

	loader.SendReady()

	h.sendBadge(client, db)

	h.Clients = append(h.Clients, client)
	h.DBs = append(h.DBs, db)
	h.Loaders = append(h.Loaders, loader)

	return client, nil
}

func (h *Goroku) finishWebLogin(pending interface{}, customModules []Module) error {
	pendingClient, ok := pending.(*CustomTelegramClient)
	if !ok || pendingClient == nil {
		return fmt.Errorf("unexpected pending client type %T", pending)
	}

	apiID := pendingClient.APIID
	apiHash := pendingClient.APIHash
	tgID := pendingClient.TGID
	if tgID == 0 {
		me, err := pendingClient.GetMe()
		if err != nil {
			return fmt.Errorf("failed to get authorized user: %v", err)
		}
		if user, ok := me.(*tg.User); ok {
			tgID = user.ID
		}
	}
	if tgID == 0 {
		return fmt.Errorf("authorized Telegram user id is unknown")
	}

	_ = pendingClient.Disconnect()
	time.Sleep(500 * time.Millisecond)

	oldSession := filepath.Join(BaseDir, "goroku-0.session")
	newSession := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", tgID))
	if oldSession != newSession {
		if _, err := os.Stat(oldSession); err == nil {
			if err := os.Rename(oldSession, newSession); err != nil {
				return fmt.Errorf("failed to rename temporary session: %v", err)
			}
			utils.SecureFile(newSession)
			log.Printf("Renamed temporary session to %s\n", filepath.Base(newSession))
		}
	}

	for _, existing := range h.Clients {
		if existing.TGID == tgID {
			return fmt.Errorf("client %d is already running", tgID)
		}
	}

	client := NewCustomTelegramClient(tgID)
	client.APIID = apiID
	client.APIHash = apiHash
	client.SessionPath = newSession
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to reconnect authorized client: %v", err)
	}

	db := NewDatabase(tgID)
	redisURI := os.Getenv("REDIS_URL")
	if redisURI == "" {
		if val := GetConfigKey("redis_uri"); val != nil {
			redisURI = fmt.Sprintf("%v", val)
		}
	}
	db.Init(redisURI)
	if customBot, ok := GetConfigKey("custom_bot").(string); ok && customBot != "" {
		db.Set("goroku.inline", "custom_bot", customBot)
	}
	client.GorokuDB = db
	db.client = client

	loader := NewModules(client, db)
	client.Loader = loader

	inlineMgr := inline.NewInlineManager(client, db, loader)
	client.GorokuInline = inlineMgr

	h.registerBuiltInModules(loader)
	for _, mod := range customModules {
		freshMod := cloneModule(mod)
		if err := loader.RegisterModule(freshMod); err != nil {
			log.Printf("Failed to register module %s for web login: %v\n", mod.Name(), err)
		}
	}

	disp := NewCommandDispatcher(loader, client, db)
	loader.SetDispatcher(disp)
	loader.SendReady()
	h.sendBadge(client, db)

	h.Clients = append(h.Clients, client)
	h.DBs = append(h.DBs, db)
	h.Loaders = append(h.Loaders, loader)

	if h.Web != nil {
		h.Web.AddLoader(client, loader, db)
	}

	log.Printf("Web login client %d initialized without restart\n", client.TGID)
	return nil
}

func cloneModule(mod Module) Module {
	if mod == nil {
		return nil
	}
	t := reflect.TypeOf(mod)
	if t.Kind() == reflect.Ptr {
		return reflect.New(t.Elem()).Interface().(Module)
	}
	return reflect.New(t).Interface().(Module)
}

func (h *Goroku) sendBadge(client *CustomTelegramClient, db *Database) {
	me, err := client.GetMe()
	if err != nil {
		return
	}
	var name string
	if u, ok := me.(*tg.User); ok {
		if u.FirstName != "" {
			name = u.FirstName
		} else {
			name = u.Username
		}
	} else {
		name = client.Username
	}

	uptime := utils.FormattedUptime()
	platform := utils.GetPlatformName()
	emoji := utils.GetPlatformEmoji()

	msg := fmt.Sprintf(
		"🪐 <b>Goroku Userbot</b> started!\n\n"+
			"👤 <b>Account:</b> %s\n"+
			"🖥 <b>Platform:</b> %s %s\n"+
			"⏱ <b>Uptime:</b> %s\n"+
			"📦 <b>Version:</b> %s",
		name, platform, emoji, uptime, GetVersionString(),
	)

	_, _ = client.SendMessage(client.TGID, msg)
}

func (h *Goroku) registerBuiltInModules(loader *Modules) {
	// Built-in modules registration (will register statically or dynamically later)
}

func getTGIDFromSessionPath(path string) (int64, error) {
	base := filepath.Base(path)
	var idStr string
	switch {
	case strings.HasPrefix(base, "goroku-") && strings.HasSuffix(base, ".session"):
		idStr = strings.TrimSuffix(strings.TrimPrefix(base, "goroku-"), ".session")
	case strings.HasPrefix(base, "heroku-") && strings.HasSuffix(base, ".session"):
		idStr = strings.TrimSuffix(strings.TrimPrefix(base, "heroku-"), ".session")
	case strings.HasPrefix(base, "hikka-") && strings.HasSuffix(base, ".session"):
		idStr = strings.TrimSuffix(strings.TrimPrefix(base, "hikka-"), ".session")
	default:
		return 0, fmt.Errorf("invalid session filename format")
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func GenerateAppName() string {
	latin := []string{
		"Amor", "Arbor", "Astra", "Aurum", "Bellum", "Caelum",
		"Calor", "Candor", "Carpe", "Celer", "Certo", "Cibus",
		"Civis", "Clemens", "Coetus", "Cogito", "Conexus",
	}
	return fmt.Sprintf("%s %s %s", latin[rand.Intn(len(latin))], latin[rand.Intn(len(latin))], latin[rand.Intn(len(latin))])
}

func generateRandomSystemVersion() string {
	systems := []string{
		"Ubuntu 22.04", "Ubuntu 24.04", "Fedora 38",
		"Debian 12 Bookworm", "Arch Linux", "CentOS Stream 9",
		"openSUSE Leap 15.5", "Manjaro 23.0", "Pop!_OS 22.04",
		"Linux Mint 21.2", "Kali Linux 2023.3",
	}
	return systems[rand.Intn(len(systems))]
}

func (h *Goroku) startCliLogin(customModules []Module) {
	client := NewCustomTelegramClient(0)
	client.APIID = h.APIID
	client.APIHash = h.APIHash
	client.SessionPath = filepath.Join(BaseDir, "goroku-0.session")

	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect Telegram client: %v\n", err)
	}

	fmt.Println("\033[0;96mYou can use QR-code to login from another device (your friend's phone, for example).\033[0m")
	userChoice := promptInput("\033[0;96mUse QR code? [y/N]: \033[0m")

	if strings.ToLower(userChoice) != "y" {
		h.cliPhoneLogin(client, customModules)
		return
	}

	fmt.Println("\033[0;96mLoading QR code...\033[0m")
	url, err := client.QRLogin()
	if err != nil {
		log.Fatalf("QR login init failed: %v\n", err)
	}

	printQR := func(qrUrl string) {
		qr := NewQRCode()
		qr.AddData(qrUrl)
		fmt.Print("\033[2J\033[3;1f")
		qr.PrintASCII(true) // invert = true matching Python
		fmt.Println("\033[0;96mScan the QR code above to log in.\033[0m")
		fmt.Println("\033[0;96mPress Ctrl+C to cancel.\033[0m")
	}

	printQR(url)

	// Poll status
	deadline := time.Now().Add(90 * time.Second)
	success := false
	lastRecreate := time.Now()
	for time.Now().Before(deadline) {
		status, err := client.QRLoginStatus()
		if err != nil {
			if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
				PrintBanner("2fa.txt")
				password := promptInput("\033[0;96mEnter 2FA password: \033[0m")
				if err := client.SignIn("", "", password); err != nil {
					log.Fatalf("\033[0;91m2FA Login failed: %v\033[0m\n", err)
				}
				success = true
				break
			}
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "canceled") || strings.Contains(errStr, "closed") || strings.Contains(errStr, "dead") {
				log.Fatalf("Telegram client connection closed: %v\n", err)
			}
		} else if status == "SUCCESS" {
			success = true
			break
		}

		// Recreate QR code after 15 seconds of inactivity
		if time.Since(lastRecreate) >= 15*time.Second {
			newUrl, err := client.QRLogin()
			if err == nil {
				url = newUrl
				printQR(url)
				lastRecreate = time.Now()
			}
		}

		time.Sleep(2 * time.Second)
	}

	if !success {
		log.Fatalf("QR login timeout. Please try again.\n")
	}

	PrintBanner("success.txt")
	fmt.Println("\033[0;92mLogged in successfully!\033[0m")

	tgID := client.TGID
	if tgID == 0 {
		me, err := client.GetMe()
		if err == nil {
			if user, ok := me.(*tg.User); ok {
				tgID = user.ID
				client.TGID = tgID
			}
		}
	}
	if tgID == 0 {
		log.Fatalf("Login failed: authorized Telegram user ID is 0\n")
	}

	db := NewDatabase(tgID)
	redisURI := os.Getenv("REDIS_URL")
	if redisURI == "" {
		if val := GetConfigKey("redis_uri"); val != nil {
			redisURI = fmt.Sprintf("%v", val)
		}
	}
	db.Init(redisURI)
	db.client = client
	client.GorokuDB = db

	loader := NewModules(client, db)
	client.Loader = loader

	inlineMgr := inline.NewInlineManager(client, db, loader)
	client.GorokuInline = inlineMgr

	h.cliSetupBot(client, db)
	h.cliSaveClientSession(client, customModules)
}

func (h *Goroku) cliPhoneLogin(client *CustomTelegramClient, customModules []Module) {
	phone := promptInput("\033[0;96mEnter phone: \033[0m")

	err := client.SendCodeRequest(phone)
	if err != nil {
		log.Fatalf("Failed to send code: %v\n", err)
	}

	fmt.Println("A verification code has been sent to your Telegram app or phone.")
	code := promptInput("Enter verification code: ")

	err = client.SignIn(phone, code, "")
	if err != nil {
		if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
			PrintBanner("2fa.txt")
			password := promptInput("\033[0;96mEnter 2FA password: \033[0m")
			if err := client.SignIn(phone, code, password); err != nil {
				log.Fatalf("\033[0;91m2FA Login failed: %v\033[0m\n", err)
			}
		} else {
			log.Fatalf("Login failed: %v\n", err)
		}
	}

	PrintBanner("success.txt")
	fmt.Println("\033[0;92mLogged in successfully!\033[0m")

	tgID := client.TGID
	if tgID == 0 {
		me, err := client.GetMe()
		if err == nil {
			if user, ok := me.(*tg.User); ok {
				tgID = user.ID
				client.TGID = tgID
			}
		}
	}
	if tgID == 0 {
		log.Fatalf("Login failed: authorized Telegram user ID is 0\n")
	}

	db := NewDatabase(tgID)
	redisURI := os.Getenv("REDIS_URL")
	if redisURI == "" {
		if val := GetConfigKey("redis_uri"); val != nil {
			redisURI = fmt.Sprintf("%v", val)
		}
	}
	db.Init(redisURI)
	db.client = client
	client.GorokuDB = db

	loader := NewModules(client, db)
	client.Loader = loader

	inlineMgr := inline.NewInlineManager(client, db, loader)
	client.GorokuInline = inlineMgr

	h.cliSetupBot(client, db)
	h.cliSaveClientSession(client, customModules)
}

func (h *Goroku) cliSetupBot(client *CustomTelegramClient, db *Database) {
	for {
		bot := promptInput("You can enter a custom bot username or leave it empty and Goroku will generate a random one: ")
		if bot == "" {
			break
		}
		bot = strings.TrimSpace(bot)
		bot = strings.TrimPrefix(bot, "@")

		// Validate username
		invalid := false
		for _, ch := range bot {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
				invalid = true
				break
			}
		}
		if invalid {
			fmt.Println("Invalid username: use only ASCII letters, digits and underscore (_).")
			continue
		}
		if !strings.HasSuffix(strings.ToLower(bot), "bot") {
			fmt.Println("Invalid username: must end with 'bot'.")
			continue
		}

		fmt.Println("Checking bot username...")
		owned, err := client.CheckBot(bot)
		if err == nil && owned {
			db.Set("goroku.inline", "custom_bot", bot)
			fmt.Println("Bot username saved!")
			break
		} else {
			fmt.Println("Bot username is occupied. Try again or leave it empty")
		}
	}
}

func (h *Goroku) cliSaveClientSession(client *CustomTelegramClient, customModules []Module) {
	tgID := client.TGID
	if tgID == 0 {
		log.Fatalf("Login failed: authorized Telegram user ID is 0\n")
	}

	_ = client.Disconnect()
	time.Sleep(500 * time.Millisecond)

	oldSession := filepath.Join(BaseDir, "goroku-0.session")
	newSession := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", tgID))
	_ = os.Rename(oldSession, newSession)
	utils.SecureFile(newSession)

	log.Printf("Booting userbot for client ID: %d...\n", tgID)
	if _, err := h.initClient(tgID, newSession, customModules); err != nil {
		log.Fatalf("Failed to init client %d: %v\n", tgID, err)
	}
}

func promptInput(prompt string) string {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
