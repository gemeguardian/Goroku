// Package inlineiface defines the interface used by the goroku package for
// inline-bot interactions. Keeping the interface here (instead of referencing
// *inline.InlineManager directly from goroku/types.go) breaks the potential
// import cycle between goroku and inline.
package inlineiface

import (
	"context"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"

	"goroku/goroku/inline"
)

// InlineManager is the subset of *inline.InlineManager methods used by the
// goroku runtime. It is implemented by *inline.InlineManager.
//
// The interface is defined in this separate package only to break the import
// cycle that would otherwise exist if types.go referenced *inline.InlineManager
// directly.
type InlineManager interface {
	RegisterManager(afterBreak bool, ignoreTokenChecks bool) error
	Close(context.Context) error
	IsComplete() bool
	GetBotAPI() *tgbotapi.BotAPI
	GetUnit(unitID string) (*inline.Unit, bool)
	CheckBot(username string) (bool, error)
	BotUsernameStr() string
	BotIDVal() int64
	GenerateMarkup(buttons [][]inline.Button) tgbotapi.InlineKeyboardMarkup
	PopQueryGallery(id string) (inline.QueryGalleryItem, bool)
	InlineModules() []inline.ModuleInlineHandlers

	Form(text string, message any, replyMarkup [][]inline.Button, opts ...inline.FormOpt) (*inline.InlineMessage, error)
	List(message any, stringsList []string, opts ...inline.FormOpt) (*inline.InlineMessage, error)
	Gallery(message any, nextHandler any, caption any, opts ...inline.FormOpt) (*inline.InlineMessage, error)
}

// Compile-time check that *inline.InlineManager implements InlineManager.
var _ InlineManager = (*inline.InlineManager)(nil)
