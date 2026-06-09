package inline

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (im *InlineManager) StoreUnit(unitID string, unit *Unit) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if unit.TTL.IsZero() {
		unit.TTL = time.Now().Add(im.markupTTL)
	}
	if unit.Module == "" {
		unit.Module = im.detectCallingModule()
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

var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	return htmlTagRegex.ReplaceAllString(s, "")
}

func (im *InlineManager) GenerateMarkup(buttons [][]Button) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, row := range buttons {
		var line []tgbotapi.InlineKeyboardButton
		for _, btn := range row {
			btnText := stripHTML(btn.Text)
			applyVisual := func(apiBtn tgbotapi.InlineKeyboardButton) tgbotapi.InlineKeyboardButton {
				apiBtn.Style = btn.Style
				apiBtn.IconCustomEmojiID = btn.IconEmojiID
				return apiBtn
			}
			if btn.URL != "" {
				line = append(line, applyVisual(tgbotapi.NewInlineKeyboardButtonURL(btnText, btn.URL)))
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
				line = append(line, applyVisual(tgbotapi.InlineKeyboardButton{
					Text:                         btnText,
					SwitchInlineQueryCurrentChat: &swVal,
				}))
			} else {
				if (btn.Handler != nil || btn.InputHandler != nil) && btn.Data == "" {
					btn.Data = localRandStr(16)
				}
				if btn.Handler != nil || btn.InputHandler != nil {
					im.mu.Lock()
					im.customMap[btn.Data] = btn
					im.mu.Unlock()
				}
				line = append(line, applyVisual(tgbotapi.NewInlineKeyboardButtonData(btnText, btn.Data)))
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
		for _, row := range buttons {
			for _, btn := range row {
				if btn.Data != "" {
					im.buttonUnits[btn.Data] = unitID
				}
				if btn.SwitchQuery != "" {
					im.buttonUnits[btn.SwitchQuery] = unitID
				}
			}
		}
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
	info, hasInfo := im.activeMessageIDs[unitID]
	im.removeUnitLocked(unitID)
	im.mu.Unlock()

	if hasInfo && info.MessageID != 0 {
		if delClient, ok := im.client.(deletableClient); ok {
			err := delClient.DeleteMessage(info.ChatID, info.MessageID)
			if err == nil {
				return nil
			}
		}
	}

	if inlineMsgID != "" {
		editMsg := tgbotapi.EditMessageTextConfig{
			BaseEdit: tgbotapi.BaseEdit{
				InlineMessageID: inlineMsgID,
			},
			Text:      "🗑 <i>Message closed.</i>",
			ParseMode: tgbotapi.ModeHTML,
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
	delete(im.activeMessageIDs, unitID)
}

func (im *InlineManager) detectCallingModule() string {
	pcs := make([]uintptr, 15)
	n := runtime.Callers(2, pcs) // start from caller of the current function
	if n == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		// Skip all frames inside the inline package
		if strings.Contains(frame.Function, "goroku/inline") {
			if !more {
				break
			}
			continue
		}

		// This is the first frame outside "goroku/inline"!
		funcName := frame.Function

		// Parse the struct/receiver name
		if idx := strings.LastIndex(funcName, "/"); idx != -1 {
			funcName = funcName[idx+1:]
		}
		if idx := strings.Index(funcName, "."); idx != -1 {
			rest := funcName[idx+1:] // "(*GorokuBackup).SomeMethod" or "SomeFunction"
			rest = strings.TrimPrefix(rest, "*")
			rest = strings.TrimPrefix(rest, "(")
			rest = strings.TrimPrefix(rest, "*")
			if closeIdx := strings.Index(rest, ")"); closeIdx != -1 {
				return rest[:closeIdx]
			}
			if dotIdx := strings.Index(rest, "."); dotIdx != -1 {
				return rest[:dotIdx]
			}
			return rest
		}
		if !more {
			break
		}
	}
	return ""
}
