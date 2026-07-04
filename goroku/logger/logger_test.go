package logger

import (
	"os"
	"testing"
)

func TestInit(t *testing.T) {
	// Default (info) level.
	_ = os.Unsetenv("GOROKU_DEBUG")
	Init()
	if L() == nil {
		t.Fatal("Logger should not be nil after Init")
	}

	// Debug level.
	_ = os.Setenv("GOROKU_DEBUG", "1")
	defer func() { _ = os.Unsetenv("GOROKU_DEBUG") }()
	Init()
	if L() == nil {
		t.Fatal("Logger should not be nil after Init with GOROKU_DEBUG=1")
	}
}
