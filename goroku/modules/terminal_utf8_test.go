package modules

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Terminal output is cut from the front at a byte offset. Cutting mid-rune
// produced invalid UTF-8 in the middle of a Cyrillic character, which Telegram
// then refused or rendered as a replacement glyph.
func TestSafeTailUTF8KeepsValidUTF8(t *testing.T) {
	// Every rune here is two bytes, so an odd cut always lands mid-rune.
	text := strings.Repeat("привет ", 500)

	for _, limit := range []int{1, 7, 101, 1023, 2047, 2048} {
		got := safeTailUTF8(text, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("safeTailUTF8(limit=%d) produced invalid UTF-8", limit)
		}
		if len(got) > limit {
			t.Fatalf("safeTailUTF8(limit=%d) returned %d bytes", limit, len(got))
		}
		if !strings.HasSuffix(text, got) {
			t.Fatalf("safeTailUTF8(limit=%d) did not return a tail of the input", limit)
		}
	}
}

func TestSafeTailUTF8ReturnsShortInputUnchanged(t *testing.T) {
	const text = "короткий вывод"
	if got := safeTailUTF8(text, 4096); got != text {
		t.Fatalf("safeTailUTF8 = %q, want the input unchanged", got)
	}
}

func TestSafeTruncateUTF8KeepsValidUTF8(t *testing.T) {
	text := strings.Repeat("привет ", 500)
	for _, limit := range []int{1, 7, 101, 2047} {
		got := safeTruncateUTF8(text, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("safeTruncateUTF8(limit=%d) produced invalid UTF-8", limit)
		}
		if len(got) > limit {
			t.Fatalf("safeTruncateUTF8(limit=%d) returned %d bytes", limit, len(got))
		}
	}
}

// The message the terminal actually sends must be valid UTF-8 end to end.
func TestBuildTerminalTextIsValidUTF8(t *testing.T) {
	m := &TerminalMod{}
	rc := 0
	text := m.buildTerminalText(
		"echo тест",
		strings.Repeat("многобайтовый вывод ", 500),
		strings.Repeat("ошибка ", 500),
		&rc,
		0,
		true,
	)
	if !utf8.ValidString(text) {
		t.Fatal("buildTerminalText produced invalid UTF-8 after truncation")
	}
}
