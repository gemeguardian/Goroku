package goroku

import (
	"errors"
	"testing"
)

func TestGetLogChatID(t *testing.T) {
	// Without DB
	client := NewCustomTelegramClient(42)
	if got := client.GetLogChatID(); got != 0 {
		t.Errorf("GetLogChatID without DB = %d; want 0", got)
	}

	// With DB but no channel_id
	db := initializedTestDatabase(t, NewDatabase(42))
	client.GorokuDB = db
	if got := client.GetLogChatID(); got != 0 {
		t.Errorf("GetLogChatID without channel_id = %d; want 0", got)
	}

	// With float64 channel_id
	db.data["goroku.forums"] = map[string]any{
		"channel_id": float64(1234567890),
	}
	if got := client.GetLogChatID(); got != 1234567890 {
		t.Errorf("GetLogChatID with float64 = %d; want 1234567890", got)
	}

	// With int64 channel_id
	db.data["goroku.forums"] = map[string]any{
		"channel_id": int64(9876543210),
	}
	if got := client.GetLogChatID(); got != 9876543210 {
		t.Errorf("GetLogChatID with int64 = %d; want 9876543210", got)
	}

	// With int channel_id
	db.data["goroku.forums"] = map[string]any{
		"channel_id": int(42),
	}
	if got := client.GetLogChatID(); got != 42 {
		t.Errorf("GetLogChatID with int = %d; want 42", got)
	}
}

func TestGetLogChatIDCheckedReportsLifecycleErrors(t *testing.T) {
	client := newTestClient(42, NewDatabase(42))
	if _, err := client.GetLogChatIDChecked(); !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized lookup error = %v", err)
	}

	db := initializedTestDatabase(t, NewDatabase(43))
	client.GorokuDB = db
	if err := db.Close(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetLogChatIDChecked(); !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("closed lookup error = %v", err)
	}
}

func TestCheckBotNilInline(t *testing.T) {
	client := NewCustomTelegramClient(42)
	ok, err := client.CheckBot("test_bot")
	if err == nil {
		t.Error("Expected error when inline manager is nil")
	}
	if ok {
		t.Error("Expected false when inline manager is nil")
	}
}
