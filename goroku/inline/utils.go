package inline

import (
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (im *InlineManager) StoreUnit(unitID string, unit *Unit) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if unit.TTL.IsZero() {
		unit.TTL = time.Now().Add(im.markupTTL)
	}
	for rowIdx := range unit.Buttons {
		for btnIdx := range unit.Buttons[rowIdx] {
			btn := &unit.Buttons[rowIdx][btnIdx]
			if btn.Input != "" {
				if btn.SwitchQuery == "" {
					btn.SwitchQuery = localRandStr(10)
				}
				im.customMap[btn.SwitchQuery] = *btn
				im.buttonUnits[btn.SwitchQuery] = unitID
				continue
			}
			if (btn.Handler != nil || btn.InputHandler != nil) && btn.Data == "" {
				btn.Data = localRandStr(16)
			}
			if btn.Data != "" {
				im.customMap[btn.Data] = *btn
				im.buttonUnits[btn.Data] = unitID
			}
		}
	}
	im.units[unitID] = unit
}

func (im *InlineManager) GetUnit(unitID string) (*Unit, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	unit, ok := im.units[unitID]
	return unit, ok
}

func (im *InlineManager) GenerateMarkup(buttons [][]Button) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, row := range buttons {
		var line []tgbotapi.InlineKeyboardButton
		for _, btn := range row {
			if btn.URL != "" {
				line = append(line, tgbotapi.NewInlineKeyboardButtonURL(btn.Text, btn.URL))
			} else if btn.Input != "" {
				switchQuery := btn.SwitchQuery
				if switchQuery == "" {
					switchQuery = localRandStr(10)
					btn.SwitchQuery = switchQuery
					im.mu.Lock()
					im.customMap[switchQuery] = btn
					im.mu.Unlock()
				}
				swVal := switchQuery + " "
				line = append(line, tgbotapi.InlineKeyboardButton{
					Text:                         btn.Text,
					SwitchInlineQueryCurrentChat: &swVal,
				})
			} else {
				if (btn.Handler != nil || btn.InputHandler != nil) && btn.Data == "" {
					btn.Data = localRandStr(16)
				}
				if btn.Handler != nil || btn.InputHandler != nil {
					im.mu.Lock()
					im.customMap[btn.Data] = btn
					im.mu.Unlock()
				}
				line = append(line, tgbotapi.NewInlineKeyboardButtonData(btn.Text, btn.Data))
			}
		}
		rows = append(rows, line)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (im *InlineManager) EditUnit(unitID string, text string, buttons [][]Button) error {
	im.mu.RLock()
	unit, exists := im.units[unitID]
	inlineMsgID := im.activeInlineMessages[unitID]
	im.mu.RUnlock()

	if !exists {
		return fmt.Errorf("unit not found")
	}

	im.mu.Lock()
	unit.Text = text
	if buttons != nil {
		unit.Buttons = buttons
	}
	im.mu.Unlock()

	markup := im.GenerateMarkup(unit.Buttons)

	if inlineMsgID != "" {
		editMsg := tgbotapi.EditMessageTextConfig{
			BaseEdit: tgbotapi.BaseEdit{
				InlineMessageID: inlineMsgID,
				ReplyMarkup:     &markup,
			},
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
		_, err := im.bot.Request(editMsg)
		return err
	}
	return nil
}

func (im *InlineManager) DeleteUnitMessage(unitID string) error {
	im.mu.Lock()
	inlineMsgID := im.activeInlineMessages[unitID]
	im.removeUnitLocked(unitID)
	im.mu.Unlock()

	if inlineMsgID != "" {
		editMsg := tgbotapi.EditMessageTextConfig{
			BaseEdit: tgbotapi.BaseEdit{
				InlineMessageID: inlineMsgID,
			},
			Text: "🗑 <i>Message closed.</i>",
		}
		_, err := im.bot.Request(editMsg)
		return err
	}
	return nil
}

func (im *InlineManager) removeUnitLocked(unitID string) {
	unit := im.units[unitID]
	if unit != nil {
		for _, row := range unit.Buttons {
			for _, btn := range row {
				if btn.Data != "" {
					delete(im.customMap, btn.Data)
					delete(im.buttonUnits, btn.Data)
				}
				if btn.SwitchQuery != "" {
					delete(im.customMap, btn.SwitchQuery)
					delete(im.buttonUnits, btn.SwitchQuery)
				}
			}
		}
		if unit.OnUnload != nil {
			go unit.OnUnload()
		}
	}
	delete(im.units, unitID)
	delete(im.activeInlineMessages, unitID)
}
