package inline

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
)

func TestGallery(t *testing.T) {
	client := &mockTelegramClient{}
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
	bot, err := tgbotapi.NewBotAPIWithClient("mock_token", tgbotapi.APIEndpoint, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("Failed to create mock bot: %v", err)
	}

	im := &InlineManager{
		bot:                  bot,
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

	// 1. Create Gallery with []string and string caption
	galleryMsg, err := im.Gallery(
		int64(123),
		[]string{"https://img1.com", "https://img2.com"},
		"Caption of gallery",
	)
	if err != nil {
		t.Fatalf("Gallery failed: %v", err)
	}

	unitID := galleryMsg.UnitID
	unit, ok := im.GetUnit(unitID)
	if !ok {
		t.Fatal("Expected gallery unit to exist")
	}

	// 2. Check slideshow callback
	cbSlideshow := CallbackQuery{
		Data:    "gal_" + unitID + "_slideshow",
		Manager: im,
	}
	// Trigger HandleGalleryCallback or call customMap handler directly
	im.mu.RLock()
	btnSlideshow, ok := im.customMap[cbSlideshow.Data]
	im.mu.RUnlock()
	if !ok || btnSlideshow.Handler == nil {
		t.Fatal("Expected slideshow handler")
	}
	err = btnSlideshow.Handler(cbSlideshow)
	if err != nil {
		t.Errorf("Slideshow handler failed: %v", err)
	}
	if unit.Interval != 7*time.Second {
		t.Errorf("Expected interval 7s, got %v", unit.Interval)
	}

	// Disable slideshow
	err = btnSlideshow.Handler(cbSlideshow)
	if err != nil {
		t.Errorf("Slideshow handler disable failed: %v", err)
	}
	if unit.Interval != 0 {
		t.Errorf("Expected interval 0, got %v", unit.Interval)
	}

	// 3. Check paging handler callback
	cbPage := CallbackQuery{
		Data:    "gal_" + unitID + "_1",
		Manager: im,
	}
	handled := im.HandleGalleryCallback(cbPage)
	if !handled {
		t.Error("Expected HandleGalleryCallback to return true for page action")
	}

	// Invalid action or prefix
	if im.HandleGalleryCallback(CallbackQuery{Data: "invalid", Manager: im}) {
		t.Error("Expected false for invalid prefix")
	}
	if im.HandleGalleryCallback(CallbackQuery{Data: "gal_" + unitID + "_invalid", Manager: im}) {
		t.Error("Expected false for invalid page action")
	}
}

func TestListAndQueryGallery(t *testing.T) {
	client := &mockTelegramClient{}
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
	bot, err := tgbotapi.NewBotAPIWithClient("mock_token", tgbotapi.APIEndpoint, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("Failed to create mock bot: %v", err)
	}

	im := &InlineManager{
		bot:                  bot,
		client:               client,
		units:                make(map[string]*Unit),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
		errorEvents:          make(map[string]chan error),
		QueryGalleries:       make(map[string]QueryGalleryItem),
		markupTTL:            5 * time.Minute,
	}

	client.sendResultFunc = func(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error) {
		signalInlineCompletion(im)
		return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 789}}}}, nil
	}

	// 1. List component test
	listMsg, err := im.List(int64(123), []string{"Item 1", "Item 2", "Item 3"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	unitID := listMsg.UnitID
	cbPage := CallbackQuery{
		Data:    "lst_" + unitID + "_1",
		Manager: im,
	}
	handled := im.HandleListCallback(cbPage)
	if !handled {
		t.Error("Expected HandleListCallback to return true for list paging")
	}

	if im.HandleListCallback(CallbackQuery{Data: "invalid", Manager: im}) {
		t.Error("Expected false for invalid list prefix")
	}

	// 2. QueryGallery test
	items := []QueryGalleryItem{
		{
			Title:       "QG Title",
			Description: "QG Desc",
			NextHandler: []string{"https://img.com"},
		},
	}
	err = im.QueryGallery("q_123", items)
	if err != nil {
		t.Fatalf("QueryGallery failed: %v", err)
	}

	// Find registered id
	var registeredID string
	im.mu.Lock()
	for id := range im.QueryGalleries {
		registeredID = id
	}
	im.mu.Unlock()

	item, ok := im.PopQueryGallery(registeredID)
	if !ok || item.Title != "QG Title" {
		t.Errorf("Expected QG Title item, got %v", item)
	}
}
