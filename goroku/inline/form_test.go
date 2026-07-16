package inline

import (
	"goroku/goroku/chatref"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestFormInvalidChatRollsBackUnitAndUnloads(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, nil, &testInlineModules{})
	var unloads atomic.Int32
	if _, err := im.Form("invalid", struct{}{}, [][]Button{{{Data: "button"}}}, WithOnUnload(func() { unloads.Add(1) })); err == nil {
		t.Fatal("Form accepted an invalid chat")
	}
	if unloads.Load() != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", unloads.Load())
	}
	im.mu.RLock()
	defer im.mu.RUnlock()
	if len(im.units) != 0 || len(im.customMap) != 0 || len(im.buttonUnits) != 0 {
		t.Fatal("invalid Form leaked registered state")
	}
}

type mockTelegramClient struct {
	inlineQueryCalled bool
	sendResultCalled  bool
	inlineQueryFunc   func(botUsername string, query string, chatID int64) (*tg.MessagesBotResults, error)
	sendResultFunc    func(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error)
}

func (m *mockTelegramClient) InlineQuery(botUsername string, query string, chatID int64) (*tg.MessagesBotResults, error) {
	m.inlineQueryCalled = true
	if m.inlineQueryFunc != nil {
		return m.inlineQueryFunc(botUsername, query, chatID)
	}
	return &tg.MessagesBotResults{
		QueryID: 999,
		Results: []tg.BotInlineResultClass{
			&tg.BotInlineResult{ID: "res_123"},
		},
	}, nil
}
func (m *mockTelegramClient) SendInlineBotResult(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error) {
	m.sendResultCalled = true
	if m.sendResultFunc != nil {
		return m.sendResultFunc(chatID, queryID, resultID, replyToMsgID)
	}
	return &tg.Updates{}, nil
}
func (m *mockTelegramClient) TGIDValue() int64 { return 0 }
func (m *mockTelegramClient) SendMessage(chat chatref.ChatRef, message string) (any, error) {
	return nil, nil
}
func (m *mockTelegramClient) CreateGorokuFolder(botID int64) error                   { return nil }
func (m *mockTelegramClient) InviteBotToChannel(channelPeer tg.InputPeerClass) error { return nil }
func (m *mockTelegramClient) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error  { return nil }
func (m *mockTelegramClient) GetSecurityManager() SecurityChecker                    { return nil }

type mockDeletableSourceMessage struct {
	deleted bool
	isOut   bool
}

func (m *mockDeletableSourceMessage) Delete() error {
	m.deleted = true
	return nil
}

func (m *mockDeletableSourceMessage) IsOut() bool {
	return m.isOut
}

func (m *mockDeletableSourceMessage) GetChatID() int64 {
	return 123
}

func (m *mockDeletableSourceMessage) GetReplyToMsgID() int64 {
	return 456
}

// signalInlineCompletion sends a nil error to every pending inline event channel.
// It is called synchronously from the mock SendInlineBotResult because InvokeUnit
// registers the channel before calling SendInlineBotResult and then waits on it.
func signalInlineCompletion(im *InlineManager) {
	im.mu.Lock()
	defer im.mu.Unlock()
	for _, ch := range im.errorEvents {
		select {
		case ch <- nil:
		default:
		}
	}
}

func TestFormSuccessful(t *testing.T) {
	client := &mockTelegramClient{}
	im := &InlineManager{
		client:               client,
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
		errorEvents:          make(map[string]chan error),
		markupTTL:            5 * time.Minute,
	}

	client.sendResultFunc = func(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error) {
		signalInlineCompletion(im)
		return &tg.Updates{
			Updates: []tg.UpdateClass{
				&tg.UpdateNewMessage{
					Message: &tg.Message{ID: 789},
				},
			},
		}, nil
	}

	// 1. Successful Form with int64 chat ID
	formMsg, err := im.Form("Hello Form", int64(123), [][]Button{
		{{Text: "Click", Data: "click_data"}},
	})
	if err != nil {
		t.Fatalf("Form failed: %v", err)
	}
	if formMsg.UnitID == "" {
		t.Error("Expected formMsg.UnitID to be populated")
	}

	// 2. Successful Form with deletable source message
	sourceMsg := &mockDeletableSourceMessage{isOut: true}
	_, err = im.Form("Form with delete", sourceMsg, [][]Button{
		{{Text: "Ok", Data: "ok_data"}},
	})
	if err != nil {
		t.Fatalf("Form with deletable failed: %v", err)
	}
	if !sourceMsg.deleted {
		t.Error("Expected source message to be deleted")
	}

	// 3. Error: client does not implement TelegramClient
	im.client = &badTelegramClient{}
	_, err = im.Form("Error Form", int64(123), nil)
	if err == nil {
		t.Error("Expected error because client does not implement TelegramClient")
	}
}

// badTelegramClient implements InlineUserBot but not TelegramClient.
type badTelegramClient struct{}

func (b *badTelegramClient) TGIDValue() int64 { return 0 }
func (b *badTelegramClient) SendMessage(chat chatref.ChatRef, message string) (any, error) {
	return nil, nil
}
func (b *badTelegramClient) CreateGorokuFolder(botID int64) error                   { return nil }
func (b *badTelegramClient) InviteBotToChannel(channelPeer tg.InputPeerClass) error { return nil }
func (b *badTelegramClient) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error  { return nil }
func (b *badTelegramClient) GetSecurityManager() SecurityChecker                    { return nil }

func TestCreateForm(t *testing.T) {
	client := &mockTelegramClient{}
	im := &InlineManager{
		client:               client,
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
		errorEvents:          make(map[string]chan error),
		markupTTL:            5 * time.Minute,
	}

	client.sendResultFunc = func(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error) {
		signalInlineCompletion(im)
		return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 789}}}}, nil
	}

	// 1. CreateForm short args
	resEmpty := im.CreateForm("text")
	if resEmpty != nil {
		t.Errorf("Expected nil for insufficient args, got %v", resEmpty)
	}

	// 2. CreateForm invalid text type
	resInvalid := im.CreateForm(123, int64(456))
	if resInvalid != nil {
		t.Errorf("Expected nil for invalid text arg, got %v", resInvalid)
	}

	// 3. CreateForm successful path
	resForm := im.CreateForm("Hello Legacy", int64(456), [][]Button{{{Text: "A", Data: "a"}}})
	if resForm == nil {
		t.Fatal("Expected created form object, got nil")
	}
	formMsg, ok := resForm.(*InlineMessage)
	if !ok || formMsg.UnitID == "" {
		t.Errorf("Expected *InlineMessage with unitID, got %v", resForm)
	}

	// 4. CreateForm failure path (e.g. client error)
	im.client = &badTelegramClient{}
	resErr := im.CreateForm("Hello Fail", int64(456))
	if resErr != nil {
		t.Errorf("Expected nil on form creation error, got %v", resErr)
	}
}
