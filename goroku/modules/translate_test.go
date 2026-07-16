package modules

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

func utf16Offset(s, needle string, occurrence int) int {
	byteStart := 0
	for i := 0; i < occurrence; i++ {
		idx := strings.Index(s[byteStart:], needle)
		if idx < 0 {
			return -1
		}
		if i == occurrence-1 {
			return len(utf16.Encode([]rune(s[:byteStart+idx])))
		}
		byteStart += idx + len(needle)
	}
	return -1
}

func TestApplyCustomEmojiFallbackKeepsRepeatedEmojiIDs(t *testing.T) {
	original := "⬜️ Выбери\n▫️ only_text\n▫️ provider"
	translated := "⬜️ <b>Select</b>\n▫️ only_text\n▫️ provider"
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityCustomEmoji{Offset: utf16Offset(original, "⬜️", 1), Length: len(utf16.Encode([]rune("⬜️"))), DocumentID: 5350628475914971096},
		&tg.MessageEntityCustomEmoji{Offset: utf16Offset(original, "▫️", 1), Length: len(utf16.Encode([]rune("▫️"))), DocumentID: 5278497648389691517},
		&tg.MessageEntityCustomEmoji{Offset: utf16Offset(original, "▫️", 2), Length: len(utf16.Encode([]rune("▫️"))), DocumentID: 5278497648389691517},
	}

	got := applyCustomEmojiFallback(translated, original, entities)
	if strings.Count(got, "<tg-emoji") != 3 {
		t.Fatalf("expected 3 custom emoji tags, got %d in %q", strings.Count(got, "<tg-emoji"), got)
	}
	if strings.Count(got, "emoji-id=\"5278497648389691517\"") != 2 {
		t.Fatalf("expected repeated bullet emoji id twice, got %q", got)
	}
}

func TestEntitySpanAndClone(t *testing.T) {
	bold := &tg.MessageEntityBold{Offset: 5, Length: 10}
	offset, length, ok := entitySpan(bold)
	if !ok || offset != 5 || length != 10 {
		t.Errorf("entitySpan failed for MessageEntityBold: got offset=%d, length=%d, ok=%t", offset, length, ok)
	}

	cloned := cloneEntityWithSpan(bold, 2, 8)
	cBold, isBold := cloned.(*tg.MessageEntityBold)
	if !isBold || cBold.Offset != 2 || cBold.Length != 8 {
		t.Errorf("cloneEntityWithSpan failed for MessageEntityBold: got %T %+v", cloned, cloned)
	}
}

func TestSliceMessageEntities(t *testing.T) {
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 5, Length: 10},
		&tg.MessageEntityItalic{Offset: 20, Length: 5},
	}

	// Slice from 7 to 22 (payloadStart=7, payloadLen=15)
	// Bold should overlap: original span 5 to 15, sliced overlap 7 to 15 (offset 0 to 8 relative to slice)
	// Italic should overlap: original span 20 to 25, sliced overlap 20 to 22 (offset 13 to 15 relative to slice)
	sliced := sliceMessageEntities(entities, 7, 15)
	if len(sliced) != 2 {
		t.Fatalf("Expected 2 sliced entities, got %d", len(sliced))
	}

	bEntity, ok1 := sliced[0].(*tg.MessageEntityBold)
	iEntity, ok2 := sliced[1].(*tg.MessageEntityItalic)
	if !ok1 || bEntity.Offset != 0 || bEntity.Length != 8 {
		t.Errorf("Sliced bold entity mismatch: %+v", sliced[0])
	}
	if !ok2 || iEntity.Offset != 13 || iEntity.Length != 2 {
		t.Errorf("Sliced italic entity mismatch: %+v", sliced[1])
	}
}

func TestReplaceFirstOutsideCustomEmojiTag(t *testing.T) {
	s := "some <tg-emoji emoji-id=\"123\">apple</tg-emoji> apple"
	got := replaceFirstOutsideCustomEmojiTag(s, "apple", "orange")
	expected := "some <tg-emoji emoji-id=\"123\">apple</tg-emoji> orange"
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

func TestDescribeEntities(t *testing.T) {
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 5, Length: 10},
	}
	desc := describeEntities(entities)
	if !strings.Contains(desc, "MessageEntityBold") || !strings.Contains(desc, "offset=5") {
		t.Errorf("describeEntities output incorrect: %q", desc)
	}
}
