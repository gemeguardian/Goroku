package goroku

import (
	"testing"
)

func TestGetLogChatID(t *testing.T) {
	// Without DB
	client := &CustomTelegramClient{TGID: 42}
	if got := client.GetLogChatID(); got != 0 {
		t.Errorf("GetLogChatID without DB = %d; want 0", got)
	}

	// With DB but no channel_id
	db := NewDatabase(42)
	client.GorokuDB = db
	if got := client.GetLogChatID(); got != 0 {
		t.Errorf("GetLogChatID without channel_id = %d; want 0", got)
	}

	// With float64 channel_id
	db.data["goroku.forums"] = map[string]interface{}{
		"channel_id": float64(1234567890),
	}
	if got := client.GetLogChatID(); got != 1234567890 {
		t.Errorf("GetLogChatID with float64 = %d; want 1234567890", got)
	}

	// With int64 channel_id
	db.data["goroku.forums"] = map[string]interface{}{
		"channel_id": int64(9876543210),
	}
	if got := client.GetLogChatID(); got != 9876543210 {
		t.Errorf("GetLogChatID with int64 = %d; want 9876543210", got)
	}

	// With int channel_id
	db.data["goroku.forums"] = map[string]interface{}{
		"channel_id": int(42),
	}
	if got := client.GetLogChatID(); got != 42 {
		t.Errorf("GetLogChatID with int = %d; want 42", got)
	}
}

func TestCheckBotNilInline(t *testing.T) {
	client := &CustomTelegramClient{TGID: 42}
	ok, err := client.CheckBot("test_bot")
	if err == nil {
		t.Error("Expected error when inline manager is nil")
	}
	if ok {
		t.Error("Expected false when inline manager is nil")
	}
}
