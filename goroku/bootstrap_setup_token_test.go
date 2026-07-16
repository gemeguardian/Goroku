package goroku

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"goroku/goroku/web"
)

func TestRandomSetupTokenFailsClosedAndIsHex(t *testing.T) {
	token, err := randomSetupToken()
	if err != nil {
		t.Fatalf("randomSetupToken: %v", err)
	}
	if len(token) != 48 {
		t.Fatalf("expected 24-byte hex token (48 chars), got len=%d value=%q", len(token), token)
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token must be hex: %v", err)
	}
	// No UnixNano decimal fallback: hex-only, fixed width.
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("unexpected token char %q in %q", r, token)
		}
	}
}

func TestHasExistingTelegramSessionsIgnoresTempZero(t *testing.T) {
	dir := t.TempDir()
	if hasExistingTelegramSessions(dir) {
		t.Fatal("empty data root should report no sessions")
	}
	if err := os.WriteFile(filepath.Join(dir, "goroku-0.session"), []byte("tmp"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasExistingTelegramSessions(dir) {
		t.Fatal("goroku-0.session must not count as completed setup")
	}
	if err := os.WriteFile(filepath.Join(dir, "goroku-42.session"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasExistingTelegramSessions(dir) {
		t.Fatal("real session file must count as existing")
	}
}

func TestSetupCompletedMarkerBlocksEnvRearmHelpers(t *testing.T) {
	dir := t.TempDir()
	if web.SetupCompleted(dir) {
		t.Fatal("fresh data root must not be setup-completed")
	}
	if err := os.WriteFile(filepath.Join(dir, "goroku-setup-completed"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !web.SetupCompleted(dir) {
		t.Fatal("marker file must mark setup completed")
	}
}
