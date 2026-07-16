package inline

import (
	"goroku/goroku/chatref"

	"github.com/gotd/td/tg"
)

// Database is the minimal DB API needed by the inline manager.
// It deliberately mirrors goroku.Database's Get/Set without importing the whole package.
type Database interface {
	Get(namespace, key string, defaultValue any) (any, error)
	Set(namespace, key string, value any) error
}

// SecurityChecker is the minimal security API the inline manager needs.
type SecurityChecker interface {
	IsOwner(userID int64) bool
	CheckModuleAccess(userID int64, moduleName string) bool
}

// InlineUserBot is the minimal set of methods the inline manager needs
// from the main userbot client (CustomTelegramClient).
type InlineUserBot interface {
	TGIDValue() int64
	SendMessage(chat chatref.ChatRef, message string) (any, error)
	CreateGorokuFolder(botID int64) error
	InviteBotToChannel(channelPeer tg.InputPeerClass) error
	PromoteBotToAdmin(channelPeer tg.InputPeerClass) error
	GetSecurityManager() SecurityChecker
}

// InlineModules provides access to the module registry for inline handlers.
type InlineModules interface {
	ModuleNames() []string
	WithModule(name string, fn func(any)) bool
}
