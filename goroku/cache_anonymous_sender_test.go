package goroku

import (
	"testing"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

// Anonymous admins and channel-signed posts arrive with a *tg.PeerChannel
// FromID. Only *tg.PeerUser was handled, so those messages had SenderID 0 and
// could be named by no policy at all — not blacklisted, not given a tsec rule.
func TestAnonymousAdminSenderIsIdentified(t *testing.T) {
	client := NewCustomTelegramClient(42)

	msg := client.buildMessageFromTG(&tg.Message{
		ID:      7,
		Message: "hi",
		PeerID:  &tg.PeerChannel{ChannelID: 100500},
		FromID:  &tg.PeerChannel{ChannelID: 100500},
	})

	if !msg.SenderIsChannel {
		t.Fatal("channel sender was not recognized")
	}
	if want := cache.TelegramChannelChatID(100500); msg.SenderChannelID != want {
		t.Fatalf("SenderChannelID = %d, want %d", msg.SenderChannelID, want)
	}
	if msg.SenderID != 0 {
		t.Fatalf("SenderID = %d, want 0: a channel is not a user", msg.SenderID)
	}
}

func TestBasicGroupSenderIsIdentified(t *testing.T) {
	client := NewCustomTelegramClient(42)

	msg := client.buildMessageFromTG(&tg.Message{
		ID:      8,
		Message: "hi",
		PeerID:  &tg.PeerChat{ChatID: 777},
		FromID:  &tg.PeerChat{ChatID: 777},
	})

	if !msg.SenderIsChannel || msg.SenderChannelID != -777 {
		t.Fatalf("SenderIsChannel = %v, SenderChannelID = %d, want true/-777", msg.SenderIsChannel, msg.SenderChannelID)
	}
}

// A normal user sender must be unaffected.
func TestUserSenderStaysAUser(t *testing.T) {
	client := NewCustomTelegramClient(42)

	msg := client.buildMessageFromTG(&tg.Message{
		ID:      9,
		Message: "hi",
		PeerID:  &tg.PeerChannel{ChannelID: 100500},
		FromID:  &tg.PeerUser{UserID: 4321},
	})

	if msg.SenderID != 4321 {
		t.Fatalf("SenderID = %d, want 4321", msg.SenderID)
	}
	if msg.SenderIsChannel {
		t.Fatal("a user sender was reported as a channel")
	}
}
