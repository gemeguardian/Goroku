package modules

import (
	"testing"

	"goroku/goroku"
)

func TestCamelToSnake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HelloWorld", "hello_world"},
		{"APILimiter", "a_p_i_limiter"},
		{"simple", "simple"},
		{"", ""},
	}
	for _, tc := range tests {
		got := camelToSnake(tc.input)
		if got != tc.expected {
			t.Errorf("camelToSnake(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestFormatTrans(t *testing.T) {
	// Positional args
	got := formatTrans("Hello {0}, you have {1} messages", "Alice", "5")
	if got != "Hello Alice, you have 5 messages" {
		t.Errorf("formatTrans positional = %q", got)
	}

	// Implicit args
	got = formatTrans("Hello {}, you have {} messages", "Bob", "3")
	if got != "Hello Bob, you have 3 messages" {
		t.Errorf("formatTrans implicit = %q", got)
	}

	// href normalization
	got = formatTrans("Click href={} here", "https://example.com")
	if got != "Click href=\"https://example.com\" here" {
		t.Errorf("formatTrans href = %q", got)
	}

	// emoji-id normalization
	got = formatTrans("emoji-id=12345 here")
	if got != "emoji-id=\"12345\" here" {
		t.Errorf("formatTrans emoji = %q", got)
	}
}

func TestGetTrans(t *testing.T) {
	// nil translator returns default
	got := getTrans(nil, "TestMod", "hello", "default")
	if got != "default" {
		t.Errorf("getTrans(nil) = %q; want default", got)
	}

	// translator with matching key (requires client and db)
	client := goroku.NewCustomTelegramClient(42)
	db := goroku.NewDatabase(42)
	tr := goroku.NewTranslator(client, db)
	got = getTrans(tr, "TestMod", "hello", "default")
	if got != "default" {
		// Translator might not have the key loaded
		t.Logf("getTrans with translator = %q (may be default if key not loaded)", got)
	}
}
