package goroku

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = old

	var buf strings.Builder
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestTTYPrint(t *testing.T) {
	// 1. TTY = true
	gotTTY := captureStdout(func() {
		TTYPrint("\x1b[0;95mWelcome!\x1b[0m", true)
	})
	expectedTTY := "\x1b[0;95mWelcome!\x1b[0m\n"
	if gotTTY != expectedTTY {
		t.Errorf("TTYPrint(true) failed: expected %q, got %q", expectedTTY, gotTTY)
	}

	// 2. TTY = false (should strip ANSI colors)
	gotNoTTY := captureStdout(func() {
		TTYPrint("\x1b[0;95mWelcome!\x1b[0m", false)
	})
	expectedNoTTY := "Welcome!\n"
	if gotNoTTY != expectedNoTTY {
		t.Errorf("TTYPrint(false) failed: expected %q, got %q", expectedNoTTY, gotNoTTY)
	}
}

func TestConfigKeyRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	oldConfigPath := ConfigPath
	ConfigPath = filepath.Join(tempDir, "test_config.json")
	defer func() { ConfigPath = oldConfigPath }()

	// Get from non-existent file returns nil
	if got := GetConfigKey("missing"); got != nil {
		t.Errorf("GetConfigKey(missing) = %v; want nil", got)
	}

	// Save and get back
	if !SaveConfigKey("api_id", int64(12345)) {
		t.Error("SaveConfigKey failed")
	}
	if got := GetConfigKey("api_id"); got != float64(12345) {
		t.Errorf("GetConfigKey(api_id) = %v; want 12345", got)
	}

	// Save another key
	if !SaveConfigKey("api_hash", "abc123") {
		t.Error("SaveConfigKey failed for api_hash")
	}
	if got := GetConfigKey("api_hash"); got != "abc123" {
		t.Errorf("GetConfigKey(api_hash) = %v; want abc123", got)
	}

	// Overwrite existing key
	if !SaveConfigKey("api_id", int64(999)) {
		t.Error("SaveConfigKey overwrite failed")
	}
	if got := GetConfigKey("api_id"); got != float64(999) {
		t.Errorf("GetConfigKey(api_id) after overwrite = %v; want 999", got)
	}
}
