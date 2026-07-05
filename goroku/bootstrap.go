package goroku

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"goroku/goroku/utils"
	"goroku/goroku/web"
	"goroku/goroku/webiface"

	"go.uber.org/zap"
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

func GetConfigKey(key string) any {
	content, err := os.ReadFile(ConfigPath) //nolint:gosec
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return nil
	}
	return data[key]
}

func SaveConfigKey(key string, value any) bool {
	var data map[string]any
	content, err := os.ReadFile(ConfigPath) //nolint:gosec
	if err == nil {
		if err := json.Unmarshal(content, &data); err != nil {
			L().Warn("failed to unmarshal config", zap.Error(err))
		}
	}
	if data == nil {
		data = make(map[string]any)
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
	if _, err := cryptoRand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (h *Goroku) ParseArguments() {
	_ = flag.Bool("root", false, "Allow running as root (handled by main.go)")
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

	if *dataRootFlag != "" {
		BaseDir = *dataRootFlag
		BasePath = BaseDir
		ConfigPath = filepath.Join(BaseDir, "config.json")
	}

	if h.NoGit {
		_ = os.Setenv("GOROKU_NO_GIT", "1")
	}
}

func Main(customModules []Module) {
	h := NewGoroku()
	h.ParseArguments()
	utils.SecureFile(ConfigPath)

	fmt.Println("🪐 Starting Goroku Go Userbot...")

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

	zeroSession := filepath.Join(BaseDir, "goroku-0.session")
	if _, err := os.Stat(zeroSession); err == nil && h.APIID != 0 && h.APIHash != "" {
		L().Info("Found pending goroku-0.session, resolving real TGID...")
		client := NewCustomTelegramClient(0)
		client.APIID = h.APIID
		client.APIHash = h.APIHash
		client.SessionPath = zeroSession
		if err := client.Connect(); err == nil {
			realID := client.TGID
			_ = client.Disconnect()
			time.Sleep(500 * time.Millisecond)
			newPath := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", realID))
			_ = os.Rename(zeroSession, newPath)
			utils.SecureFile(newPath)
			L().Info("Renamed goroku-0.session", zap.Int64("real_id", realID))
		} else {
			L().Error("Failed to connect with goroku-0.session", zap.Error(err))
		}
	}

	if !h.DisableWeb {
		setupToken := strings.TrimSpace(os.Getenv("GOROKU_SETUP_TOKEN"))
		if setupToken == "" {
			setupToken = randomSetupToken()
			_ = os.Setenv("GOROKU_SETUP_TOKEN", setupToken)
		}
		apiToken := ""
		if h.APIID != 0 && h.APIHash != "" {
			apiToken = h.APIHash
		}
		h.Web = web.NewWebCore(web.WebConfig{
			ApiToken:   apiToken,
			SetupToken: setupToken,
			DataRoot:   BaseDir,
			SaveConfig: SaveConfigKey,
			Restart:    Restart,
			OnLogin: func(client webiface.TelegramClient) error {
				return h.finishWebLogin(client, customModules)
			},
			GetClient: func() webiface.TelegramClient {
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
		logURL := setupURL
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
			setupURLPath := filepath.Join(BaseDir, "goroku-setup-url.txt")
			if err := os.WriteFile(setupURLPath, []byte(setupURL+"\n"), 0600); err == nil {
				utils.SecureFile(setupURLPath)
				L().Info("Initial setup URL saved", zap.String("path", setupURLPath))
			} else {
				L().Warn("Failed to save initial setup URL", zap.Error(err))
			}
		}
		L().Info("Web mode ready", zap.String("url", logURL), zap.Bool("setup_token_required", !hasExistingSessions))
	}

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
			L().Info("No active sessions found, please use the Web dashboard to log in")
		}
	} else {
		for _, sessionFile := range activeSessions {
			tgID, err := getTGIDFromSessionPath(sessionFile)
			if err != nil {
				L().Warn("Skip invalid session file", zap.String("file", sessionFile), zap.Error(err))
				continue
			}
			L().Info("Booting userbot", zap.Int64("tg_id", tgID))
			client, err := h.initClient(tgID, sessionFile, customModules)
			if err != nil {
				L().Error("Failed to init client", zap.Int64("tg_id", tgID), zap.Error(err))
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

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		sig := <-sigCh
		L().Info("Received signal, initiating graceful shutdown", zap.String("signal", sig.String()))

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Stop all clients gracefully
		for _, client := range h.Clients {
			if client != nil {
				client.GracefulStop(shutdownCtx)
			}
		}

		// Flush logs
		_ = L().Sync()

		os.Exit(0)
	}()

	select {}
}
