package goroku

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type Message struct {
	ID           int64
	ChatID       int64
	SenderID     int64
	Text         string
	RawText      string
	Entities     []tg.MessageEntityClass
	Out          bool
	Media        interface{}
	IsPrivate    bool
	IsChannel    bool
	IsGroup      bool
	Client       *CustomTelegramClient
	GrepQuery    string
	GrepInvert   bool
	CutLines     int
	SplitOutput  bool
	ReplyToMsgID int64
	FwdFrom      interface{}
	IsForwarded  bool
	Answered     bool
	ViaBotID     int64
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
	ConfigDefaults() map[string]interface{}
}

type ModuleWithConfigReady interface {
	ConfigReady(config map[string]interface{}) error
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

type CustomTelegramClient struct {
	APIID                  int64
	APIHash                string
	TGID                   int64
	Username               string
	parseMode              string
	entityCache            map[string]interface{}
	permsCache             map[string]interface{}
	cacheMu                sync.RWMutex
	GorokuEntityCache      map[interface{}]CacheRecordEntity
	GorokuPermsCache       map[interface{}]map[interface{}]CacheRecordPerms
	GorokuFullChannelCache map[interface{}]CacheRecordFullChannel
	GorokuFullUserCache    map[interface{}]CacheRecordFullUser
	ForbiddenConstructors  []uint32
	GorokuMe               interface{}
	GorokuDB               interface{}
	Loader                 interface{}
	GorokuInline           interface{}
	phoneCodeHash          string
	qrLoginSignal          <-chan struct{}
	readyCh                chan struct{}
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
}

type RateLimitRecord struct {
	Name string
	TS   time.Time
}

func NewCustomTelegramClient(tgID int64) *CustomTelegramClient {
	return &CustomTelegramClient{
		TGID:                   tgID,
		entityCache:            make(map[string]interface{}),
		permsCache:             make(map[string]interface{}),
		GorokuEntityCache:      make(map[interface{}]CacheRecordEntity),
		GorokuPermsCache:       make(map[interface{}]map[interface{}]CacheRecordPerms),
		GorokuFullChannelCache: make(map[interface{}]CacheRecordFullChannel),
		GorokuFullUserCache:    make(map[interface{}]CacheRecordFullUser),
		ForbiddenConstructors:  make([]uint32, 0),
	}
}

func (c *CustomTelegramClient) GetMe() (interface{}, error) {
	if c.rawAPI == nil {
		return nil, nil
	}
	return c.client.Self(c.ctx)
}

func (c *CustomTelegramClient) Disconnect() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

type JSONSerializable interface{}

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
	if msg.Client == nil || msg.ReplyToMsgID == 0 {
		return nil, nil
	}
	return msg.Client.GetMessage(msg.ChatID, msg.ReplyToMsgID)
}

type MsgOption func(req interface{})

func (m *Message) Reply(text string, opts ...MsgOption) error {
	if m.Client == nil {
		return fmt.Errorf("no client attached")
	}
	_, err := m.Client.SendMessageWithOptions(m.ChatID, text, opts...)
	return err
}

func (m *Message) Edit(text string, opts ...MsgOption) error {
	if m.Client == nil {
		return fmt.Errorf("no client attached")
	}
	_, err := m.Client.EditMessage(m.ChatID, m.ID, text, opts...)
	return err
}

func (m *Message) Delete() error {
	if m.Client == nil || m.Client.rawAPI == nil {
		return fmt.Errorf("no client attached")
	}
	peer, err := m.Client.ResolvePeer(m.ChatID)
	if err != nil {
		return err
	}
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		_, err = m.Client.rawAPI.ChannelsDeleteMessages(m.Client.ctx,
			&tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{
					ChannelID:  ch.ChannelID,
					AccessHash: ch.AccessHash,
				},
				ID: []int{int(m.ID)},
			})
		return err
	}
	_, err = m.Client.rawAPI.MessagesDeleteMessages(m.Client.ctx,
		&tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     []int{int(m.ID)},
		})
	return err
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
