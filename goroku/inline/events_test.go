package inline

import (
	"bytes"
	"github.com/gotd/td/tg"
	"goroku/goroku/chatref"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

type mockOwnerClient struct {
	ownerID int64
}

func (m *mockOwnerClient) TGIDValue() int64 { return m.ownerID }
func (m *mockOwnerClient) SendMessage(chat chatref.ChatRef, message string) (chatref.SentMessage, error) {
	return nil, nil
}
func (m *mockOwnerClient) CreateGorokuFolder(botID int64) error                   { return nil }
func (m *mockOwnerClient) InviteBotToChannel(channelPeer tg.InputPeerClass) error { return nil }
func (m *mockOwnerClient) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error  { return nil }
func (m *mockOwnerClient) GetSecurityManager() SecurityChecker                    { return nil }

func TestEventsHandleUpdateInlineQuery(t *testing.T) {
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
		bot:                  bot,
		client:               &mockOwnerClient{ownerID: 42},
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
	}

	// 1. InlineQuery: Empty query (Help query)
	lastMethod = ""
	im.HandleUpdate(tgbotapi.Update{
		InlineQuery: &tgbotapi.InlineQuery{
			ID:   "iq_empty",
			From: &tgbotapi.User{ID: 42},
		},
	})
	if lastMethod != "" {
		t.Errorf("Expected no request for empty query without modules, got %s", lastMethod)
	}

	// 2. InlineQuery: Input button
	im.customMap["input_btn"] = Button{
		Input: "Transfer val",
	}
	im.HandleUpdate(tgbotapi.Update{
		InlineQuery: &tgbotapi.InlineQuery{
			ID:    "iq_input",
			From:  &tgbotapi.User{ID: 42},
			Query: "input_btn some_value",
		},
	})
	if !strings.Contains(lastMethod, "answerInlineQuery") {
		t.Errorf("Expected answerInlineQuery method, got %s", lastMethod)
	}
	unescapedBody, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(unescapedBody, "Transferring") {
		t.Errorf("Expected Transferring in body, got %s", unescapedBody)
	}

	// 3. InlineQuery: Stored Unit Query
	im.units["unit_abc"] = &Unit{
		ID:      "unit_abc",
		Type:    "form",
		Text:    "Form text",
		Buttons: [][]Button{{{Text: "A", Data: "a"}}},
	}
	im.HandleUpdate(tgbotapi.Update{
		InlineQuery: &tgbotapi.InlineQuery{
			ID:    "iq_unit",
			From:  &tgbotapi.User{ID: 42},
			Query: "unit_abc",
		},
	})
	if !strings.Contains(lastMethod, "answerInlineQuery") {
		t.Errorf("Expected answerInlineQuery method, got %s", lastMethod)
	}
	unescapedBody2, _ := url.QueryUnescape(lastBody)
	if !strings.Contains(unescapedBody2, "Form") {
		t.Errorf("Expected Form in body, got %s", unescapedBody2)
	}
}

func TestEventsHandleUpdateCallbackQuery(t *testing.T) {
	var lastMethod string
	_ = lastMethod

	transport := &mockRoundTripper{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			lastMethod = req.URL.Path
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
		client:               &mockOwnerClient{ownerID: 42},
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
	}

	handlerCalled := false
	handlerDone := make(chan struct{})
	im.customMap["btn_custom"] = Button{
		Handler: func(c CallbackQuery) error {
			handlerCalled = true
			close(handlerDone)
			return nil
		},
	}

	// 1. CallbackQuery: custom handler
	im.HandleUpdate(tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb_custom",
			From: &tgbotapi.User{ID: 42},
			Data: "btn_custom",
		},
	})
	select {
	case <-handlerDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected custom button handler to be called")
	}
	if !handlerCalled {
		t.Error("Expected custom button handler to be called")
	}

	// 2. CallbackQuery: Gallery Callback
	im.units["unit_gal"] = &Unit{
		ID:    "unit_gal",
		Type:  "gallery",
		Pages: []string{"https://img.com"},
	}
	im.HandleUpdate(tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb_gal",
			From: &tgbotapi.User{ID: 42},
			Data: "gal_unit_gal_0",
		},
	})
	if !strings.Contains(lastMethod, "answerCallbackQuery") {
		t.Errorf("Expected answerCallbackQuery, got %s", lastMethod)
	}

	// 3. CallbackQuery: List Callback
	im.units["unit_lst"] = &Unit{
		ID:    "unit_lst",
		Type:  "list",
		Pages: []string{"Item 1"},
	}
	im.HandleUpdate(tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb_lst",
			From: &tgbotapi.User{ID: 42},
			Data: "lst_unit_lst_0",
		},
	})
	if !strings.Contains(lastMethod, "answerCallbackQuery") {
		t.Errorf("Expected answerCallbackQuery, got %s", lastMethod)
	}
}

func TestEventsHandleUpdateChosenInlineResult(t *testing.T) {
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
		client:               &mockOwnerClient{ownerID: 42},
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
		errorEvents:          make(map[string]chan error),
	}

	// 1. ChosenInlineResult: Input Button Handler
	inputHandlerCalled := false
	inputHandlerDone := make(chan struct{})
	im.customMap["input_btn"] = Button{
		Input: "Transfer",
		InputHandler: func(c CallbackQuery, val string) error {
			inputHandlerCalled = true
			if val != "value_abc" {
				t.Errorf("Expected input value 'value_abc', got %s", val)
			}
			close(inputHandlerDone)
			return nil
		},
	}
	im.buttonUnits["input_btn"] = "unit_123"

	im.HandleUpdate(tgbotapi.Update{
		ChosenInlineResult: &tgbotapi.ChosenInlineResult{
			ResultID:        "res_123",
			From:            &tgbotapi.User{ID: 42},
			Query:           "input_btn value_abc",
			InlineMessageID: "msg_inline_999",
		},
	})
	select {
	case <-inputHandlerDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected input handler to be called")
	}
	if !inputHandlerCalled {
		t.Error("Expected input handler to be called")
	}

	// 2. ChosenInlineResult: Stored Unit Result
	im.units["unit_xyz"] = &Unit{
		ID:   "unit_xyz",
		Type: "form",
	}
	errCh := make(chan error, 1)
	im.errorEvents["unit_xyz"] = errCh

	im.HandleUpdate(tgbotapi.Update{
		ChosenInlineResult: &tgbotapi.ChosenInlineResult{
			ResultID:        "res_456",
			From:            &tgbotapi.User{ID: 42},
			Query:           "unit_xyz",
			InlineMessageID: "inline_xyz_789",
		},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Expected nil on errCh, got %v", err)
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("Timeout waiting for errCh update")
	}

	im.mu.RLock()
	mappedInlineID := im.activeInlineMessages["unit_xyz"]
	im.mu.RUnlock()
	if mappedInlineID != "inline_xyz_789" {
		t.Errorf("Expected active inline message ID to be mapped, got %s", mappedInlineID)
	}
}
