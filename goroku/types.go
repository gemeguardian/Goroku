package goroku

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"goroku/goroku/cache"
	"goroku/goroku/chatref"
	"goroku/goroku/inlineiface"
	"goroku/goroku/logger"
	"goroku/goroku/webiface"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

var _ = logger.L

// Compile-time consumer-port assertions (M7.2).
var (
	_ webiface.TelegramClient = (*CustomTelegramClient)(nil)
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
	ctx          context.Context
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
	APIID                  int64
	APIHash                string
	TGID                   int64
	Username               string
	parseMode              string
	cacheMu                sync.RWMutex
	GorokuEntityCache      map[cache.EntityCacheKey]cache.CacheRecordEntity
	GorokuPermsCache       map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms
	GorokuFullChannelCache map[cache.EntityCacheKey]cache.CacheRecordFullChannel
	GorokuFullUserCache    map[cache.EntityCacheKey]cache.CacheRecordFullUser
	ForbiddenConstructors  []uint32
	GorokuMe               *tg.User
	GorokuDB               *Database
	Loader                 *Modules
	GorokuInline           inlineiface.InlineManager // assigned by goroku package via *inline.InlineManager
	Web                    WebRuntime
	phoneCodeHash          string
	qrLoginSignal          <-chan struct{}
	SessionPath            string
	client                 *telegram.Client
	rawAPI                 *tg.Client
	ctx                    context.Context
	cancel                 context.CancelFunc

	RatelimitMu        sync.Mutex
	Ratelimiter        []RateLimitRecord
	SuspendUntil       time.Time
	BypassSuspendUntil time.Time
	FloodWaitLock      bool

	runDone chan struct{}
	runMu   sync.Mutex
	runErr  error
}

// TGIDValue returns the Telegram user ID associated with the client.
func (c *CustomTelegramClient) TGIDValue() int64 {
	return c.TGID
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
	c.TGID = id
}

type RateLimitRecord struct {
	Name string
	TS   time.Time
}

func NewCustomTelegramClient(tgID int64) *CustomTelegramClient {
	return &CustomTelegramClient{
		TGID:                   tgID,
		GorokuEntityCache:      make(map[cache.EntityCacheKey]cache.CacheRecordEntity),
		GorokuPermsCache:       make(map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms),
		GorokuFullChannelCache: make(map[cache.EntityCacheKey]cache.CacheRecordFullChannel),
		GorokuFullUserCache:    make(map[cache.EntityCacheKey]cache.CacheRecordFullUser),
		ForbiddenConstructors:  make([]uint32, 0),
	}
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
		L().Warn("Client stop failed", zap.Int64("tg_id", c.TGID), zap.Error(err))
		return
	}
	L().Info("Client stopped gracefully", zap.Int64("tg_id", c.TGID))
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
