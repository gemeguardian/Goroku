package goroku

import (
	"goroku/goroku/inline"
	"testing"
)

type mockCallingModule struct {
	im *inline.InlineManager
}

func (m *mockCallingModule) Call() string {
	return inline.DetectCallingModuleForTest(m.im)
}

func TestDetectCallingModule(t *testing.T) {
	im := &inline.InlineManager{}
	caller := &mockCallingModule{im: im}
	got := caller.Call()
	expected := "mockCallingModule"
	if got != expected {
		t.Errorf("detectCallingModule failed: expected %q, got %q", expected, got)
	}
}
