package goroku

import (
	"testing"
)

func TestSecurityCheckDoesNotReloadRightsEveryCall(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]any{
		"owner":     []any{int64(42)},
		"all_users": []any{},
	}
	db.data["goroku.main"] = map[string]any{
		"command_prefixes": map[string]any{
			"999": []any{"."},
		},
	}

	sm := NewSecurityManager(&CustomTelegramClient{TGID: 42}, db)

	// Simulate prefixes being written after startup. Check() is a hot path and
	// must not run reloadRights()/cleanup on every message.
	db.data["goroku.main"]["command_prefixes"] = map[string]any{
		"999": []any{"."},
	}

	if !sm.Check(&Message{SenderID: 42, ChatID: 1, Out: true}, "ping") {
		t.Fatal("owner/outgoing message should pass security check")
	}

	raw, _ := db.Get("goroku.main", "command_prefixes", nil)
	prefixes, ok := raw.(map[string]any)
	if !ok {
		prefRaw, _ := db.Get("goroku.main", "command_prefixes", nil)
		t.Fatalf("command_prefixes has unexpected type: %T", prefRaw)
	}
	if _, ok := prefixes["999"]; !ok {
		t.Fatal("Check() reloaded rights and cleaned command_prefixes on the hot path")
	}
}

func TestSecurityCheckWhitelistsOwnerAndBlacklistsUsers(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL | EVERYONE),
		"masks": map[string]any{
			"everyone_cmd": float64(EVERYONE),
		},
	}
	db.data["goroku.main"] = map[string]any{
		"blacklist_users": []any{int64(200)}, // Non-owner 200 is blacklisted
	}

	sm := NewSecurityManager(&CustomTelegramClient{TGID: 42}, db)
	sm.Stop() // Stop background reloader tick to avoid leak

	// Owner/Self (SenderID = 42) -> should pass
	if !sm.Check(&Message{SenderID: 42, ChatID: 1}, "ping") {
		t.Fatal("Owner should pass security check")
	}

	// Outgoing message (Out = true) from anyone -> should pass
	if !sm.Check(&Message{SenderID: 999, ChatID: 1, Out: true}, "ping") {
		t.Fatal("Outgoing message should pass security check")
	}

	// Non-owner 200 is blacklisted -> should fail everyone_cmd even though it is EVERYONE
	if sm.Check(&Message{SenderID: 200, ChatID: 1}, "everyone_cmd") {
		t.Fatal("Blacklisted non-owner should NOT pass security check")
	}

	// Non-owner (SenderID = 300) who is NOT blacklisted -> should pass everyone_cmd
	if !sm.Check(&Message{SenderID: 300, ChatID: 1}, "everyone_cmd") {
		t.Fatal("Non-blacklisted user should pass everyone_cmd")
	}
}

func TestSecurityCheckEveryoneAndPMMasks(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL | EVERYONE), // Allow overrides to work
		"masks": map[string]any{
			"everyone_cmd": float64(EVERYONE),
			"pm_only_cmd":  float64(PM),
		},
	}

	sm := NewSecurityManager(&CustomTelegramClient{TGID: 42}, db)
	sm.Stop()

	// everyone_cmd can be run by anyone (SenderID = 200)
	if !sm.Check(&Message{SenderID: 200, ChatID: 1}, "everyone_cmd") {
		t.Fatal("everyone_cmd should be accessible to anyone")
	}

	// pm_only_cmd in a group chat (ChatID = -100) -> should fail
	if sm.Check(&Message{SenderID: 200, ChatID: -100, IsGroup: true}, "pm_only_cmd") {
		t.Fatal("pm_only_cmd should fail in a group chat")
	}

	// pm_only_cmd in a PM (ChatID = 200, IsPrivate = true) -> should succeed
	if !sm.Check(&Message{SenderID: 200, ChatID: 200, IsPrivate: true}, "pm_only_cmd") {
		t.Fatal("pm_only_cmd should succeed in a private chat")
	}
}

func TestAllMaskIncludesEveryone(t *testing.T) {
	if (ALL & EVERYONE) == 0 {
		t.Fatal("ALL must include EVERYONE")
	}
}

func TestSecurityCheckSudoMask(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"sudo":          []any{int64(100)},
		"bounding_mask": float64(ALL),
		"masks": map[string]any{
			"sudo_cmd": float64(SUDO),
		},
	}
	db.data["goroku.main"] = map[string]any{}

	sm := NewSecurityManager(&CustomTelegramClient{TGID: 42}, db)
	sm.Stop()

	if !sm.Check(&Message{SenderID: 100, ChatID: 1}, "sudo_cmd") {
		t.Fatal("sudo user should pass command with SUDO mask")
	}
	if sm.Check(&Message{SenderID: 101, ChatID: 1}, "sudo_cmd") {
		t.Fatal("non-sudo user should not pass command with SUDO mask")
	}
}

func TestSecurityCheckTsecRules(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(OWNER), // Default OWNER only
		"tsec_user": []any{
			map[string]any{
				"target":    float64(100),
				"rule_type": "command",
				"rule":      "test_cmd",
				"expires":   float64(0),
			},
		},
		"tsec_chat": []any{
			map[string]any{
				"target":    float64(-999),
				"rule_type": "command",
				"rule":      "test_cmd",
				"expires":   float64(0),
			},
		},
	}

	sm := NewSecurityManager(&CustomTelegramClient{TGID: 42}, db)
	sm.Stop()

	// Non-owner 100 runs other cmd -> fails
	if sm.Check(&Message{SenderID: 100, ChatID: 1}, "other_cmd") {
		t.Fatal("tsec user should only run authorized command")
	}

	// Non-owner 100 runs test_cmd -> passes
	if !sm.Check(&Message{SenderID: 100, ChatID: 1}, "test_cmd") {
		t.Fatal("tsec user should pass for test_cmd")
	}

	// Non-owner 200 runs test_cmd in other chat -> fails
	if sm.Check(&Message{SenderID: 200, ChatID: 1}, "test_cmd") {
		t.Fatal("other user in other chat should fail test_cmd")
	}

	// Non-owner 200 runs test_cmd in tsec_chat -999 -> passes
	if !sm.Check(&Message{SenderID: 200, ChatID: -999}, "test_cmd") {
		t.Fatal("any user in tsec_chat should pass for test_cmd")
	}
}

func TestIntFromInterface(t *testing.T) {
	tests := []struct {
		input    any
		fallback int
		want     int
	}{
		{42, 0, 42},
		{int64(42), 0, 42},
		{float64(42.9), 0, 42},
		{"42", 0, 42},
		{"invalid", 99, 99},
		{nil, 99, 99},
		{true, 99, 99},
	}

	for _, tc := range tests {
		got := intFromInterface(tc.input, tc.fallback)
		if got != tc.want {
			t.Errorf("intFromInterface(%v, %d) = %d; want %d", tc.input, tc.fallback, got, tc.want)
		}
	}
}
