package goroku

import (
	"testing"
)

func TestMessageGetters(t *testing.T) {
	var m *Message
	// Verify nil safety
	if m.GetChatID() != 0 {
		t.Error("Expected 0 for nil message GetChatID")
	}
	if m.GetID() != 0 {
		t.Error("Expected 0 for nil message GetID")
	}
	if m.IsOut() != false {
		t.Error("Expected false for nil message IsOut")
	}
	if m.GetReplyToMsgID() != 0 {
		t.Error("Expected 0 for nil message GetReplyToMsgID")
	}

	m = &Message{
		ID:           123,
		ChatID:       456,
		Out:          true,
		ReplyToMsgID: 789,
	}

	if m.GetChatID() != 456 {
		t.Errorf("Expected 456, got %d", m.GetChatID())
	}
	if m.GetID() != 123 {
		t.Errorf("Expected 123, got %d", m.GetID())
	}
	if m.IsOut() != true {
		t.Error("Expected true, got false")
	}
	if m.GetReplyToMsgID() != 789 {
		t.Errorf("Expected 789, got %d", m.GetReplyToMsgID())
	}
}

func TestCustomTelegramClientInit(t *testing.T) {
	client := NewCustomTelegramClient(12345)
	if client.TGID != 12345 {
		t.Errorf("Expected TGID 12345, got %d", client.TGID)
	}

	// Verify caching map allocations
	if client.GorokuEntityCache == nil {
		t.Error("GorokuEntityCache was not initialized")
	}
	if client.GorokuPermsCache == nil {
		t.Error("GorokuPermsCache was not initialized")
	}

	// Test Disconnect on uninitialized raw client (should not panic)
	err := client.Disconnect()
	if err != nil {
		t.Errorf("Unexpected error on Disconnect: %v", err)
	}
}
