package goroku

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"goroku/goroku/cache"
	"goroku/goroku/chatref"
	"goroku/goroku/inline"
	"goroku/goroku/inlineiface"
	"goroku/goroku/logger"
	"goroku/goroku/webiface"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

var _ = logger.L

// Compile-time consumer-port assertions (M7.2). They guarantee that
// *CustomTelegramClient satisfies the web and inline ports and that the send
// result wrapper implements the typed chatref.SentMessage interface.
var (
	_ webiface.TelegramClient  = (*CustomTelegramClient)(nil)
	_ webiface.Database        = (*Database)(nil)
	_ webiface.ModulesRegistry = (*Modules)(nil)
	_ inline.InlineUserBot     = (*CustomTelegramClient)(nil)
	_ chatref.SentMessage      = sentMessage{}
)

type Message struct {
	ID           int64
	ChatID       int64
	SenderID     int64
	Text         string
	RawText      string
	Entities     []tg.MessageEntityClass
	Out          bool
	Media        tg.MessageMediaClass
	IsPrivate    bool
	IsChannel    bool
	IsGroup      bool
	Client       *CustomTelegramClient
	GrepQuery    string
	GrepInvert   bool
	CutLines     int
	SplitOutput  bool
	ReplyToMsgID int64
	FwdFrom      tg.MessageFwdHeader
	IsForwarded  bool
	Answered     bool
	ViaBotID     int64
	// SenderChannelID identifies the channel a message was sent as, for
	// anonymous admins and channel-signed posts. Telegram gives those a
	// *tg.PeerChannel FromID and no user, so SenderID is 0 for them; policies
	// that want to name such a sender must use this field.
	SenderChannelID int64
	SenderIsChannel bool
	ctx             context.Context
}

// Context is canceled when dispatcher shutdown begins. Existing handlers
// remain source-compatible and may opt into cancellation gradually.
func (m *Message) Context() context.Context {
	if m != nil {
		if m.ctx != nil {
			return m.ctx
		}
		if m.Client != nil {
			return m.Client.rpcContext(nil)
		}
	}
	return context.Background()
}

func (m *Message) GetChatID() int64 {
	if m == nil {
		return 0
	}
	return m.ChatID
}

func (m *Message) GetID() int64 {
	if m == nil {
		return 0
	}
	return m.ID
}

type CommandHandler func(msg *Message) error
type WatcherHandler func(msg *Message) error

type RegisteredWatcher struct {
	Handler    WatcherHandler
	ModuleName string
	Meta       CommandMeta
	// regex is compiled once at registration from Meta.Regex.
	regex    *regexp.Regexp
	ownerKey string
	lease    *moduleLease
}

type Module interface {
	Name() string
	Strings() map[string]string
	Init(client *CustomTelegramClient, db *Database) error
	ClientReady() error
	OnUnload() error
	OnDlmod() error
	Commands() map[string]CommandHandler
	Watchers() []WatcherHandler
}

// ModuleFactory constructs a fresh Module instance for one client.
// Factories replace reflect-based cloning so each client owns independent state.
type ModuleFactory func() Module

type CommandMeta struct {
	OnlyPM       bool
	OnlyChats    bool
	OnlyGroups   bool
	OnlyChannels bool
	OnlyOwner    bool
	NoForwarded  bool
	NoStickers   bool
	NoAudio      bool
	NoDoc        bool
	NoMedia      bool
	OnlyMedia    bool
	OnlyPhotos   bool
	OnlyVideos   bool
	OnlyAudios   bool
	OnlyDocs     bool
	OnlyStickers bool
	Editable     bool
	Mention      bool
	NoMention    bool
	NoCommands   bool
	OnlyCommands bool
	OnlyInline   bool
	NoInline     bool
	NoPM         bool
	NoReply      bool
	OnlyReply    bool
	Regex        string
	StartsWith   string
	EndsWith     string
	Contains     string
	FromID       []int64
	ChatID       []int64
	Ratelimit    bool
	Alias        string
	Aliases      []string
	Filter       func(*Message) bool
}

type ModuleWithMeta interface {
	CommandMetas() map[string]CommandMeta
}

type ModuleWithConfig interface {
	ConfigDefaults() map[string]any
}

type ModuleWithConfigReady interface {
	ConfigReady(config map[string]any) error
}

type ModuleWithConfigValidators interface {
	ConfigValidators() map[string]Validator
}

type ModuleWithAllModules interface {
	SetAllModules(*Modules)
}

type ModuleWithTranslator interface {
	SetTranslator(*Translator)
}

type ModuleWithWatcherMetas interface {
	WatcherMetas() []CommandMeta
}

// WebRuntime is the narrow web control surface modules use (no package global).
type WebRuntime interface {
	Port() int
	Stop()
	StartAsync(port int, proxyPass bool)
	GetURL(proxyPass bool) string
	ApproveWebAuth(token string) bool
}

type CustomTelegramClient struct {
	APIID   int64
	APIHash string
	// identityMu guards the account identity below. It is filled by the
	// client.Run goroutine once the session authorizes, while the web login
	// coordinator, the security manager and every module read it — the fields
	// were plain and raced on every login.
	identityMu             sync.RWMutex
	tgID                   int64
	username               string
	gorokuMe               *tg.User
	parseMode              string
	cacheMu                sync.RWMutex
	GorokuEntityCache      map[cache.EntityCacheKey]cache.CacheRecordEntity
	GorokuPermsCache       map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms
	GorokuFullChannelCache map[cache.EntityCacheKey]cache.CacheRecordFullChannel
	GorokuFullUserCache    map[cache.EntityCacheKey]cache.CacheRecordFullUser
	// forbiddenConstructors is read on every outgoing RPC and rewritten by the
	// config reload goroutine. It is an atomic pointer to an immutable slice:
	// readers take a snapshot, writers publish a fresh slice. A plain field
	// let a reader observe a torn slice header and skip the check entirely.
	forbiddenConstructors atomic.Pointer[[]uint32]
	GorokuDB              *Database
	Loader                *Modules
	GorokuInline          inlineiface.InlineManager // assigned by goroku package via *inline.InlineManager
	Web                   WebRuntime
	phoneCodeHash         string
	qrLoginSignal         <-chan struct{}
	SessionPath           string
	client                *telegram.Client
	rawAPI                *tg.Client
	ctx                   context.Context
	cancel                context.CancelFunc

	RatelimitMu        sync.Mutex
	Ratelimiter        []RateLimitRecord
	SuspendUntil       time.Time
	BypassSuspendUntil time.Time
	FloodWaitLock      bool

	runDone chan struct{}
	runMu   sync.Mutex
	runErr  error

	// connState is the observable transport state (see ConnectionState). It is
	// atomic because the readiness probe reads it from an HTTP handler while
	// the run goroutine writes it.
	connState atomic.Int32
}

// TGIDValue returns the Telegram user ID associated with the client.
func (c *CustomTelegramClient) TGIDValue() int64 {
	if c == nil {
		return 0
	}
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.tgID
}

// Username returns the account username, empty until the session authorizes.
func (c *CustomTelegramClient) Username() string {
	if c == nil {
		return ""
	}
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.username
}

// Me returns the authorized account, nil until the session authorizes.
func (c *CustomTelegramClient) Me() *tg.User {
	if c == nil {
		return nil
	}
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.gorokuMe
}

// Identity returns the account identity as one consistent snapshot. Calling
// TGIDValue, Username and Me separately takes three snapshots, which a
// concurrent SetIdentity can fall between.
func (c *CustomTelegramClient) Identity() (int64, string, *tg.User) {
	if c == nil {
		return 0, "", nil
	}
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.tgID, c.username, c.gorokuMe
}

// SetIdentity publishes the account identity learned from an authorized
// session. All three fields move together: a reader must never see a new TGID
// beside a stale Me.
func (c *CustomTelegramClient) SetIdentity(id int64, username string, me *tg.User) {
	if c == nil {
		return
	}
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.tgID = id
	c.username = username
	c.gorokuMe = me
}

// SetUsername overrides the cached username.
func (c *CustomTelegramClient) SetUsername(username string) {
	if c == nil {
		return
	}
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.username = username
}

// SetMe overrides the cached account.
func (c *CustomTelegramClient) SetMe(me *tg.User) {
	if c == nil {
		return
	}
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.gorokuMe = me
}

// API returns the live gotd Telegram API client for trusted modules.
// Modules can use any generated MTProto method without a Goroku-side method
// allowlist; callers remain responsible for peer resolution and permissions.
func (c *CustomTelegramClient) API() *tg.Client {
	if c == nil {
		return nil
	}
	return c.rawAPI
}

func (c *CustomTelegramClient) InlineProvider() webiface.InlineProvider {
	provider, ok := c.GorokuInline.(webiface.InlineProvider)
	if !ok {
		return nil
	}
	return provider
}

// SetTGID sets the Telegram user ID associated with the client.
func (c *CustomTelegramClient) SetTGID(id int64) {
	if c == nil {
		return
	}
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.tgID = id
}

type RateLimitRecord struct {
	Name string
	TS   time.Time
}

func NewCustomTelegramClient(tgID int64) *CustomTelegramClient {
	client := &CustomTelegramClient{
		tgID:                   tgID,
		GorokuEntityCache:      make(map[cache.EntityCacheKey]cache.CacheRecordEntity),
		GorokuPermsCache:       make(map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms),
		GorokuFullChannelCache: make(map[cache.EntityCacheKey]cache.CacheRecordFullChannel),
		GorokuFullUserCache:    make(map[cache.EntityCacheKey]cache.CacheRecordFullUser),
	}
	client.SetForbiddenConstructors(nil)
	return client
}

var (
	ErrClientNotInitialized = errors.New("client not initialized")
	ErrNoReply              = errors.New("message has no reply")
	ErrNotFound             = errors.New("not found")
)

func (c *CustomTelegramClient) GetMe() (any, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	return c.client.Self(c.ctx)
}

func (c *CustomTelegramClient) Disconnect() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Close(ctx)
}

// Close cancels client.Run and waits without creating a wrapper goroutine.
func (c *CustomTelegramClient) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.runMu.Lock()
	cancel := c.cancel
	done := c.runDone
	c.runMu.Unlock()
	c.setConnectionState(ConnectionDisconnected)
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		c.runMu.Lock()
		err := c.runErr
		c.runMu.Unlock()
		return err
	default:
	}
	select {
	case <-done:
		c.runMu.Lock()
		err := c.runErr
		c.runMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GracefulStop preserves the historical logging wrapper.
func (c *CustomTelegramClient) GracefulStop(ctx context.Context) {
	if err := c.Close(ctx); err != nil {
		L().Warn("Client stop failed", zap.Int64("tg_id", c.TGIDValue()), zap.Error(err))
		return
	}
	L().Info("Client stopped gracefully", zap.Int64("tg_id", c.TGIDValue()))
}

// AnimateMessage cycles through frames in a Telegram message.
// It edits the message with each frame, waiting interval between frames.
func AnimateMessage(msg *Message, frames []string, interval time.Duration) error {
	for _, frame := range frames {
		if err := msg.Answer(frame); err != nil {
			return err
		}
		time.Sleep(interval)
	}
	return nil
}

// InvokeCommand programmatically dispatches a command.
// modules is the *Modules registry, cmdName is the command (without prefix).
func InvokeCommand(modules *Modules, msg *Message, cmdName string, args string) error {
	handler, exists := modules.Dispatch(cmdName)
	if !exists {
		return fmt.Errorf("command %s not found", cmdName)
	}
	// Clone message with new text
	newMsg := *msg
	if args != "" {
		newMsg.Text = cmdName + " " + args
		newMsg.RawText = cmdName + " " + args
	} else {
		newMsg.Text = cmdName
		newMsg.RawText = cmdName
	}
	return handler(&newMsg)
}

func (msg *Message) GetReplyMessage() (*Message, error) {
	if msg.Client == nil {
		return nil, ErrClientNotInitialized
	}
	if msg.ReplyToMsgID == 0 {
		return nil, ErrNoReply
	}
	return msg.Client.GetMessageContext(msg.Context(), ChatRefID(msg.ChatID), msg.ReplyToMsgID)
}

type MsgOption func(req any)

// ChatRef is a re-export of the chatref package type for convenience.
type ChatRef = chatref.ChatRef

// EntityRef / UserRef / ChannelRef re-export chatref aliases for cache callers.
type EntityRef = chatref.EntityRef
type UserRef = chatref.UserRef
type ChannelRef = chatref.ChannelRef

// SentMessage re-exports the chatref send-result interface used as the typed
// return of CustomTelegramClient.SendMessage and the webiface/inline ports.
type SentMessage = chatref.SentMessage

// ChatRefID builds a reference from a numeric chat/user/channel ID.
func ChatRefID(id int64) ChatRef { return chatref.ID(id) }

// ChatRefUsername builds a reference from a username (with or without @).
func ChatRefUsername(username string) ChatRef { return chatref.Username(username) }

// ChatRefPeer builds a reference from an already-resolved Telegram peer.
func ChatRefPeer(peer tg.InputPeerClass) ChatRef { return chatref.Peer(peer) }

func (m *Message) Reply(text string, opts ...MsgOption) error {
	if m.Client == nil {
		return fmt.Errorf("no client attached")
	}
	_, err := m.Client.SendMessageWithOptionsContext(m.Context(), ChatRefID(m.ChatID), text, opts...)
	return err
}

func (m *Message) Edit(text string, opts ...MsgOption) error {
	if m.Client == nil {
		return fmt.Errorf("no client attached")
	}
	_, err := m.Client.EditMessageContext(m.Context(), ChatRefID(m.ChatID), m.ID, text, opts...)
	return err
}

func (m *Message) Delete() error {
	if m.Client == nil {
		return fmt.Errorf("no client attached")
	}
	return m.Client.DeleteMessageContext(m.Context(), ChatRefID(m.ChatID), m.ID)
}

func (m *Message) IsOut() bool {
	if m == nil {
		return false
	}
	return m.Out
}

func (m *Message) GetReplyToMsgID() int64 {
	if m == nil {
		return 0
	}
	return m.ReplyToMsgID
}
