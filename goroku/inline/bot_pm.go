package inline

import (
	"fmt"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// SetFSMState sets the FSM state for a user.
func (im *InlineManager) SetFSMState(user any, state any) bool {
	im.mu.Lock()
	defer im.mu.Unlock()

	userStr := fmt.Sprintf("%v", user)
	stateStr := fmt.Sprintf("%v", state)

	if state == nil || stateStr == "" || stateStr == "false" {
		delete(im.fsm, userStr)
		return true
	}

	im.fsm[userStr] = stateStr
	return true
}

// GetFSMState retrieves the FSM state of a user. Returns false if not set.
func (im *InlineManager) GetFSMState(user any) any {
	im.mu.RLock()
	defer im.mu.RUnlock()

	userStr := fmt.Sprintf("%v", user)
	if val, ok := im.fsm[userStr]; ok {
		return val
	}
	return false
}

// HandleBotPM is called to process private messages sent to the inline bot.
func (im *InlineManager) HandleBotPM(m *tgbotapi.Message) {
	if m.Chat.ID == 0 || !m.Chat.IsPrivate() || m.Text == "/start goroku init" {
		return
	}

	// Forward PM to loaded modules that support HandleBotPM
	if im.allModules != nil {
		for _, mod := range im.allModules.GetModules() {
			if m.Text == "/start" {
				if named, ok := mod.(interface{ Name() string }); !ok || named.Name() != "InlineStuff" {
					continue
				}
			}
			if handler, ok := mod.(interface{ HandleBotPM(msg *tgbotapi.Message) }); ok {
				handler.HandleBotPM(m)
			}
		}
	}
}
