package goroku

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// Compile-time: WebCore is the production WebRuntime implementation.
var _ WebRuntime = (*web.WebCore)(nil)

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
	SSHTunnel   bool
	ProxyHost   string
	ProxyPort   int
	ProxySecret string
	ProxyPass   string
	Clients     []*CustomTelegramClient
	DBs         []*Database
	Loaders     []*Modules
	Web         *web.WebCore
	TGLogs      *TelegramLogsHandler

	// ShutdownTimeout bounds how long Run waits for the shared teardown worker.
	// The worker continues safely if external code ignores cancellation.
	ShutdownTimeout time.Duration

	lifecycleMu      sync.Mutex
	shuttingDown     bool
	shutdownStarted  bool
	shutdownDone     chan struct{}
	shutdownErr      error
	start            func(context.Context) error
	requestCh        chan struct{}
	request          lifecycleRequest
	runStarted       bool
	runDone          chan struct{}
	runErr           error
	runCancel        context.CancelFunc
	runContext       context.Context
	startupActive    bool
	startupDone      chan struct{}
	connectClient    func(context.Context, *CustomTelegramClient) error
	ownsGlobal       bool
	lifecycleOps     int
	lifecycleOpsDone chan struct{}
}

type lifecycleRequest uint8

const (
	requestNone lifecycleRequest = iota
	requestStop
	requestRestart
)

// ErrRestartRequested tells the process owner to replace or relaunch Goroku
// after coordinated shutdown has completed.
var ErrRestartRequested = errors.New("goroku restart requested")
var ErrAppAlreadyRunning = errors.New("another Goroku application is already running")

const defaultShutdownTimeout = 30 * time.Second

func NewGoroku() *Goroku {
	opsDone := make(chan struct{})
	close(opsDone)
	return &Goroku{
		Clients:          make([]*CustomTelegramClient, 0),
		DBs:              make([]*Database, 0),
		Loaders:          make([]*Modules, 0),
		shutdownDone:     make(chan struct{}),
		requestCh:        make(chan struct{}),
		runDone:          make(chan struct{}),
		ShutdownTimeout:  defaultShutdownTimeout,
		lifecycleOpsDone: opsDone,
	}
}

// NewApp creates the production application. Startup is performed by Run.
// factories construct a fresh Module per client (no shared/reflected clones).
func NewApp(factories []ModuleFactory) *Goroku {
	h := NewGoroku()
	h.start = func(ctx context.Context) error { return h.startup(ctx, factories) }
	return h
}

// Run starts the application, waits for cancellation or a lifecycle request,
// and performs coordinated shutdown. A Goroku value can be run only once;
// concurrent and repeated callers observe the same result.
func (h *Goroku) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	h.lifecycleMu.Lock()
	if h.runStarted {
		done := h.runDone
		h.lifecycleMu.Unlock()
		runCancel()
		<-done
		h.lifecycleMu.Lock()
		err := h.runErr
		h.lifecycleMu.Unlock()
		return err
	}
	h.runStarted = true
	if h.shutdownStarted {
		h.lifecycleMu.Unlock()
		runCancel()
		return h.completeRun(h.waitForShutdown())
	}
	h.runCancel = runCancel
	h.runContext = runCtx
	h.startupActive = true
	h.startupDone = make(chan struct{})
	start := h.start
	h.lifecycleMu.Unlock()

	if !setActiveApp(h) {
		h.finishStartup()
		runCancel()
		return h.completeRun(ErrAppAlreadyRunning)
	}
	h.lifecycleMu.Lock()
	h.ownsGlobal = true
	h.lifecycleMu.Unlock()

	startResult := make(chan error, 1)
	go func() {
		var err error
		if !h.isShuttingDown() && start != nil {
			err = start(runCtx)
		}
		h.finishStartup()
		startResult <- err
	}()

	var result error
	startupFinished := false
	select {
	case result = <-startResult:
		startupFinished = true
	case <-runCtx.Done():
		result = h.requestResult(runCtx.Err())
	case <-h.requestCh:
		result = h.requestResult(nil)
	}

	if startupFinished {
		result = h.requestResult(result)
	}
	if startupFinished && result == nil {
		select {
		case <-runCtx.Done():
			result = h.requestResult(runCtx.Err())
		case <-h.requestCh:
			result = h.requestResult(nil)
		}
	}
	shutdownErr := h.waitForShutdown()
	if result == nil {
		result = shutdownErr
	} else if shutdownErr != nil {
		result = errors.Join(result, shutdownErr)
	}

	return h.completeRun(result)
}

func (h *Goroku) requestResult(fallback error) error {
	h.lifecycleMu.Lock()
	request := h.request
	h.lifecycleMu.Unlock()
	if request == requestNone {
		return fallback
	}
	if request == requestRestart {
		if fallback != nil && !errors.Is(fallback, context.Canceled) {
			return errors.Join(fallback, ErrRestartRequested)
		}
		return ErrRestartRequested
	}
	if errors.Is(fallback, context.Canceled) {
		return nil
	}
	return fallback
}

func (h *Goroku) finishStartup() {
	h.lifecycleMu.Lock()
	if h.startupActive {
		h.startupActive = false
		close(h.startupDone)
	}
	h.lifecycleMu.Unlock()
}

func (h *Goroku) completeRun(err error) error {
	h.lifecycleMu.Lock()
	h.runCancel = nil
	h.runContext = nil
	h.runErr = err
	select {
	case <-h.runDone:
	default:
		close(h.runDone)
	}
	h.lifecycleMu.Unlock()
	return err
}

func (h *Goroku) waitForShutdown() error {
	timeout := h.ShutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.Shutdown(ctx)
}

func (h *Goroku) isShuttingDown() bool {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	return h.shuttingDown
}

func (h *Goroku) lifecycleContext() context.Context {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	if h.runContext != nil {
		return h.runContext
	}
	return context.Background()
}

func (h *Goroku) beginLifecycleOperation() error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	if h.shuttingDown {
		return context.Canceled
	}
	if h.lifecycleOps == 0 {
		h.lifecycleOpsDone = make(chan struct{})
	}
	h.lifecycleOps++
	return nil
}

func (h *Goroku) endLifecycleOperation() {
	h.lifecycleMu.Lock()
	h.lifecycleOps--
	if h.lifecycleOps == 0 {
		close(h.lifecycleOpsDone)
	}
	h.lifecycleMu.Unlock()
}

// RequestStop asks Run to shut down. The first lifecycle request wins.
func (h *Goroku) RequestStop() bool { return h.requestLifecycle(requestStop) }

// RequestRestart asks Run to shut down and return ErrRestartRequested.
func (h *Goroku) RequestRestart() bool { return h.requestLifecycle(requestRestart) }

func (h *Goroku) requestLifecycle(request lifecycleRequest) bool {
	h.lifecycleMu.Lock()
	if h.request != requestNone || h.shutdownStarted {
		h.lifecycleMu.Unlock()
		return false
	}
	h.request = request
	close(h.requestCh)
	cancel := h.runCancel
	h.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// Shutdown stops all runtime components. It is safe to call concurrently or repeatedly.
func (h *Goroku) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.lifecycleMu.Lock()
	if h.shutdownDone == nil {
		h.shutdownDone = make(chan struct{})
	}
	if !h.shutdownStarted {
		h.shutdownStarted = true
		h.shuttingDown = true
		if h.request == requestNone {
			h.request = requestStop
			close(h.requestCh)
		}
		go h.finishShutdown()
	}
	cancel := h.runCancel
	done := h.shutdownDone
	h.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}

	select {
	case <-done:
		h.lifecycleMu.Lock()
		err := h.shutdownErr
		h.lifecycleMu.Unlock()
		return err
	default:
	}
	select {
	case <-done:
		h.lifecycleMu.Lock()
		err := h.shutdownErr
		h.lifecycleMu.Unlock()
		return err
	case <-ctx.Done():
		h.lifecycleMu.Lock()
		err := h.shutdownErr
		h.lifecycleMu.Unlock()
		return errors.Join(ctx.Err(), err)
	}
}

func (h *Goroku) finishShutdown() {
	h.lifecycleMu.Lock()
	startupDone := h.startupDone
	startupActive := h.startupActive
	h.lifecycleMu.Unlock()
	if startupActive && startupDone != nil {
		<-startupDone
	}
	h.lifecycleMu.Lock()
	operationsDone := h.lifecycleOpsDone
	h.lifecycleMu.Unlock()
	if operationsDone != nil {
		<-operationsDone
	}

	h.lifecycleMu.Lock()
	webCore := h.Web
	clients := append([]*CustomTelegramClient(nil), h.Clients...)
	dbs := append([]*Database(nil), h.DBs...)
	loaders := append([]*Modules(nil), h.Loaders...)
	logHandler := h.TGLogs
	h.lifecycleMu.Unlock()

	var errs []error
	recordError := func(err error) {
		if err == nil {
			return
		}
		errs = append(errs, err)
		h.lifecycleMu.Lock()
		h.shutdownErr = errors.Join(h.shutdownErr, err)
		h.lifecycleMu.Unlock()
	}
	// A fully stopped HTTP server is the application intake boundary. It is
	// closed first so no new auth or runtime work can enter during draining.
	if webCore != nil {
		if err := webCore.Close(context.Background()); err != nil {
			recordError(fmt.Errorf("stop web: %w", err))
		}
		for _, client := range clients {
			if client != nil {
				webCore.UnregisterClient(client.TGIDValue())
			}
		}
	}

	for _, loader := range loaders {
		if loader != nil {
			if dispatcher := loader.GetDispatcher(); dispatcher != nil {
				dispatcher.Stop()
			}
		}
	}

	// Inline callbacks can execute module code and access the database. Drain
	// them before module unload or database close so a caller timeout cannot
	// tear dependencies out from under an active inline worker.
	for _, client := range clients {
		if client == nil {
			continue
		}
		if inlineManager, ok := client.GorokuInline.(interface{ Close(context.Context) error }); ok {
			if err := inlineManager.Close(context.Background()); err != nil {
				recordError(err)
			}
		}
	}
	for _, loader := range loaders {
		if loader == nil {
			continue
		}
		if dispatcher := loader.GetDispatcher(); dispatcher != nil {
			if err := dispatcher.Close(context.Background()); err != nil {
				recordError(err)
			}
		}
		if err := loader.Shutdown(context.Background()); err != nil {
			recordError(err)
		}
	}
	if logHandler != nil {
		if err := logHandler.Close(context.Background()); err != nil {
			recordError(fmt.Errorf("close Telegram log poller: %w", err))
		}
	}
	for _, client := range clients {
		if client == nil {
			continue
		}
		if err := client.Close(context.Background()); err != nil {
			recordError(fmt.Errorf("disconnect Telegram client %d: %w", client.TGIDValue(), err))
		}
	}
	for _, db := range dbs {
		if db != nil {
			if err := db.Close(context.Background()); err != nil {
				recordError(err)
			}
		}
	}
	if err := L().Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTTY) {
		recordError(fmt.Errorf("sync logger: %w", err))
	}
	releaseLogging(logHandler)
	h.lifecycleMu.Lock()
	if len(errs) == 0 {
		h.shutdownErr = nil
	}
	owned := h.ownsGlobal
	h.ownsGlobal = false
	h.lifecycleMu.Unlock()
	if owned {
		clearActiveApp(h)
	}
	close(h.shutdownDone)
}

func readConfigLastValid() (map[string]any, bool, string) {
	path := lastValidPath(ConfigPath)
	content, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, false, ""
	}
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil || data == nil {
		return nil, false, ""
	}
	return data, true, fileGenerationLabel(path, content)
}

func repairConfig(data map[string]any) {
	bytes, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		L().Warn("Config recovery repair marshal failed", zap.String("path", ConfigPath), zap.Error(err))
		return
	}
	if err := writeFileAtomic(ConfigPath, bytes); err != nil && !errors.Is(err, errAtomicWriteCommitted) {
		L().Warn("Config recovery repair write failed", zap.String("path", ConfigPath), zap.Error(err))
		return
	}
	utils.SecureFile(ConfigPath)
}

// loadConfigData loads main config.json, recovering from the durable last-valid
// sibling when the primary is missing or corrupt. A corrupt primary without
// recovery returns an error so callers do not rewrite from an empty map.
func loadConfigData() (map[string]any, error) {
	content, err := os.ReadFile(ConfigPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			if data, ok, label := readConfigLastValid(); ok {
				L().Warn("Config primary missing; recovered from last-valid copy",
					zap.String("path", ConfigPath),
					zap.String("backup", lastValidPath(ConfigPath)),
					zap.String("generation", label))
				repairConfig(data)
				return data, nil
			}
			return make(map[string]any), nil
		}
		// Unreadable primary (permission/IO): attempt last-valid like Database.
		if data, ok, label := readConfigLastValid(); ok {
			L().Warn("Config primary unreadable; recovered from last-valid copy",
				zap.String("path", ConfigPath),
				zap.String("backup", lastValidPath(ConfigPath)),
				zap.String("generation", label),
				zap.Error(err))
			repairConfig(data)
			return data, nil
		}
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		if recovered, ok, label := readConfigLastValid(); ok {
			L().Warn("Config recovered from last-valid copy",
				zap.String("path", ConfigPath),
				zap.String("backup", lastValidPath(ConfigPath)),
				zap.String("generation", label),
				zap.Error(err))
			repairConfig(recovered)
			return recovered, nil
		}
		return nil, err
	}
	if data == nil {
		data = make(map[string]any)
	}
	return data, nil
}

func GetConfigKey(key string) any {
	data, err := loadConfigData()
	if err != nil || data == nil {
		return nil
	}
	return data[key]
}

func SaveConfigKey(key string, value any) bool {
	data, err := loadConfigData()
	if err != nil {
		L().Warn("failed to load config for save; refusing to clobber", zap.Error(err))
		return false
	}
	data[key] = value
	bytes, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return false
	}
	err = writeFileAtomic(ConfigPath, bytes)
	if err != nil && !errors.Is(err, errAtomicWriteCommitted) {
		return false
	}
	utils.SecureFile(ConfigPath)
	return true
}

func randomSetupToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func configWebSetupCompleted() bool {
	switch v := GetConfigKey("web_setup_completed").(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "1" || s == "true" || s == "yes"
	case float64:
		return v != 0
	default:
		return false
	}
}

func hasExistingTelegramSessions(dataRoot string) bool {
	for _, pattern := range []string{
		filepath.Join(dataRoot, "goroku-*.session"),
		filepath.Join(dataRoot, "heroku-*.session"),
		filepath.Join(dataRoot, "hikka-*.session"),
	} {
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			base := filepath.Base(f)
			if base != "goroku-0.session" && base != "hikka-0.session" {
				return true
			}
		}
	}
	return false
}

func (h *Goroku) ParseArguments() {
	_ = flag.Bool("root", false, "Allow running as root (handled by main.go)")
	portFlag := flag.Int("port", 8080, "Port for web panel")
	noWebFlag := flag.Bool("no-web", false, "Disable web setup dashboard")
	noGitFlag := flag.Bool("no-git", false, "Disable git operations")
	qrLoginFlag := flag.Bool("qr-login", false, "CLI login: use QR code (skip y/N prompt; requires --no-web)")
	noAuthFlag := flag.Bool("no-auth", false, "Skip interactive CLI auth when no sessions exist (requires --no-web)")
	sandboxFlag := flag.Bool("sandbox", false, "Sandbox mode: disable process restarts")
	sshTunnelFlag := flag.Bool("ssh-tunnel", false, "Expose the web panel through an SSH reverse tunnel (not MTProto proxy)")
	dataRootFlag := flag.String("data-root", "", "Custom path to data directory")
	proxyHostFlag := flag.String("proxy-host", "", "MTProto proxy host (requires --proxy-port and --proxy-secret)")
	proxyPortFlag := flag.Int("proxy-port", 0, "MTProto proxy port")
	proxySecretFlag := flag.String("proxy-secret", "", "MTProto proxy secret (hex)")
	proxyPassFlag := flag.String("proxy-pass", "", "Deprecated alias for --proxy-secret (not SSH tunnel)")
	flag.Parse()

	h.Port = *portFlag
	h.DisableWeb = *noWebFlag
	h.NoGit = *noGitFlag
	h.QRLogin = *qrLoginFlag
	h.NoAuth = *noAuthFlag
	h.Sandbox = *sandboxFlag
	h.SSHTunnel = *sshTunnelFlag
	h.ProxyHost = strings.TrimSpace(*proxyHostFlag)
	h.ProxyPort = *proxyPortFlag
	h.ProxySecret = NormalizeProxySecret(*proxySecretFlag, *proxyPassFlag)
	h.ProxyPass = strings.TrimSpace(*proxyPassFlag)

	if *dataRootFlag != "" {
		BaseDir = *dataRootFlag
		ConfigPath = filepath.Join(BaseDir, "config.json")
	}

	if h.NoGit {
		_ = os.Setenv("GOROKU_NO_GIT", "1")
	}
}

func (h *Goroku) applyProxyConfig() error {
	return ConfigureMTProxy(MTProxyConfig{
		Host:   h.ProxyHost,
		Port:   h.ProxyPort,
		Secret: h.ProxySecret,
	})
}

func (h *Goroku) startup(ctx context.Context, factories []ModuleFactory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.ParseArguments()
	if err := h.applyProxyConfig(); err != nil {
		return fmt.Errorf("invalid MTProto proxy flags: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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

	h.lifecycleMu.Lock()
	if h.shuttingDown {
		h.lifecycleMu.Unlock()
		return context.Canceled
	}
	h.TGLogs = InitLogging()
	h.lifecycleMu.Unlock()

	zeroSession := filepath.Join(BaseDir, "goroku-0.session")
	if _, err := os.Stat(zeroSession); err == nil && h.APIID != 0 && h.APIHash != "" {
		L().Info("Found pending goroku-0.session, resolving real TGID...")
		client := NewCustomTelegramClient(0)
		client.APIID = h.APIID
		client.APIHash = h.APIHash
		client.SessionPath = zeroSession
		if err := client.ConnectContext(ctx); err == nil {
			realID := client.TGID
			_ = client.Disconnect()
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
			newPath := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", realID))
			_ = os.Rename(zeroSession, newPath)
			utils.SecureFile(newPath)
			L().Info("Renamed goroku-0.session", zap.Int64("real_id", realID))
		} else {
			L().Error("Failed to connect with goroku-0.session", zap.Error(err))
		}
	}

	if !h.DisableWeb {
		hasExistingSessions := hasExistingTelegramSessions(BaseDir)
		setupCompleted := hasExistingSessions || web.SetupCompleted(BaseDir) || configWebSetupCompleted()
		setupToken := ""
		if !setupCompleted {
			setupToken = strings.TrimSpace(os.Getenv("GOROKU_SETUP_TOKEN"))
			if setupToken == "" {
				minted, err := randomSetupToken()
				if err != nil {
					return fmt.Errorf("mint web setup token: %w", err)
				}
				setupToken = minted
				_ = os.Setenv("GOROKU_SETUP_TOKEN", setupToken)
			}
		} else {
			// Onboarding already done: never re-arm setup from a leftover env token.
			_ = os.Unsetenv("GOROKU_SETUP_TOKEN")
		}
		apiToken := ""
		if h.APIID != 0 && h.APIHash != "" {
			apiToken = h.APIHash
		}
		webCore := web.NewWebCore(web.WebConfig{
			ApiToken:   apiToken,
			SetupToken: setupToken,
			DataRoot:   BaseDir,
			SaveConfig: SaveConfigKey,
			Restart:    func() { h.RequestRestart() },
			OnLogin: func(client webiface.TelegramClient) error {
				return h.finishWebLogin(client, factories)
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
		h.lifecycleMu.Lock()
		if h.shuttingDown {
			h.lifecycleMu.Unlock()
			return context.Canceled
		}
		h.Web = webCore
		h.lifecycleMu.Unlock()
		webCore.SetPort(h.Port)
		if err := webCore.StartContext(ctx, h.Port, h.SSHTunnel); err != nil {
			return fmt.Errorf("start web server: %w", err)
		}
		setupURL := webCore.GetURL(h.SSHTunnel)
		logURL := setupURL
		if setupToken != "" && !setupCompleted {
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
		L().Info("Web mode ready", zap.String("url", logURL), zap.Bool("setup_token_required", setupToken != "" && !setupCompleted))
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
			if err := h.startCliLogin(ctx, factories); err != nil {
				return err
			}
		} else {
			L().Info("No active sessions found, please use the Web dashboard to log in")
		}
	} else {
		for _, sessionFile := range activeSessions {
			if err := ctx.Err(); err != nil {
				return err
			}
			tgID, err := getTGIDFromSessionPath(sessionFile)
			if err != nil {
				L().Warn("Skip invalid session file", zap.String("file", sessionFile), zap.Error(err))
				continue
			}
			L().Info("Booting userbot", zap.Int64("tg_id", tgID))
			client, err := h.initClientContext(ctx, tgID, sessionFile, factories)
			if err != nil {
				L().Error("Failed to init client", zap.Int64("tg_id", tgID), zap.Error(err))
				if strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
					HandleAuthKeyUnregistered(tgID, sessionFile)
				}
				continue
			}
			if h.Web != nil {
				if err := h.registerWebRuntime(client); err != nil {
					L().Error("Failed to register web runtime", zap.Int64("tg_id", client.TGID), zap.Error(err))
				}
			}
		}
	}
	return ctx.Err()
}

// Main is retained for source compatibility. New programs should use
// NewApp(factories).Run(ctx); signal policy belongs to the caller.
// Deprecated: use NewApp and Run.
func Main(factories []ModuleFactory) {
	if err := NewApp(factories).Run(context.Background()); err != nil {
		L().Error("Goroku stopped with an error", zap.Error(err))
	}
}
