package inline

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		inputs   []string
		expected string
	}{
		{[]string{"", "hello", "world"}, "hello"},
		{[]string{"", "", ""}, ""},
		{[]string{"apple", "banana"}, "apple"},
	}
	for _, tc := range tests {
		got := firstNonEmpty(tc.inputs...)
		if got != tc.expected {
			t.Errorf("firstNonEmpty(%v) = %q; want %q", tc.inputs, got, tc.expected)
		}
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<b>hello</b>", "hello"},
		{"hello <a href=''>world</a>!", "hello world!"},
		{"plain text", "plain text"},
	}
	for _, tc := range tests {
		got := stripHTML(tc.input)
		if got != tc.expected {
			t.Errorf("stripHTML(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestCallbackQueryAnswerAndEdit(t *testing.T) {
	var lastMethod string
	var lastBody string

	transport := &mockRoundTripper{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			lastMethod = req.URL.Path
			bodyBytes, _ := io.ReadAll(req.Body)
			lastBody = string(bodyBytes)
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
		bot:         bot,
		buttonUnits: make(map[string]string),
	}
	cb := CallbackQuery{
		ID:      "cb_id_123",
		Manager: im,
	}

	if err := cb.Answer("Hello", true); err != nil {
		t.Fatalf("CallbackQuery.Answer failed: %v", err)
	}
	if !strings.Contains(lastMethod, "answerCallbackQuery") {
		t.Errorf("Expected method answerCallbackQuery, got %s", lastMethod)
	}
	unescapedBody, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(unescapedBody, "cb_id_123") || !strings.Contains(unescapedBody, "Hello") {
		t.Errorf("Expected cb_id_123 and Hello in request body, got %s", unescapedBody)
	}

	if err := cb.Edit("New text", tgbotapi.InlineKeyboardMarkup{}); err == nil || !strings.Contains(err.Error(), "no message to edit") {
		t.Errorf("Expected 'no message to edit' error, got %v", err)
	}
}

func TestInlineMessageEditAndDelete(t *testing.T) {
	var lastMethod string
	var lastBody string

	transport := &mockRoundTripper{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			lastMethod = req.URL.Path
			bodyBytes, _ := io.ReadAll(req.Body)
			lastBody = string(bodyBytes)
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
		bot:              bot,
		buttonUnits:      make(map[string]string),
		activeMessageIDs: make(map[string]MessageIDInfo),
	}
	msg := NewInlineMessage(im, "unit_123", "inline_msg_id_789")
	if msg.Form == nil {
		t.Error("Expected Form map to be initialized")
	}

	btnVal := "btn_data_val"
	swVal := "switch_val"
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Data", btnVal),
			tgbotapi.InlineKeyboardButton{Text: "Switch", SwitchInlineQueryCurrentChat: &swVal},
		),
	)

	if err := msg.Edit("Updated text", markup); err != nil {
		t.Fatalf("InlineMessage.Edit failed: %v", err)
	}
	if !strings.Contains(lastMethod, "editMessageText") {
		t.Errorf("Expected editMessageText method, got %s", lastMethod)
	}
	unescapedBody, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(unescapedBody, "inline_msg_id_789") || !strings.Contains(unescapedBody, "Updated text") {
		t.Errorf("Expected inlineMsgID and Text in request body, got %s", unescapedBody)
	}

	im.mu.RLock()
	uData := im.buttonUnits[btnVal]
	uSwitch := im.buttonUnits[swVal]
	im.mu.RUnlock()
	if uData != "unit_123" {
		t.Errorf("Expected buttonUnits['%s'] to be 'unit_123', got %s", btnVal, uData)
	}
	if uSwitch != "unit_123" {
		t.Errorf("Expected buttonUnits['%s'] to be 'unit_123', got %s", swVal, uSwitch)
	}

	ok, err := msg.Delete()
	if err != nil {
		t.Fatalf("InlineMessage.Delete failed: %v", err)
	}
	if !ok {
		t.Error("Expected Delete to return true")
	}
	unescapedBody2, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(lastMethod, "editMessageText") || !strings.Contains(unescapedBody2, "Message closed") {
		t.Errorf("Expected editMessageText with Message closed, got method=%s body=%s", lastMethod, unescapedBody2)
	}

	mockClient := &mockDeletableClient{}
	im.client = mockClient
	im.activeMessageIDs["unit_123"] = MessageIDInfo{ChatID: 456, MessageID: 789}
	_ = mockClient
	ok, err = msg.Delete()
	if err != nil {
		t.Fatalf("InlineMessage.Delete with client failed: %v", err)
	}
	if !ok {
		t.Error("Expected Delete with client to return true")
	}
	if !mockClient.deleteCalled || mockClient.chat != int64(456) || mockClient.messageID != 789 {
		t.Errorf("Expected mockClient to receive DeleteMessage(456, 789), got called=%v chat=%v msgID=%d", mockClient.deleteCalled, mockClient.chat, mockClient.messageID)
	}

	mockClient.deleteCalled = false
	im.activeMessageIDs["unit_123"] = MessageIDInfo{ChatID: 456, MessageID: 789}
	ok, err = msg.Unload()
	if err != nil || !ok || !mockClient.deleteCalled {
		t.Errorf("Unload failed: err=%v, ok=%v, called=%v", err, ok, mockClient.deleteCalled)
	}
}

func TestBotInlineMessageEditAndDelete(t *testing.T) {
	var lastMethod string
	var lastBody string

	transport := &mockRoundTripper{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			lastMethod = req.URL.Path
			bodyBytes, _ := io.ReadAll(req.Body)
			lastBody = string(bodyBytes)
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
		bot:         bot,
		buttonUnits: make(map[string]string),
	}
	msg := NewBotInlineMessage(im, "unit_456", 111, 222)
	if msg.Form == nil {
		t.Error("Expected Form map to be initialized")
	}

	btnVal := "data_val"
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Data", btnVal),
		),
	)

	if err := msg.Edit("Bot text", markup); err != nil {
		t.Fatalf("BotInlineMessage.Edit failed: %v", err)
	}
	if !strings.Contains(lastMethod, "editMessageText") {
		t.Errorf("Expected editMessageText method, got %s", lastMethod)
	}
	unescapedBody, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(unescapedBody, "111") || !strings.Contains(unescapedBody, "222") || !strings.Contains(unescapedBody, "Bot text") {
		t.Errorf("Expected ChatID, MessageID and Text in request body, got %s", unescapedBody)
	}

	ok, err := msg.Delete()
	if err != nil {
		t.Fatalf("BotInlineMessage.Delete failed: %v", err)
	}
	if !ok {
		t.Error("Expected Delete to return true")
	}
	if !strings.Contains(lastMethod, "deleteMessage") {
		t.Errorf("Expected deleteMessage method, got %s", lastMethod)
	}
	unescapedBody2, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(unescapedBody2, "111") || !strings.Contains(unescapedBody2, "222") {
		t.Errorf("Expected ChatID and MessageID in request body, got %s", unescapedBody2)
	}

	ok, err = msg.Unload()
	if err != nil || !ok {
		t.Errorf("Unload failed: err=%v, ok=%v", err, ok)
	}
}

func TestLocalRandStr(t *testing.T) {
	s1 := localRandStr(10)
	s2 := localRandStr(10)
	if len(s1) != 10 || len(s2) != 10 || s1 == s2 {
		t.Error("localRandStr failed")
	}
}
