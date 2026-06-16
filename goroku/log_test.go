package goroku

import (
	"testing"
)

func TestFilterLogMessage(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Failed to fetch updates", true},
		{"failed to fetch updates from telegram server", true},
		{"Some Sleep logs here", true},
		{"normal startup log", false},
		{"random error log", false},
	}

	for _, tc := range tests {
		got := FilterLogMessage(tc.input)
		if got != tc.expected {
			t.Errorf("FilterLogMessage(%q) = %t; want %t", tc.input, got, tc.expected)
		}
	}
}

func TestCoreOverwriteError(t *testing.T) {
	err := &CoreOverwriteError{Message: "critical override"}
	if err.Error() != "critical override" {
		t.Errorf("Expected 'critical override', got %q", err.Error())
	}
}

func TestRunContext(t *testing.T) {
	called := false
	RunContext(nil, func() {
		called = true
	})
	if !called {
		t.Error("Expected context function to be executed")
	}
}
