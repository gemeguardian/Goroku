package goroku

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
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

	// Durable last-valid sibling retained after a second generation.
	lastValid := lastValidPath(ConfigPath)
	if _, err := os.Stat(lastValid); err != nil {
		t.Fatalf("config last-valid missing after successful saves: %v", err)
	}
}

func TestSaveConfigKeyAtomicFailureLeavesPrevious(t *testing.T) {
	tempDir := t.TempDir()
	oldConfigPath := ConfigPath
	goodPath := filepath.Join(tempDir, "test_config.json")
	ConfigPath = goodPath
	defer func() { ConfigPath = oldConfigPath }()

	if !SaveConfigKey("api_id", int64(1)) {
		t.Fatal("initial save failed")
	}
	if !SaveConfigKey("api_hash", "keep-me") {
		t.Fatal("second save failed")
	}
	before, err := os.ReadFile(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLastValid, err := os.ReadFile(lastValidPath(goodPath))
	if err != nil {
		t.Fatal(err)
	}

	// Parent path is a regular file, so CreateTemp/atomic write cannot proceed.
	blocker := filepath.Join(tempDir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	ConfigPath = filepath.Join(blocker, "config.json")
	if SaveConfigKey("api_id", int64(2)) {
		t.Fatal("SaveConfigKey unexpectedly succeeded for invalid parent path")
	}

	ConfigPath = goodPath
	after, err := os.ReadFile(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("previous config changed after failed save")
	}
	afterLastValid, err := os.ReadFile(lastValidPath(goodPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeLastValid, afterLastValid) {
		t.Fatalf("last-valid changed after failed save")
	}
	if got := GetConfigKey("api_hash"); got != "keep-me" {
		t.Fatalf("api_hash = %v, want keep-me", got)
	}
}

func TestSaveConfigKeyDoesNotClobberCorruptPrimary(t *testing.T) {
	tempDir := t.TempDir()
	oldConfigPath := ConfigPath
	ConfigPath = filepath.Join(tempDir, "test_config.json")
	defer func() { ConfigPath = oldConfigPath }()

	if err := os.WriteFile(ConfigPath, []byte(`{corrupt`), 0600); err != nil {
		t.Fatal(err)
	}
	if SaveConfigKey("api_id", int64(42)) {
		t.Fatal("SaveConfigKey clobbered corrupt primary without recovery")
	}
	content, err := os.ReadFile(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{corrupt` {
		t.Fatalf("corrupt primary was rewritten: %q", content)
	}
}

func TestSaveConfigKeyRecoversFromLastValid(t *testing.T) {
	tempDir := t.TempDir()
	oldConfigPath := ConfigPath
	ConfigPath = filepath.Join(tempDir, "test_config.json")
	defer func() { ConfigPath = oldConfigPath }()

	good, err := json.MarshalIndent(map[string]any{"api_hash": "from-backup", "keep": true}, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath+".last-valid", good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath, []byte(`{corrupt`), 0600); err != nil {
		t.Fatal(err)
	}

	if !SaveConfigKey("api_id", int64(7)) {
		t.Fatal("SaveConfigKey failed to recover from last-valid")
	}
	if got := GetConfigKey("api_hash"); got != "from-backup" {
		t.Fatalf("api_hash = %v, want from-backup", got)
	}
	if got := GetConfigKey("api_id"); got != float64(7) {
		t.Fatalf("api_id = %v, want 7", got)
	}
	if got := GetConfigKey("keep"); got != true {
		t.Fatalf("keep = %v, want true", got)
	}
}

func TestGetConfigKeyRecoversFromLastValid(t *testing.T) {
	tempDir := t.TempDir()
	oldConfigPath := ConfigPath
	ConfigPath = filepath.Join(tempDir, "test_config.json")
	defer func() { ConfigPath = oldConfigPath }()

	good, err := json.MarshalIndent(map[string]any{"token": "secret"}, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath+".last-valid", good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath, []byte(`{corrupt`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := GetConfigKey("token"); got != "secret" {
		t.Fatalf("GetConfigKey recovered value = %v, want secret", got)
	}
}

func TestGetConfigKeyRecoversFromLastValidWhenPrimaryUnreadable(t *testing.T) {
	tempDir := t.TempDir()
	oldConfigPath := ConfigPath
	ConfigPath = filepath.Join(tempDir, "test_config.json")
	defer func() { ConfigPath = oldConfigPath }()

	good, err := json.MarshalIndent(map[string]any{"token": "from-unreadable"}, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath+".last-valid", good, 0600); err != nil {
		t.Fatal(err)
	}
	// Directory primary path: ReadFile fails with a non-IsNotExist error.
	if err := os.Mkdir(ConfigPath, 0700); err != nil {
		t.Fatal(err)
	}
	if got := GetConfigKey("token"); got != "from-unreadable" {
		t.Fatalf("GetConfigKey unreadable recovery = %v, want from-unreadable", got)
	}
}
