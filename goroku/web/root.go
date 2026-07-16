package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"goroku/goroku/logger"
	"goroku/goroku/webiface"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
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

// MTProtoConnection is reserved for future web-driven transport wiring.
// No concrete connection type is wired into the web runtime yet (M7 residual).
type MTProtoConnection struct{}

// MTProtoProxy is reserved for future web-driven MTProto proxy wiring.
// No concrete proxy type is wired into the web runtime yet (M7 residual).
type MTProtoProxy struct{}

type WebConfig struct {
	ApiToken   string
	SetupToken string
	DataRoot   string
	Connection *MTProtoConnection // reserved; currently unused by the web runtime
	Proxy      *MTProtoProxy      // reserved; currently unused by the web runtime
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

// SessionService owns browser sessions, CSRF tokens, and setup-token exchange.
type SessionService struct {
	sessions   map[string]WebSession
	setupToken string
}

// RuntimeRegistry owns typed Telegram runtimes exposed to the web UI.
type RuntimeRegistry struct {
	clientData     map[int64]RuntimeClient
	nextGeneration uint64
}

// qrLoginState is the typed QR-login progress for the web coordinator.
// Idle: URL empty and Done false. Pending: URL set. Complete: Done true.
type qrLoginState struct {
	URL  string
	Done bool
}

// LoginCoordinator owns Telegram phone/QR login and pending web-auth challenges.
type LoginCoordinator struct {
	signInClients map[string]webiface.TelegramClient
	pendingClient webiface.TelegramClient
	qrLogin       qrLoginState
	qrTaskActive  bool
	twoFANeeded   bool
	pendingAuths  map[string]*PendingAuth
	authAccepting bool
}

// StaticUI resolves template/static resource roots for web delivery.
type StaticUI struct {
	dataRoot string
}

// Web is the public façade composing session, registry, login, and UI services.
type Web struct {
	mu sync.RWMutex
	SessionService
	RuntimeRegistry
	LoginCoordinator
	StaticUI
	ratelimit      map[string][]int64
	apiToken       string
	connection     *MTProtoConnection
	proxy          *MTProtoProxy
	saveConfig     func(key string, value any) bool
	restart        func()
	onLogin        func(client webiface.TelegramClient) error
	apiSetChan     chan struct{}
	clientsSetChan chan struct{}
	getClient      func() webiface.TelegramClient
}

// RuntimeClient is one fully initialized client available to the web runtime.
type RuntimeClient struct {
	ID         int64
	Client     webiface.TelegramClient
	Loader     webiface.ModulesRegistry // nil allowed; web UI does not call yet
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
		SessionService: SessionService{
			sessions:   make(map[string]WebSession),
			setupToken: setupToken,
		},
		RuntimeRegistry: RuntimeRegistry{
			clientData: make(map[int64]RuntimeClient),
		},
		LoginCoordinator: LoginCoordinator{
			signInClients: make(map[string]webiface.TelegramClient),
			pendingAuths:  make(map[string]*PendingAuth),
			authAccepting: true,
		},
		StaticUI: StaticUI{
			dataRoot: cfg.DataRoot,
		},
		ratelimit:      make(map[string][]int64),
		apiToken:       cfg.ApiToken,
		connection:     cfg.Connection,
		proxy:          cfg.Proxy,
		saveConfig:     cfg.SaveConfig,
		restart:        cfg.Restart,
		onLogin:        cfg.OnLogin,
		apiSetChan:     make(chan struct{}),
		clientsSetChan: make(chan struct{}),
		getClient:      cfg.GetClient,
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
