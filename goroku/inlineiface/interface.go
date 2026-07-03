// Package inlineiface defines the interface used by the goroku package for
// inline-bot interactions. Keeping the interface here (instead of referencing
// *inline.InlineManager directly from goroku/types.go) breaks the potential
// import cycle between goroku and inline.
package inlineiface

import (
	tgbotapi "github.com/OvyFlash/telegram-bot-api"

	"goroku/goroku/inline"
)

// InlineManager is the subset of *inline.InlineManager methods used by the
// goroku runtime. It is implemented by *inline.InlineManager.
type InlineManager interface {
	RegisterManager(afterBreak bool, ignoreTokenChecks bool) error
	IsComplete() bool
	GetBotAPI() *tgbotapi.BotAPI
	GetUnit(unitID string) (*inline.Unit, bool)
	CheckBot(username string) (bool, error)
	BotUsernameStr() string
	BotIDVal() int64
	GenerateMarkup(buttons [][]inline.Button) tgbotapi.InlineKeyboardMarkup
	PopQueryGallery(id string) (inline.QueryGalleryItem, bool)

	Form(text string, message interface{}, replyMarkup [][]inline.Button, opts ...inline.FormOpt) (*inline.InlineMessage, error)
	List(message interface{}, stringsList []string, opts ...inline.FormOpt) (*inline.InlineMessage, error)
	Gallery(message interface{}, nextHandler interface{}, caption interface{}, opts ...inline.FormOpt) (*inline.InlineMessage, error)
}

// Compile-time check that *inline.InlineManager implements InlineManager.
var _ InlineManager = (*inline.InlineManager)(nil)
