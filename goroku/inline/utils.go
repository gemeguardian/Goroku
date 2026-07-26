package inline

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

func (im *InlineManager) StoreUnit(unitID string, unit *Unit) {
	if unit.Module == "" {
		unit.Module = im.detectCallingModule()
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.closed {
		return
	}
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

var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	return htmlTagRegex.ReplaceAllString(s, "")
}

func (im *InlineManager) GenerateMarkup(buttons [][]Button) tgbotapi.InlineKeyboardMarkup {
	generation, _, err := im.claimIntake()
	if err != nil {
		return tgbotapi.InlineKeyboardMarkup{}
	}
	defer generation.release()
	return im.generateMarkup(buttons)
}

func (im *InlineManager) generateMarkup(buttons [][]Button) tgbotapi.InlineKeyboardMarkup {
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
					if !im.closed {
						im.customMap[switchQuery] = btn
					}
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
					if !im.closed {
						im.customMap[btn.Data] = btn
					}
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
	generation, _, err := im.claimIntake()
	if err != nil {
		return err
	}
	defer generation.release()
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

	markup := im.generateMarkup(unit.Buttons)

	if inlineMsgID != "" {
		editMsg := tgbotapi.EditMessageTextConfig{
			BaseEdit: tgbotapi.BaseEdit{
				InlineMessageID: inlineMsgID,
				ReplyMarkup:     &markup,
			},
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
		_, err := im.request(editMsg)
		return err
	}
	return nil
}

func (im *InlineManager) DeleteUnitMessage(unitID string) error {
	generation, _, err := im.claimIntake()
	if err != nil {
		return err
	}
	defer generation.release()
	im.mu.Lock()
	inlineMsgID := im.activeInlineMessages[unitID]
	info, hasInfo := im.activeMessageIDs[unitID]
	unload := im.removeUnitLocked(unitID)
	im.mu.Unlock()
	im.runUnitUnload(unload)

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
		_, err := im.request(editMsg)
		return err
	}
	return nil
}

func (im *InlineManager) removeUnitLocked(unitID string) func() {
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
	}
	delete(im.units, unitID)
	delete(im.activeInlineMessages, unitID)
	delete(im.activeMessageIDs, unitID)
	if unit != nil {
		return unit.OnUnload
	}
	return nil
}

func (im *InlineManager) runUnitUnload(unload func()) {
	if unload == nil {
		return
	}
	if !im.startGenerationWorker(func(context.Context) { unload() }) {
		unload()
	}
}

type moduleReceiver struct {
	pkgPath  string
	typeName string
}

type moduleOwner struct {
	name      string
	ambiguous bool
}

func (im *InlineManager) detectCallingModule() string {
	owners := im.registeredModuleOwners()
	if len(owners) == 0 {
		return ""
	}

	pcs := make([]uintptr, 32)
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

		receiver, ok := receiverFromFunction(frame.Function)
		if ok {
			if owner, exists := owners[receiver]; exists && !owner.ambiguous {
				return owner.name
			}
		}
		if !more {
			break
		}
	}
	return ""
}

func (im *InlineManager) registeredModuleOwners() map[moduleReceiver]moduleOwner {
	owners := make(map[moduleReceiver]moduleOwner)
	if im.allModules == nil {
		return owners
	}
	for _, registryName := range im.allModules.ModuleNames() {
		im.allModules.WithModule(registryName, func(module any) {
			t := reflect.TypeOf(module)
			for t != nil && t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			if t == nil || t.Name() == "" || t.PkgPath() == "" {
				return
			}
			name := registryName
			if named, ok := module.(interface{ Name() string }); ok && named.Name() != "" {
				name = named.Name()
			}
			key := moduleReceiver{pkgPath: t.PkgPath(), typeName: t.Name()}
			if existing, ok := owners[key]; ok && existing.name != name {
				owners[key] = moduleOwner{ambiguous: true}
				return
			}
			owners[key] = moduleOwner{name: name}
		})
	}
	return owners
}

func receiverFromFunction(function string) (moduleReceiver, bool) {
	if marker := strings.Index(function, ".("); marker >= 0 {
		rest := strings.TrimPrefix(function[marker+2:], "*")
		closeIdx := strings.Index(rest, ")")
		if closeIdx <= 0 {
			return moduleReceiver{}, false
		}
		return moduleReceiver{pkgPath: function[:marker], typeName: rest[:closeIdx]}, true
	}

	lastSlash := strings.LastIndex(function, "/")
	dotOffset := strings.Index(function[lastSlash+1:], ".")
	if dotOffset < 0 {
		return moduleReceiver{}, false
	}
	marker := lastSlash + 1 + dotOffset
	rest := function[marker+1:]
	dotIdx := strings.Index(rest, ".")
	if dotIdx <= 0 {
		return moduleReceiver{}, false
	}
	return moduleReceiver{pkgPath: function[:marker], typeName: rest[:dotIdx]}, true
}
