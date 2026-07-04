package webiface

import (
	"goroku/goroku/chatref"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// ChatRef is re-exported from chatref so webiface and goroku share the same type.
type ChatRef = chatref.ChatRef

// TelegramClient is the subset of *goroku.CustomTelegramClient methods used
// by the web package. It is implemented by *goroku.CustomTelegramClient.
type TelegramClient interface {
	TGIDValue() int64
	Connect() error
	Disconnect() error
	SendCodeRequest(phone string) error
	SignIn(phone, code, password string) error
	QRLogin() (string, error)
	QRLoginStatus() (string, error)
	SendMessage(chat ChatRef, message string) (any, error)
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

// Modules is the subset of *goroku.Modules used by the web package.
type Modules interface {
	GetModule(name string) (Module, bool)
}

// Module is the subset of goroku.Module used by the web package.
type Module interface {
	Name() string
}
