package goroku

import (
	"context"
	"errors"
	"strings"
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
	RunContext(context.TODO(), func() {
		called = true
	})
	if !called {
		t.Error("Expected context function to be executed")
	}
}

func TestTranslatorInitCheckedReportsDatabaseLifecycle(t *testing.T) {
	translator := &Translator{
		db:      NewDatabase(1),
		data:    make(map[string]any),
		rawData: make(map[string]map[string]any),
		packs:   "langpacks",
	}
	if _, err := translator.InitChecked(); !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized translator error = %v", err)
	}

	db := initializedTestDatabase(t, NewDatabase(2))
	if err := db.Close(nil); err != nil {
		t.Fatal(err)
	}
	translator.db = db
	if _, err := translator.InitChecked(); !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("closed translator error = %v", err)
	}
}

func TestTracebackUnitUsesRegisteredLoaderModule(t *testing.T) {
	unit := tracebackUnit(NewCustomTelegramClient(42), telegramTraceback{})
	if unit.Module != "Loader" {
		t.Fatalf("traceback callback module = %q, want Loader", unit.Module)
	}
	if !unit.DisableSecurity || len(unit.Buttons) != 1 || len(unit.Buttons[0]) != 1 {
		t.Fatalf("unexpected traceback callback unit: %#v", unit)
	}
}

func TestParseTelegramLogRecordsFormatsErrorCard(t *testing.T) {
	record := `{"timestamp":"2026-07-17 12:00:00","level":"ERROR","caller":"goroku/example.go:42","msg":"bridge request failed","error":"access blocked","stacktrace":"goroutine 1 [running]:\ngoroku/example.run()\n\t/root/eblan/Goroku/goroku/example.go:42 +0x1","module":"Bridge"}`
	normal, tracebacks := parseTelegramLogRecords([]string{record})
	if len(normal) != 0 || len(tracebacks) != 1 {
		t.Fatalf("parsed normal=%#v tracebacks=%#v", normal, tracebacks)
	}
	for _, expected := range []string{"🔴", "🎯 Source:", "❓ Error:", "💭 Message:", "module=Bridge"} {
		if !strings.Contains(tracebacks[0].summary, expected) {
			t.Errorf("summary %q does not contain %q", tracebacks[0].summary, expected)
		}
	}
	if !strings.Contains(tracebacks[0].full, "<pre>👉 /root/eblan/Goroku/goroku/example.go:42 in goroku/example.run()</pre>") {
		t.Errorf("full traceback is not frame-formatted: %q", tracebacks[0].full)
	}
}
