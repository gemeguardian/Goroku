package goroku

import (
	"testing"
)

func TestTranslateLayout(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ghbdtn", "привет"},
		{"/pfnm", "|зать"},
		{"привет", "ghbdtn"},
	}

	for _, tc := range tests {
		got := translateLayout(tc.input)
		if got != tc.expected {
			t.Errorf("translateLayout(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestWatcherTagsMatch(t *testing.T) {
	db := NewDatabase(42)
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	cd := NewCommandDispatcher(modules, client, db)

	// Case 1: OnlyPM
	metaPM := CommandMeta{OnlyPM: true}
	msgPM := &Message{RawText: "hello", IsPrivate: true}
	msgGroup := &Message{RawText: "hello", IsGroup: true}

	if !cd.watcherTagsMatch(msgPM, metaPM) {
		t.Error("Expected watcherTagsMatch=true for OnlyPM with private message")
	}
	if cd.watcherTagsMatch(msgGroup, metaPM) {
		t.Error("Expected watcherTagsMatch=false for OnlyPM with group message")
	}

	// Case 2: StartsWith / EndsWith / Contains
	metaString := CommandMeta{
		StartsWith: "hello",
		Contains:   "world",
		EndsWith:   "!",
	}
	msgMatch := &Message{RawText: "hello world!"}
	msgMismatch := &Message{RawText: "hello world"}

	if !cd.watcherTagsMatch(msgMatch, metaString) {
		t.Error("Expected string rules to match")
	}
	if cd.watcherTagsMatch(msgMismatch, metaString) {
		t.Error("Expected string rules to not match due to EndsWith")
	}

	// Case 3: FromID / ChatID
	metaID := CommandMeta{
		FromID: []int64{100, 200},
		ChatID: []int64{-999},
	}
	msgIDMatch := &Message{SenderID: 200, ChatID: -999}
	msgIDMismatch := &Message{SenderID: 300, ChatID: -999}

	if !cd.watcherTagsMatch(msgIDMatch, metaID) {
		t.Error("Expected FromID/ChatID match")
	}
	if cd.watcherTagsMatch(msgIDMismatch, metaID) {
		t.Error("Expected FromID mismatch")
	}
}

func TestHandleGrep(t *testing.T) {
	db := NewDatabase(42)
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	cd := NewCommandDispatcher(modules, client, db)

	// Grep query
	msg := &Message{Text: "logs output | grep error", RawText: "logs output | grep error"}
	msg = cd.handleGrep(msg)
	if msg.GrepQuery != "error" || msg.GrepInvert != false || msg.Text != "logs output " {
		t.Errorf("Grep query extraction failed: %+v", msg)
	}

	// Inverted Grep query
	msgInv := &Message{Text: "logs output | grep -v debug", RawText: "logs output | grep -v debug"}
	msgInv = cd.handleGrep(msgInv)
	if msgInv.GrepQuery != "debug" || msgInv.GrepInvert != true || msgInv.Text != "logs output " {
		t.Errorf("Inverted grep extraction failed: %+v", msgInv)
	}

	// Cut lines
	msgCut := &Message{Text: "logs output | cut 10", RawText: "logs output | cut 10"}
	msgCut = cd.handleGrep(msgCut)
	if msgCut.CutLines != 10 || msgCut.Text != "logs output " {
		t.Errorf("Cut lines extraction failed: %+v", msgCut)
	}

	// Split output
	msgSplit := &Message{Text: "logs output | split", RawText: "logs output | split"}
	msgSplit = cd.handleGrep(msgSplit)
	if !msgSplit.SplitOutput || msgSplit.Text != "logs output " {
		t.Errorf("Split output flag failed: %+v", msgSplit)
	}
}
