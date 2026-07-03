package logger

import (
	"os"
	"testing"
)

func TestInit(t *testing.T) {
	// Default (info) level.
	os.Unsetenv("GOROKU_DEBUG")
	Init()
	if L() == nil {
		t.Fatal("Logger should not be nil after Init")
	}

	// Debug level.
	os.Setenv("GOROKU_DEBUG", "1")
	defer os.Unsetenv("GOROKU_DEBUG")
	Init()
	if L() == nil {
		t.Fatal("Logger should not be nil after Init with GOROKU_DEBUG=1")
	}
}
