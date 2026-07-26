package webiface

import (
	"goroku/goroku/chatref"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// ChatRef is re-exported from chatref so webiface and goroku share the same type.
type ChatRef = chatref.ChatRef

// SentMessage is re-exported from chatref; it is the typed return of
// TelegramClient.SendMessage.
type SentMessage = chatref.SentMessage

// TelegramClient is the subset of *goroku.CustomTelegramClient methods used
// by the web package. It is implemented by *goroku.CustomTelegramClient.
type TelegramClient interface {
	TGIDValue() int64
	// Connected reports whether the MTProto transport is up. The readiness
	// probe uses it, so a dead connection is visible from outside.
	Connected() bool
	Connect() error
	Disconnect() error
	SendCodeRequest(phone string) error
	SignIn(phone, code, password string) error
	InlineProvider() InlineProvider
	QRLogin() (string, error)
	QRLoginStatus() (string, error)
	SendMessage(chat ChatRef, message string) (chatref.SentMessage, error)
	ResolveUsername(username string) (bool, error)
	CheckBot(username string) (bool, error)
}

// InlineProvider is the subset of the inline manager used by the web package.
type InlineProvider interface {
	GetBotAPI() *tgbotapi.BotAPI
	PopWebAuthToken(token string) bool
}

// Database is the subset of *goroku.Database used by the web package.
type Database interface {
	Get(owner, key string, defaultValue any) (any, error)
}

// ModulesRegistry is the modules surface held on RuntimeClient.
// Web handlers do not call into it yet; *goroku.Modules implements it.
// Residual: expand when web UI needs module list/control (M7).
type ModulesRegistry interface {
	ModuleNames() []string
}
