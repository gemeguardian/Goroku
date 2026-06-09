package inline

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"time"
)

type FormOpt func(*Unit)

func WithPhoto(url string) FormOpt {
	return func(u *Unit) { u.Photo = url }
}

func WithGif(url string) FormOpt {
	return func(u *Unit) { u.GifURL = url }
}

func WithVideo(url string) FormOpt {
	return func(u *Unit) { u.Video = url }
}

func WithFile(url, mimeType string) FormOpt {
	return func(u *Unit) {
		u.File = url
		u.MimeType = mimeType
	}
}

func WithLocation(lat, lon float64) FormOpt {
	return func(u *Unit) { u.Location = []float64{lat, lon} }
}

func WithAudio(audio interface{}) FormOpt {
	return func(u *Unit) { u.Audio = audio }
}

func WithForceMe(b bool) FormOpt {
	return func(u *Unit) { u.ForceMe = b }
}

func WithDisableSecurity(b bool) FormOpt {
	return func(u *Unit) { u.DisableSecurity = b }
}

func WithAlwaysAllow(ids []int64) FormOpt {
	return func(u *Unit) { u.AlwaysAllow = ids }
}

func WithTTL(duration time.Duration) FormOpt {
	return func(u *Unit) { u.TTL = time.Now().Add(duration) }
}

func WithOnUnload(fn func()) FormOpt {
	return func(u *Unit) { u.OnUnload = fn }
}

func WithStartText(text string) FormOpt {
	return func(u *Unit) { u.StartText = text }
}

func localRandStr(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz1234567890"
	b := make([]byte, size)
	for i := range b {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			b[i] = charset[0]
		} else {
			b[i] = charset[num.Int64()]
		}
	}
	return string(b)
}

// Form creates and sends an interactive form.
func (im *InlineManager) Form(
	text string,
	message interface{}, // This can be *Message or ChatID (int64)
	replyMarkup [][]Button,
	opts ...FormOpt,
) (*InlineMessage, error) {
	unitID := localRandStr(16)

	unit := &Unit{
		ID:      unitID,
		Type:    "form",
		Text:    text,
		Message: message,
		Buttons: replyMarkup,
		TTL:     time.Now().Add(im.markupTTL),
	}

	for _, opt := range opts {
		opt(unit)
	}

	// Store unit
	im.StoreUnit(unitID, unit)

	var chatID int64
	var replyToMsgID int64

	// Try to extract ChatID and ReplyToMsgID from message interface
	// In Python, this is either message ID (int) or Message object
	if id, ok := message.(int64); ok {
		chatID = id
	} else if id, ok := message.(int); ok {
		chatID = int64(id)
	} else {
		// Use reflection or interface assertion to get ChatID / ID
		// In goroku package, Message struct has ChatID and ID.
		// Since we cannot import goroku directly, we use interface assertions:
		if hasChat, ok := message.(interface{ GetChatID() int64 }); ok {
			chatID = hasChat.GetChatID()
		}
		if hasReplyTo, ok := message.(interface{ GetReplyToMsgID() int64 }); ok {
			replyToMsgID = hasReplyTo.GetReplyToMsgID()
		}
	}

	if chatID == 0 {
		return nil, fmt.Errorf("invalid or zero chat ID")
	}

	// Invoke unit
	_, err := im.InvokeUnit(unitID, chatID, replyToMsgID)
	if err != nil {
		im.mu.Lock()
		im.removeUnitLocked(unitID)
		im.mu.Unlock()
		return nil, err
	}

	// Delete original message if outgoing
	type deletable interface {
		Delete() error
		IsOut() bool
	}
	if del, ok := message.(deletable); ok && del.IsOut() {
		for attempt := 1; attempt <= 3; attempt++ {
			if attempt > 1 {
				time.Sleep(time.Duration(attempt*250) * time.Millisecond)
			}
			if err := del.Delete(); err != nil {
				log.Printf("[Inline] failed to delete source form message attempt=%d: %v", attempt, err)
				continue
			}
			log.Printf("[Inline] deleted source form message attempt=%d", attempt)
			break
		}
	}

	im.mu.Lock()
	inlineMsgID := im.activeInlineMessages[unitID]
	im.mu.Unlock()

	return NewInlineMessage(im, unitID, inlineMsgID), nil
}

// CreateForm is a legacy compatibility wrapper for Form.
func (im *InlineManager) CreateForm(args ...interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	text, ok1 := args[0].(string)
	msg := args[1]
	var markup [][]Button
	if len(args) > 2 {
		if m, ok := args[2].([][]Button); ok {
			markup = m
		}
	}
	if !ok1 {
		return nil
	}
	res, err := im.Form(text, msg, markup)
	if err != nil {
		return nil
	}
	return res
}
