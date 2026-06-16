package inline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// List creates and sends a paginated list menu.
func (im *InlineManager) List(
	message interface{},
	stringsList []string,
	opts ...FormOpt,
) (*InlineMessage, error) {
	if len(stringsList) == 0 {
		return nil, fmt.Errorf("list cannot be empty")
	}

	unitID := localRandStr(16)

	unit := &Unit{
		ID:          unitID,
		Type:        "list",
		Pages:       stringsList,
		CurrentPage: 0,
		TotalPages:  len(stringsList),
		TTL:         time.Now().Add(im.markupTTL),
	}

	for _, opt := range opts {
		opt(unit)
	}

	// Generate buttons
	unit.Buttons = im.generateListButtons(unitID, 0, unit.TotalPages)

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

	// Register close callback
	im.mu.Lock()
	im.customMap[fmt.Sprintf("lst_%s_close", unitID)] = Button{
		Handler: func(c CallbackQuery) error {
			_ = c.Answer("Closing list...", false)
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
	im.mu.Unlock()

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

func (im *InlineManager) generateListButtons(unitID string, page, total int) [][]Button {
	pagingRows := im.BuildPagination(unitID, page+1, total, "lst")

	closeRow := []Button{
		{
			Text: "🔻 Close",
			Data: fmt.Sprintf("lst_%s_close", unitID),
		},
	}

	return append(pagingRows, closeRow)
}

func (im *InlineManager) updateListPage(unitID string, page int, c CallbackQuery) error {
	im.mu.Lock()
	unit, ok := im.units[unitID]
	im.mu.Unlock()
	if !ok {
		return fmt.Errorf("unit not found")
	}

	if page < 0 || page >= len(unit.Pages) {
		return fmt.Errorf("page out of bounds")
	}

	text := unit.Pages[page]

	im.mu.Lock()
	unit.CurrentPage = page
	unit.Buttons = im.generateListButtons(unitID, page, unit.TotalPages)
	markup := im.GenerateMarkup(unit.Buttons)
	im.mu.Unlock()

	var editMsg tgbotapi.EditMessageTextConfig
	if c.InlineMessage != nil {
		editMsg = tgbotapi.EditMessageTextConfig{
			BaseEdit: tgbotapi.BaseEdit{
				InlineMessageID: c.InlineMessage.InlineMessageID,
				ReplyMarkup:     &markup,
			},
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
	} else {
		editMsg = tgbotapi.EditMessageTextConfig{
			BaseEdit: tgbotapi.BaseEdit{
				BaseChatMessage: tgbotapi.BaseChatMessage{
					ChatConfig: tgbotapi.ChatConfig{ChatID: c.ChatID},
					MessageID:  int(c.MessageID),
				},
				ReplyMarkup: &markup,
			},
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
	}

	_, err := im.bot.Request(editMsg)
	return err
}

// HandleListCallback handles paging callbacks for list.
func (im *InlineManager) HandleListCallback(c CallbackQuery) bool {
	if !strings.HasPrefix(c.Data, "lst_") {
		return false
	}

	parts := strings.Split(c.Data, "_")
	if len(parts) < 3 {
		return false
	}

	unitID := parts[1]
	action := parts[2]

	if action == "close" {
		return false // Handled by customMap handler
	}

	page, err := strconv.Atoi(action)
	if err != nil {
		return false
	}

	_ = c.Answer(fmt.Sprintf("Loading page %d...", page+1), false)

	err = im.updateListPage(unitID, page, c)
	if err != nil {
		_ = c.Answer(fmt.Sprintf("Error: %v", err), true)
	}

	return true
}

func (im *InlineManager) BuildPagination(unitID string, currentPage, totalPages int, prefix string) [][]Button {
	if totalPages <= 1 {
		return nil
	}

	var row []Button

	if totalPages <= 5 {
		for i := 1; i <= totalPages; i++ {
			text := strconv.Itoa(i)
			if i == currentPage {
				text = fmt.Sprintf("· %d ·", i)
			}
			row = append(row, Button{
				Text: text,
				Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, i-1),
			})
		}
		return [][]Button{row}
	}

	if currentPage <= 3 {
		for i := 1; i <= 5; i++ {
			text := strconv.Itoa(i)
			pageIdx := i - 1
			if i == currentPage {
				text = fmt.Sprintf("· %d ·", i)
			} else if i == 4 {
				text = fmt.Sprintf("%d ›", i)
			} else if i == 5 {
				text = fmt.Sprintf("%d »", totalPages)
				pageIdx = totalPages - 1
			}
			row = append(row, Button{
				Text: text,
				Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, pageIdx),
			})
		}
		return [][]Button{row}
	}

	if currentPage > totalPages-3 {
		row = append(row, Button{
			Text: "« 1",
			Data: fmt.Sprintf("%s_%s_0", prefix, unitID),
		})
		row = append(row, Button{
			Text: fmt.Sprintf("‹ %d", totalPages-3),
			Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, totalPages-4),
		})
		for i := totalPages - 2; i <= totalPages; i++ {
			text := strconv.Itoa(i)
			if i == currentPage {
				text = fmt.Sprintf("· %d ·", i)
			}
			row = append(row, Button{
				Text: text,
				Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, i-1),
			})
		}
		return [][]Button{row}
	}

	row = append(row, Button{
		Text: "« 1",
		Data: fmt.Sprintf("%s_%s_0", prefix, unitID),
	})
	row = append(row, Button{
		Text: fmt.Sprintf("‹ %d", currentPage-1),
		Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, currentPage-2),
	})
	row = append(row, Button{
		Text: fmt.Sprintf("· %d ·", currentPage),
		Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, currentPage-1),
	})
	row = append(row, Button{
		Text: fmt.Sprintf("%d ›", currentPage+1),
		Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, currentPage),
	})
	row = append(row, Button{
		Text: fmt.Sprintf("%d »", totalPages),
		Data: fmt.Sprintf("%s_%s_%d", prefix, unitID, totalPages-1),
	})

	return [][]Button{row}
}
