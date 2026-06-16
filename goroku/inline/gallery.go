package inline

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// Gallery creates a scrollable gallery with next/prev buttons and slideshow support.
func (im *InlineManager) Gallery(
	message interface{},
	nextHandler interface{}, // slice of strings or func(int) string
	caption interface{}, // string, slice of strings or func(int) string
	opts ...FormOpt,
) (*InlineMessage, error) {
	unitID := localRandStr(16)

	unit := &Unit{
		ID:          unitID,
		Type:        "gallery",
		Message:     message,
		CurrentPage: 0,
		TTL:         time.Now().Add(im.markupTTL),
	}

	for _, opt := range opts {
		opt(unit)
	}

	// Resolve gallery list/callback
	if list, ok := nextHandler.([]string); ok {
		unit.Pages = list
		unit.TotalPages = len(list)
	} else if fn, ok := nextHandler.(func(int) string); ok {
		unit.GalleryFn = fn
	} else {
		return nil, fmt.Errorf("invalid nextHandler type: must be []string or func(int) string")
	}

	// Resolve caption
	if cStr, ok := caption.(string); ok {
		unit.Text = cStr
	} else if cList, ok := caption.([]string); ok {
		unit.Pages = cList // If pages was empty, use this, or we can resolve per index
	}

	// Get first photo URL
	var firstPhoto string
	if unit.GalleryFn != nil {
		firstPhoto = unit.GalleryFn(0)
	} else if len(unit.Pages) > 0 {
		firstPhoto = unit.Pages[0]
	}

	if firstPhoto == "" {
		return nil, fmt.Errorf("gallery has no images")
	}

	unit.Photo = firstPhoto

	// Generate buttons
	unit.Buttons = im.generateGalleryButtons(unitID, 0, unit.TotalPages)

	// Store unit
	im.StoreUnit(unitID, unit)

	var chatID int64
	var replyToMsgID int64

	if id, ok := message.(int64); ok {
		chatID = id
	} else if id, ok := message.(int); ok {
		chatID = int64(id)
	} else {
		if hasChat, ok := message.(interface{ GetChatID() int64 }); ok {
			chatID = hasChat.GetChatID()
		}
		if hasReplyTo, ok := message.(interface{ GetReplyToMsgID() int64 }); ok {
			replyToMsgID = hasReplyTo.GetReplyToMsgID()
		}
	}

	if chatID == 0 {
		return nil, fmt.Errorf("invalid chat ID")
	}

	// Register callback handlers in customMap
	im.registerGalleryCallbacks(unitID, caption)

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
		_ = del.Delete()
	}

	im.mu.Lock()
	inlineMsgID := im.activeInlineMessages[unitID]
	im.mu.Unlock()

	return NewInlineMessage(im, unitID, inlineMsgID), nil
}

func (im *InlineManager) generateGalleryButtons(unitID string, page int, total int) [][]Button {
	var row []Button
	// Back
	if page > 0 {
		row = append(row, Button{
			Text: "⏪",
			Data: fmt.Sprintf("gal_%s_%d", unitID, page-1),
		})
	}
	// Slideshow toggle
	row = append(row, Button{
		Text: "⏱",
		Data: fmt.Sprintf("gal_%s_slideshow", unitID),
	})
	// Forward
	if total == 0 || page < total-1 {
		row = append(row, Button{
			Text: "⏩",
			Data: fmt.Sprintf("gal_%s_%d", unitID, page+1),
		})
	}

	return [][]Button{
		row,
		{
			{
				Text: "🔻 Close",
				Data: fmt.Sprintf("gal_%s_close", unitID),
			},
		},
	}
}

func (im *InlineManager) registerGalleryCallbacks(unitID string, caption interface{}) {
	im.mu.Lock()
	defer im.mu.Unlock()

	// Register close
	im.customMap[fmt.Sprintf("gal_%s_close", unitID)] = Button{
		Handler: func(c CallbackQuery) error {
			_ = c.Answer("Closing gallery...", false)
			_, err := c.BotMessage.Delete()
			if err != nil && c.InlineMessage != nil {
				_, err = c.InlineMessage.Delete()
			}
			im.mu.Lock()
			im.removeUnitLocked(unitID)
			im.mu.Unlock()
			return err
		},
	}

	// Register slideshow
	im.customMap[fmt.Sprintf("gal_%s_slideshow", unitID)] = Button{
		Handler: func(c CallbackQuery) error {
			im.mu.Lock()
			unit, ok := im.units[unitID]
			im.mu.Unlock()
			if !ok {
				return c.Answer("Gallery expired", true)
			}

			if unit.Interval == 0 {
				unit.Interval = 7 * time.Second
				_ = c.Answer("Slideshow enabled (7s)", false)
				go im.runSlideshow(unitID, c, caption)
			} else {
				unit.Interval = 0
				_ = c.Answer("Slideshow disabled", false)
			}
			return nil
		},
	}

	// Register paging handler (wildcard matching via data prefix in events)
	// We'll check prefix in events.go when dispatching callback queries!
}

func (im *InlineManager) runSlideshow(unitID string, c CallbackQuery, caption interface{}) {
	for {
		im.mu.Lock()
		unit, ok := im.units[unitID]
		interval := unit.Interval
		im.mu.Unlock()

		if !ok || interval == 0 {
			return
		}

		time.Sleep(interval)

		im.mu.Lock()
		unit, ok = im.units[unitID]
		if !ok || unit.Interval == 0 {
			im.mu.Unlock()
			return
		}
		nextPage := unit.CurrentPage + 1
		if unit.TotalPages > 0 && nextPage >= unit.TotalPages {
			nextPage = 0
		}
		im.mu.Unlock()

		err := im.updateGalleryPage(unitID, nextPage, c, caption)
		if err != nil {
			log.Printf("[Gallery] slideshow error: %v\n", err)
			return
		}
	}
}

func (im *InlineManager) updateGalleryPage(unitID string, page int, c CallbackQuery, caption interface{}) error {
	im.mu.Lock()
	unit, ok := im.units[unitID]
	im.mu.Unlock()
	if !ok {
		return fmt.Errorf("unit not found")
	}

	var photoURL string
	if unit.GalleryFn != nil {
		photoURL = unit.GalleryFn(page)
	} else if len(unit.Pages) > 0 {
		photoURL = unit.Pages[page%len(unit.Pages)]
	}

	if photoURL == "" {
		return fmt.Errorf("no image url found for page %d", page)
	}

	// Determine caption
	var capText string
	if cStr, ok := caption.(string); ok {
		capText = cStr
	} else if cList, ok := caption.([]string); ok {
		capText = cList[page%len(cList)]
	} else if cFn, ok := caption.(func(int) string); ok {
		capText = cFn(page)
	}

	im.mu.Lock()
	unit.CurrentPage = page
	unit.Photo = photoURL
	unit.Buttons = im.generateGalleryButtons(unitID, page, unit.TotalPages)
	markup := im.GenerateMarkup(unit.Buttons)
	im.mu.Unlock()

	media := tgbotapi.FileURL(photoURL)
	var inputMedia tgbotapi.InputMedia

	isGif := strings.HasSuffix(strings.ToLower(photoURL), ".gif") || strings.HasSuffix(strings.ToLower(photoURL), ".mp4") || unit.Gif
	if isGif {
		inputMedia = &tgbotapi.InputMediaAnimation{
			BaseInputMedia: tgbotapi.BaseInputMedia{
				Type:      "animation",
				Media:     media,
				Caption:   capText,
				ParseMode: tgbotapi.ModeHTML,
			},
		}
	} else {
		inputMedia = &tgbotapi.InputMediaPhoto{
			BaseInputMedia: tgbotapi.BaseInputMedia{
				Type:      "photo",
				Media:     media,
				Caption:   capText,
				ParseMode: tgbotapi.ModeHTML,
			},
		}
	}

	var editMsg tgbotapi.EditMessageMediaConfig
	if c.InlineMessage != nil {
		editMsg = tgbotapi.EditMessageMediaConfig{
			BaseEdit: tgbotapi.BaseEdit{
				InlineMessageID: c.InlineMessage.InlineMessageID,
				ReplyMarkup:     &markup,
			},
			Media: inputMedia,
		}
	} else {
		editMsg = tgbotapi.EditMessageMediaConfig{
			BaseEdit: tgbotapi.BaseEdit{
				BaseChatMessage: tgbotapi.BaseChatMessage{
					ChatConfig: tgbotapi.ChatConfig{ChatID: c.ChatID},
					MessageID:  int(c.MessageID),
				},
				ReplyMarkup: &markup,
			},
			Media: inputMedia,
		}
	}

	_, err := im.bot.Request(editMsg)
	return err
}

// HandleGalleryCallback processes pagination callbacks for gallery.
func (im *InlineManager) HandleGalleryCallback(c CallbackQuery) bool {
	if !strings.HasPrefix(c.Data, "gal_") {
		return false
	}

	parts := strings.Split(c.Data, "_")
	if len(parts) < 3 {
		return false
	}

	unitID := parts[1]
	action := parts[2]

	im.mu.Lock()
	unit, exists := im.units[unitID]
	im.mu.Unlock()

	if !exists {
		_ = c.Answer("Gallery expired", true)
		return true
	}

	if action == "slideshow" || action == "close" {
		// Handled by customMap handlers
		return false
	}

	page, err := strconv.Atoi(action)
	if err != nil {
		return false
	}

	_ = c.Answer(fmt.Sprintf("Loading slide %d...", page+1), false)

	// Resolve caption from unit configuration/state
	var caption interface{} = unit.Text
	if len(unit.Pages) > 0 && unit.GalleryFn == nil {
		caption = unit.Pages
	}

	err = im.updateGalleryPage(unitID, page, c, caption)
	if err != nil {
		_ = c.Answer(fmt.Sprintf("Error: %v", err), true)
	}

	return true
}
