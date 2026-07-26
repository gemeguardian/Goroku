package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Under systemd or cron the root confirmation prompt can never be answered.
// The process used to die with a bare "refusing to run as root" and no
// indication of how to proceed.
func TestConfirmRootRunWithoutATerminalExplainsHowToProceed(t *testing.T) {
	_, err := confirmRootRun(strings.NewReader(""), false)
	if !errors.Is(err, errRootNoTerminal) {
		t.Fatalf("error = %v, want errRootNoTerminal", err)
	}
	for _, hint := range []string{"--root", "NO_SUDO=1", "unprivileged"} {
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("error message does not mention %q: %v", hint, err)
		}
	}
}

// systemd's StandardInput=null gives a character device that yields EOF, so the
// device check alone is not enough.
func TestConfirmRootRunTreatsImmediateEOFAsNoTerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devNull.Close() })

	if _, err := confirmRootRun(devNull, true); !errors.Is(err, errRootNoTerminal) {
		t.Fatalf("error = %v, want errRootNoTerminal for /dev/null", err)
	}
}

func TestConfirmRootRunAcceptsAnswers(t *testing.T) {
	noSudo, err := confirmRootRun(strings.NewReader("no_sudo\n"), true)
	if err != nil || !noSudo {
		t.Fatalf("no_sudo = (%v, %v), want (true, nil)", noSudo, err)
	}

	noSudo, err = confirmRootRun(strings.NewReader("force_insecure\n"), true)
	if err != nil || noSudo {
		t.Fatalf("force_insecure = (%v, %v), want (false, nil)", noSudo, err)
	}

	if _, err := confirmRootRun(strings.NewReader("nope\n"), true); err == nil {
		t.Fatal("an unrecognized answer was accepted")
	} else if errors.Is(err, errRootNoTerminal) {
		t.Fatalf("an answered prompt reported a missing terminal: %v", err)
	}
}

func TestStdinIsTerminalIsFalseForAPipe(t *testing.T) {
	original := os.Stdin
	t.Cleanup(func() { os.Stdin = original })

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	os.Stdin = reader
	if stdinIsTerminal() {
		t.Fatal("a pipe was reported as a terminal")
	}
}

func TestStdinIsTerminalIsFalseForAClosedHandle(t *testing.T) {
	original := os.Stdin
	t.Cleanup(func() { os.Stdin = original })

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	_ = reader.Close()

	os.Stdin = reader
	if stdinIsTerminal() {
		t.Fatal("a closed handle was reported as a terminal")
	}
}
