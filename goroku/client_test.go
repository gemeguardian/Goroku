package goroku

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type typedTestRequest struct {
	typeID uint32
}

func (t typedTestRequest) Encode(*bin.Buffer) error { return nil }
func (t typedTestRequest) TypeID() uint32           { return t.typeID }

func TestGetRawChannelID(t *testing.T) {
	tests := []struct {
		input    int64
		expected int64
	}{
		{-1001234567890, 1234567890}, // channel ID
		{-1001, 1001},                // group ID (not a channel)
		{123, 123},                   // user ID
		{0, 0},
	}
	for _, tc := range tests {
		got := getRawChannelID(tc.input)
		if got != tc.expected {
			t.Errorf("getRawChannelID(%d) = %d; want %d", tc.input, got, tc.expected)
		}
	}
}

func TestToBotAPIChatID(t *testing.T) {
	client := &CustomTelegramClient{}
	tests := []struct {
		input    int64
		expected int64
	}{
		{1234567890, -1000000000000 - 1234567890},
		{1, -1000000000001},
	}
	for _, tc := range tests {
		got := client.ToBotAPIChatID(tc.input)
		if got != tc.expected {
			t.Errorf("ToBotAPIChatID(%d) = %d; want %d", tc.input, got, tc.expected)
		}
	}
}

func TestIsSameChat(t *testing.T) {
	if !isSameChat(-1001234567890, -1001234567890) {
		t.Error("Expected same chat for identical IDs")
	}
	if !isSameChat(-1001234567890, 1234567890) {
		t.Error("Expected same chat for channel ID and raw ID")
	}
	if isSameChat(-1001234567890, -1009876543210) {
		t.Error("Expected different chats for different IDs")
	}
}

func TestForbiddenInvokerBlocksConfiguredConstructor(t *testing.T) {
	called := false
	invoker := &forbiddenInvoker{
		parent: &mockInvoker{onInvoke: func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			called = true
			return nil
		}},
		client: newForbiddenTestClient(123),
	}

	if err := invoker.Invoke(context.Background(), typedTestRequest{typeID: 123}, nil); err == nil {
		t.Fatal("expected forbidden constructor error")
	}
	if called {
		t.Fatal("parent invoker should not be called for forbidden constructor")
	}
}

func TestResolveRequestPeerReturnsResolvedPeer(t *testing.T) {
	client := &CustomTelegramClient{}
	peer := &tg.InputPeerUser{UserID: 123, AccessHash: 456}

	got, err := client.resolveRequestPeer(ChatRefPeer(peer))
	if err != nil {
		t.Fatalf("resolveRequestPeer returned error: %v", err)
	}
	if got != peer {
		t.Fatal("resolveRequestPeer should return the provided peer")
	}
}

func TestResolveRequestPeerDoesNotFallbackToBareUser(t *testing.T) {
	client := &CustomTelegramClient{}

	peer, err := client.resolveRequestPeer(ChatRefID(123))
	if err == nil {
		t.Fatal("expected resolve error for disconnected client")
	}
	if peer != nil {
		t.Fatalf("expected nil peer on resolve error, got %T", peer)
	}
}

func TestGetSentMessageID(t *testing.T) {
	// UpdateNewMessage
	resp1 := &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewMessage{
				Message: &tg.Message{ID: 42},
			},
		},
	}
	if got := GetSentMessageID(resp1); got != 42 {
		t.Errorf("GetSentMessageID(UpdateNewMessage) = %d; want 42", got)
	}

	// UpdateNewChannelMessage
	resp2 := &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{
				Message: &tg.Message{ID: 99},
			},
		},
	}
	if got := GetSentMessageID(resp2); got != 99 {
		t.Errorf("GetSentMessageID(UpdateNewChannelMessage) = %d; want 99", got)
	}

	// UpdateShortSentMessage
	resp3 := &tg.UpdateShortSentMessage{ID: 77}
	if got := GetSentMessageID(resp3); got != 77 {
		t.Errorf("GetSentMessageID(UpdateShortSentMessage) = %d; want 77", got)
	}

	// Unknown type
	if got := GetSentMessageID("unknown"); got != 0 {
		t.Errorf("GetSentMessageID(unknown) = %d; want 0", got)
	}
}
