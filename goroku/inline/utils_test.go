package inline

import (
	"bytes"
	"github.com/gotd/td/tg"
	"goroku/goroku/chatref"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// mockRoundTripper intercepts HTTP requests for tgbotapi.BotAPI
type mockRoundTripper struct {
	roundTrip func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

type mockDeletableClient struct {
	deleteCalled bool
	chat         any
	messageID    int64
	err          error
}

func (m *mockDeletableClient) TGIDValue() int64 { return 0 }
func (m *mockDeletableClient) SendMessage(chat chatref.ChatRef, message string) (any, error) {
	return nil, nil
}
func (m *mockDeletableClient) CreateGorokuFolder(botID int64) error                   { return nil }
func (m *mockDeletableClient) InviteBotToChannel(channelPeer tg.InputPeerClass) error { return nil }
func (m *mockDeletableClient) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error  { return nil }
func (m *mockDeletableClient) GetSecurityManager() SecurityChecker                    { return nil }

func (m *mockDeletableClient) DeleteMessage(chat any, messageID int64) error {
	m.deleteCalled = true
	m.chat = chat
	m.messageID = messageID
	return m.err
}

func TestStoreAndGetUnit(t *testing.T) {
	im := &InlineManager{
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		markupTTL:            5 * time.Minute,
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
	}

	btn1 := Button{Text: "Btn 1", Data: "data1"}
	btn2 := Button{Text: "Btn 2", Input: "input2", SwitchQuery: "switch2"}
	btn3 := Button{Text: "Btn 3", Handler: func(ctx CallbackQuery) error { return nil }}

	unit := &Unit{
		Text: "Test Unit",
		Buttons: [][]Button{
			{btn1, btn2, btn3},
		},
	}

	im.StoreUnit("unit1", unit)

	// Verify unit store
	stored, ok := im.GetUnit("unit1")
	if !ok {
		t.Fatal("Expected unit1 to be stored")
	}
	if stored.Text != "Test Unit" {
		t.Errorf("Expected Text 'Test Unit', got %s", stored.Text)
	}

	// TTL should be set
	if stored.TTL.IsZero() {
		t.Error("Expected TTL to be initialized")
	}

	// Module should be detected (since called from test, it should have detected something)
	if stored.Module == "" {
		t.Error("Expected Module to be detected and set")
	}

	// Verify button custom maps
	im.mu.RLock()
	defer im.mu.RUnlock()

	// btn1
	if _, ok := im.customMap["data1"]; !ok {
		t.Error("Expected data1 in customMap")
	}
	if uID := im.buttonUnits["data1"]; uID != "unit1" {
		t.Errorf("Expected buttonUnits['data1'] to be 'unit1', got %s", uID)
	}

	// btn2
	if _, ok := im.customMap["switch2"]; !ok {
		t.Error("Expected switch2 in customMap")
	}

	// btn3 (should have generated a random Data string)
	var generatedData string
	for k, btn := range im.customMap {
		if btn.Text == "Btn 3" {
			generatedData = k
			break
		}
	}
	if generatedData == "" {
		t.Error("Expected random Data generated for btn3")
	}
}

func TestGenerateMarkup(t *testing.T) {
	im := &InlineManager{
		customMap:   make(map[string]Button),
		buttonUnits: make(map[string]string),
	}

	buttons := [][]Button{
		{
			{Text: "<b>URL Button</b>", URL: "https://example.com"},
			{Text: "Input Button", Input: "some_input"},
			{Text: "Data Button", Data: "some_data"},
			{Text: "Handler Button", Handler: func(ctx CallbackQuery) error { return nil }},
		},
	}

	markup := im.GenerateMarkup(buttons)
	if len(markup.InlineKeyboard) != 1 {
		t.Fatalf("Expected 1 row of buttons, got %d", len(markup.InlineKeyboard))
	}

	row := markup.InlineKeyboard[0]
	if len(row) != 4 {
		t.Fatalf("Expected 4 buttons, got %d", len(row))
	}

	// URL button (HTML tags stripped)
	if row[0].Text != "URL Button" {
		t.Errorf("Expected stripped text 'URL Button', got %q", row[0].Text)
	}
	if row[0].URL == nil || *row[0].URL != "https://example.com" {
		t.Errorf("Expected URL 'https://example.com', got %v", row[0].URL)
	}

	// Input button
	if row[1].SwitchInlineQueryCurrentChat == nil || !strings.HasSuffix(*row[1].SwitchInlineQueryCurrentChat, " ") {
		t.Errorf("Expected SwitchInlineQueryCurrentChat set with space suffix, got %v", row[1].SwitchInlineQueryCurrentChat)
	}

	// Data button
	if row[2].CallbackData == nil || *row[2].CallbackData != "some_data" {
		t.Errorf("Expected CallbackData 'some_data', got %v", row[2].CallbackData)
	}

	// Handler button (should have generated callback data)
	if row[3].CallbackData == nil || *row[3].CallbackData == "" {
		t.Error("Expected generated CallbackData for handler button")
	}
}

func TestEditAndDeleteUnit(t *testing.T) {
	transport := &mockRoundTripper{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			var respStr string
			if req.URL.Path == "/botmock_token/getMe" {
				respStr = `{"ok": true, "result": {"id": 123456, "is_bot": true, "first_name": "TestBot", "username": "test_bot"}}`
			} else {
				respStr = `{"ok":true,"result":{"message_id":123}}`
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte(respStr))),
			}, nil
		},
	}

	bot, err := tgbotapi.NewBotAPIWithOptions("mock_token", tgbotapi.WithAPIEndpoint(tgbotapi.APIEndpoint), tgbotapi.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatalf("Failed to create mock bot: %v", err)
	}

	im := &InlineManager{
		bot:                  bot,
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
	}

	// Try editing non-existent unit
	err = im.EditUnit("nonexistent", "Hello", nil)
	if err == nil || !strings.Contains(err.Error(), "unit not found") {
		t.Errorf("Expected 'unit not found' error, got %v", err)
	}

	// Setup unit
	unit := &Unit{
		Text: "Original text",
		Buttons: [][]Button{
			{{Text: "Click", Data: "click_data"}},
		},
	}
	im.units["unit1"] = unit
	im.activeInlineMessages["unit1"] = "inline_msg_id_123"

	// Edit unit
	err = im.EditUnit("unit1", "New text", [][]Button{
		{{Text: "New Click", Data: "new_click_data"}},
	})
	if err != nil {
		t.Fatalf("EditUnit failed: %v", err)
	}

	if unit.Text != "New text" {
		t.Errorf("Expected text 'New text', got %s", unit.Text)
	}

	// Test DeleteUnitMessage with deletableClient
	mockClient := &mockDeletableClient{}
	im.client = mockClient
	im.activeMessageIDs["unit1"] = MessageIDInfo{
		ChatID:    456,
		MessageID: 789,
	}

	t.Logf("im.client is %T, deletableClient ok: %v", im.client, mockClient != nil)
	im.mu.RLock()
	info, hasInfo := im.activeMessageIDs["unit1"]
	im.mu.RUnlock()
	t.Logf("activeMessageIDs hasInfo: %v, messageID: %d", hasInfo, info.MessageID)

	err = im.DeleteUnitMessage("unit1")
	if err != nil {
		t.Fatalf("DeleteUnitMessage failed: %v", err)
	}

	if !mockClient.deleteCalled {
		t.Error("Expected DeleteMessage to be called on client")
	}
	if mockClient.chat != int64(456) || mockClient.messageID != 789 {
		t.Errorf("Expected DeleteMessage(456, 789), got DeleteMessage(%v, %d)", mockClient.chat, mockClient.messageID)
	}

	// After deletion, unit should be removed from maps
	if _, ok := im.GetUnit("unit1"); ok {
		t.Error("Expected unit1 to be removed from units map")
	}
}
