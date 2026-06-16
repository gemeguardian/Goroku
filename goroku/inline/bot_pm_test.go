package inline

import (
	"testing"
)

func TestFSMState(t *testing.T) {
	im := &InlineManager{
		fsm: make(map[string]string),
	}

	// 1. Initially false / not set
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false for unset FSM, got %v", state)
	}

	// 2. Set state
	if !im.SetFSMState(42, "state_1") {
		t.Error("SetFSMState failed")
	}
	if state := im.GetFSMState(42); state != "state_1" {
		t.Errorf("Expected 'state_1', got %v", state)
	}

	// 3. Clear state by setting to nil
	if !im.SetFSMState(42, nil) {
		t.Error("SetFSMState with nil failed")
	}
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false after clearing state, got %v", state)
	}

	// 4. Clear state by setting to empty string
	im.SetFSMState(42, "state_2")
	im.SetFSMState(42, "")
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false after setting to empty string, got %v", state)
	}

	// 5. Clear state by setting to "false"
	im.SetFSMState(42, "state_3")
	im.SetFSMState(42, "false")
	if state := im.GetFSMState(42); state != false {
		t.Errorf("Expected false after setting to 'false', got %v", state)
	}
}
