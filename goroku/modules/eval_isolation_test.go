package modules

import (
	"strings"
	"testing"
	"time"

	"goroku/goroku"
	"goroku/goroku/evalstats"
)

// withShortEvalTimeout shrinks the eval budget so a hung eval is reachable in a
// test without waiting out the production 15s.
func withShortEvalTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	original := yaegiEvalTimeout
	yaegiEvalTimeout = d
	t.Cleanup(func() { yaegiEvalTimeout = original })
}

// An eval that never returns must time out rather than hang the command, and
// the abandoned goroutine must be counted so the operator can see it.
func TestEvalInfiniteLoopTimesOutAndIsCounted(t *testing.T) {
	withShortEvalTimeout(t, 300*time.Millisecond)
	m := &Eval{Base: goroku.Base{Client: goroku.NewCustomTelegramClient(123)}}

	before := evalstats.Stuck()
	start := time.Now()
	_, _, _, err := m.runYaegiEval(&goroku.Message{}, "for { _ = 1 }")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("infinite loop returned without error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want a timeout", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("infinite eval took %v to report a timeout", elapsed)
	}
	if got := evalstats.Stuck(); got <= before {
		t.Fatalf("stuck eval count = %d, was %d: the abandoned goroutine is invisible", got, before)
	}
}

// The abandoned goroutine goes on printing forever. Its output must stay
// bounded instead of growing until the process is killed.
func TestEvalOutputIsBoundedNotUnbounded(t *testing.T) {
	withShortEvalTimeout(t, 300*time.Millisecond)
	m := &Eval{Base: goroku.Base{Client: goroku.NewCustomTelegramClient(123)}}

	// Printing in a loop is exactly the shape that used to grow a plain
	// bytes.Buffer without limit while out() read it concurrently.
	_, stdout, stderr, err := m.runYaegiEval(&goroku.Message{}, `for { println("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx") }`)
	if err == nil {
		t.Fatal("printing infinite loop returned without error")
	}
	// The truncation marker is appended on read, so allow a little slack.
	if len(stdout) > externalOutputLimit+64 {
		t.Fatalf("stdout = %d bytes, want at most the %d byte limit", len(stdout), externalOutputLimit)
	}
	if len(stderr) > externalOutputLimit+64 {
		t.Fatalf("stderr = %d bytes, want at most the %d byte limit", len(stderr), externalOutputLimit)
	}

	// Give the abandoned goroutine a moment to keep writing; the buffer must
	// still be readable and still bounded (this is what -race exercises).
	time.Sleep(200 * time.Millisecond)
	if len(stdout) > externalOutputLimit+64 {
		t.Fatalf("stdout grew to %d bytes after the timeout", len(stdout))
	}
}

// Output over the limit is truncated with a marker rather than dropped or
// allowed to consume memory.
func TestEvalOutputOverLimitIsTruncated(t *testing.T) {
	b := newBoundedBuffer(16)
	if _, err := b.Write([]byte(strings.Repeat("a", 1000))); err != nil {
		t.Fatal(err)
	}
	if !b.Truncated() {
		t.Fatal("bounded buffer did not report truncation")
	}
	got := b.String()
	if !strings.HasPrefix(got, strings.Repeat("a", 16)) {
		t.Fatalf("buffer content = %q, want the first 16 bytes kept", got)
	}
	if !strings.Contains(got, "[output truncated]") {
		t.Fatalf("buffer content = %q, want a truncation marker", got)
	}
	if len(got) > 16+len("\n[output truncated]") {
		t.Fatalf("buffer kept %d bytes despite a 16 byte limit", len(got))
	}
}

// The whole point of the in-process model: the live client is reachable.
// If this ever stops holding, the docs in yaegi_runtime.go and SECURITY.md are
// wrong again.
func TestEvalReachesTheLiveClient(t *testing.T) {
	client := goroku.NewCustomTelegramClient(123)
	client.SetUsername("example")
	m := &Eval{Base: goroku.Base{Client: client}}

	res, _, _, err := m.runYaegiEval(&goroku.Message{}, "client.Username()")
	if err != nil {
		t.Fatalf("client.Username: %v", err)
	}
	if res != "example" {
		t.Fatalf("client.Username = %q, want the live value", res)
	}
}
