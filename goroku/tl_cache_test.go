package goroku

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestAnswerPlanUsesParsedPlainTextLength(t *testing.T) {
	raw := strings.Repeat("<tg-emoji emoji-id=5197195523794157505>▫️</tg-emoji>", 90)
	plain, _ := parseHTML(raw)
	if len(raw) <= 4000 {
		t.Fatalf("test setup raw html should exceed old raw limit, got %d", len(raw))
	}
	if telegramTextLen(plain) >= telegramMessageLimit {
		t.Fatalf("test setup parsed plain text should fit telegram limit, got %d", telegramTextLen(plain))
	}

	plan := planLongAnswer(raw, false)
	if plan.mode != answerModeDirect {
		t.Fatalf("expected direct answer for formatted text under parsed limit, got %v", plan.mode)
	}
}

func TestAnswerPlanUsesInlinePaginationUpToTenPages(t *testing.T) {
	text := strings.Repeat("a", telegramMessageLimit*2+100)
	plan := planLongAnswer(text, true)
	if plan.mode != answerModeInlineList {
		t.Fatalf("expected inline list for long text within ten pages, got %v", plan.mode)
	}
	if len(plan.pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(plan.pages))
	}
	for i, page := range plan.pages {
		plain, _ := parseHTML(page)
		if telegramTextLen(plain) > telegramMessageLimit {
			t.Fatalf("page %d exceeds limit: %d", i, telegramTextLen(plain))
		}
	}
}

func TestAnswerPlanFallsBackToFileOverTenPages(t *testing.T) {
	text := strings.Repeat("a", telegramMessageLimit*10+1)
	plan := planLongAnswer(text, true)
	if plan.mode != answerModeFile {
		t.Fatalf("expected file fallback for more than ten pages, got %v", plan.mode)
	}
}

func TestBuildMessageFromTGNormalizesChatIDsAndForwardFlag(t *testing.T) {
	client := NewCustomTelegramClient(1000)
	forwarded := &tg.Message{
		ID:      1,
		PeerID:  &tg.PeerChannel{ChannelID: 42},
		FromID:  &tg.PeerUser{UserID: 777},
		Message: "channel message",
	}
	forwarded.SetFwdFrom(tg.MessageFwdHeader{})

	channelMsg := client.buildMessageFromTG(forwarded)
	if channelMsg.ChatID != TelegramChannelChatID(42) {
		t.Fatalf("expected normalized channel chat id, got %d", channelMsg.ChatID)
	}
	if !channelMsg.IsChannel {
		t.Fatalf("expected IsChannel to be true")
	}
	if !channelMsg.IsForwarded {
		t.Fatalf("expected forwarded flag to follow gotd optional field flag")
	}

	group := &tg.Message{
		ID:      2,
		PeerID:  &tg.PeerChat{ChatID: 99},
		FromID:  &tg.PeerUser{UserID: 888},
		Message: "group message",
	}
	groupMsg := client.buildMessageFromTG(group)
	if groupMsg.ChatID != -99 {
		t.Fatalf("expected negative basic group chat id, got %d", groupMsg.ChatID)
	}
	if !groupMsg.IsGroup {
		t.Fatalf("expected IsGroup to be true")
	}
}
