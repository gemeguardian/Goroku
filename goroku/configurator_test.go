package goroku

import (
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	io.Copy(&buf, r)
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
