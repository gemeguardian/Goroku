package utils

import (
	"reflect"
	"testing"
)

type dummyDoc struct {
	MimeType string
}

type dummyMsgWithMedia struct {
	Media interface{}
}

func TestGetTopic(t *testing.T) {
	if got := GetTopic("hello"); got != 0 {
		t.Errorf("Expected 0, got %d", got)
	}
}

func TestGetMimeType(t *testing.T) {
	if got := GetMimeType(nil); got != "" {
		t.Errorf("Expected empty, got %q", got)
	}

	doc := &dummyDoc{MimeType: "video/mp4"}
	if got := GetMimeType(doc); got != "video/mp4" {
		t.Errorf("Expected video/mp4, got %q", got)
	}

	nonDoc := "not-a-struct"
	if got := GetMimeType(nonDoc); got != "application/octet-stream" {
		t.Errorf("Expected default, got %q", got)
	}
}

func TestSmartSplit(t *testing.T) {
	text := "This is a very long string that we need to split nicely."
	// Length 15
	// Chunk 1: "This is a very " (split at last space before 15) -> "This is a very "
	// Chunk 2: "long string " (split at last space before 15) -> "long string "
	// Chunk 3: "that we need to " -> "that we need to "
	// Chunk 4: "split nicely." -> "split nicely."
	parts := SmartSplit(text, 15)
	expected := []string{"This is a very ", "long string ", "that we need ", "to split ", "nicely."}
	if !reflect.DeepEqual(parts, expected) {
		t.Errorf("SmartSplit failed: expected %v, got %v", expected, parts)
	}

	// Test hard cut
	partsHard := SmartSplit("abcdefghijkl", 5)
	expectedHard := []string{"abcde", "fghij", "kl"}
	if !reflect.DeepEqual(partsHard, expectedHard) {
		t.Errorf("SmartSplit hard cut failed: expected %v, got %v", expectedHard, partsHard)
	}

	// Test newline split preference
	partsNL := SmartSplit("hello\nworld", 7)
	expectedNL := []string{"hello\n", "world"}
	if !reflect.DeepEqual(partsNL, expectedNL) {
		t.Errorf("SmartSplit newline failed: expected %v, got %v", expectedNL, partsNL)
	}
}

func TestArraySum(t *testing.T) {
	arrays := [][]string{
		{"a", "b"},
		{"c"},
		{"d", "e"},
	}
	got := ArraySum(arrays)
	expected := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("ArraySum failed: expected %v, got %v", expected, got)
	}
}

func TestAnswer(t *testing.T) {
	got := Answer("ping", "pong")
	expected := "Response: ping -> Output: pong"
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

func TestGetMessageLink(t *testing.T) {
	// Username link
	linkUser := GetMessageLink(0, 12345, "MyBot")
	if linkUser != "https://t.me/MyBot/12345" {
		t.Errorf("Expected username link, got %q", linkUser)
	}

	// Private chat link
	linkChat := GetMessageLink(999, 12345, "")
	if linkChat != "https://t.me/c/999/12345" {
		t.Errorf("Expected chat link, got %q", linkChat)
	}
}

func TestCensor(t *testing.T) {
	got := Censor("Token 123456789:AAF123456789_abcdefghijklmnopqrstuv")
	if got != "Token [REDACTED]" {
		t.Errorf("Censor failed, got %q", got)
	}
}

func TestExtractURLs(t *testing.T) {
	text := "Check https://google.com or http://github.com/OvyFlash/telegram-bot-api now!"
	got := ExtractURLs(text)
	expected := []string{"https://google.com", "http://github.com/OvyFlash/telegram-bot-api"}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("ExtractURLs failed: expected %v, got %v", expected, got)
	}
}

func TestHasMedia(t *testing.T) {
	if HasMedia(nil) {
		t.Error("Expected false for nil")
	}

	var m *dummyMsgWithMedia
	if HasMedia(m) {
		t.Error("Expected false for nil pointer")
	}

	msgNoMedia := &dummyMsgWithMedia{Media: nil}
	if HasMedia(msgNoMedia) {
		t.Error("Expected false for nil Media field")
	}

	var dummyMedia interface{} = "some-media"
	msgWithMedia := &dummyMsgWithMedia{Media: dummyMedia}
	if !HasMedia(msgWithMedia) {
		t.Error("Expected true for non-nil Media field")
	}
}
