package inline

import (
	"testing"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

func TestFSMState(t *testing.T) {
	im := &InlineManager{
		fsm: make(map[string]string),
	}

	// 1. Initially false / not set
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false for unset FSM, got %v", state)
	}

	// 2. Set state
	if !im.SetFSMState(42, "state_1") {
		t.Error("SetFSMState failed")
	}
	if state := im.GetFSMState(42); state != "state_1" {
		t.Errorf("Expected 'state_1', got %v", state)
	}

	// 3. Clear state by setting to nil
	if !im.SetFSMState(42, nil) {
		t.Error("SetFSMState with nil failed")
	}
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false after clearing state, got %v", state)
	}

	// 4. Clear state by setting to empty string
	im.SetFSMState(42, "state_2")
	im.SetFSMState(42, "")
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false after setting to empty string, got %v", state)
	}

	// 5. Clear state by setting to "false"
	im.SetFSMState(42, "state_3")
	im.SetFSMState(42, "false")
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false after setting to 'false', got %v", state)
	}
}

type mockModules struct {
	modules map[string]any
}

func (m *mockModules) GetModules() map[string]any {
	return m.modules
}

type mockPMModule struct {
	name            string
	botPMCalled     bool
	lastReceivedMsg *tgbotapi.Message
}

func (m *mockPMModule) Name() string {
	return m.name
}

func (m *mockPMModule) HandleBotPM(msg *tgbotapi.Message) {
	m.botPMCalled = true
	m.lastReceivedMsg = msg
}

func TestHandleBotPM(t *testing.T) {
	mockMod := &mockPMModule{name: "InlineStuff"}
	mods := &mockModules{
		modules: map[string]any{
			"inline": mockMod,
		},
	}

	im := &InlineManager{
		allModules: mods,
	}

	// 1. Should ignore start init
	msgInit := &tgbotapi.Message{
		Chat: tgbotapi.Chat{ID: 123, Type: "private"},
		Text: "/start goroku init",
	}
	im.HandleBotPM(msgInit)
	if mockMod.botPMCalled {
		t.Error("Expected HandleBotPM not to be called for init message")
	}

	// 2. Normal PM message
	msgNormal := &tgbotapi.Message{
		Chat: tgbotapi.Chat{ID: 123, Type: "private"},
		Text: "Hello bot",
	}
	im.HandleBotPM(msgNormal)
	if !mockMod.botPMCalled {
		t.Error("Expected HandleBotPM to be called for normal message")
	}
	if mockMod.lastReceivedMsg.Text != "Hello bot" {
		t.Errorf("Expected message text 'Hello bot', got %s", mockMod.lastReceivedMsg.Text)
	}

	// 3. /start command on non-InlineStuff module should be skipped
	mockMod.botPMCalled = false
	mockOtherMod := &mockPMModule{name: "OtherModule"}
	mods.modules["other"] = mockOtherMod

	msgStart := &tgbotapi.Message{
		Chat: tgbotapi.Chat{ID: 123, Type: "private"},
		Text: "/start",
	}
	im.HandleBotPM(msgStart)
	if mockOtherMod.botPMCalled {
		t.Error("Expected /start not to be handled by non-InlineStuff module")
	}
	if !mockMod.botPMCalled {
		t.Error("Expected /start to be handled by InlineStuff module")
	}
}
