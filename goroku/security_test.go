package goroku

import (
	"testing"
)

func TestSecurityCheckDoesNotReloadRightsEveryCall(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]interface{}{
		"owner":     []interface{}{int64(42)},
		"all_users": []interface{}{},
	}
	db.data["goroku.main"] = map[string]interface{}{
		"command_prefixes": map[string]interface{}{
			"999": []interface{}{"."},
		},
	}

	sm := NewSecurityManager(&CustomTelegramClient{TGID: 42}, db)

	// Simulate prefixes being written after startup. Check() is a hot path and
	// must not run reloadRights()/cleanup on every message.
	db.data["goroku.main"]["command_prefixes"] = map[string]interface{}{
		"999": []interface{}{"."},
	}

	if !sm.Check(&Message{SenderID: 42, ChatID: 1, Out: true}, "ping") {
		t.Fatal("owner/outgoing message should pass security check")
	}

	prefixes, ok := db.Get("goroku.main", "command_prefixes", nil).(map[string]interface{})
	if !ok {
		t.Fatalf("command_prefixes has unexpected type: %T", db.Get("goroku.main", "command_prefixes", nil))
	}
	if _, ok := prefixes["999"]; !ok {
		t.Fatal("Check() reloaded rights and cleaned command_prefixes on the hot path")
	}
}

func TestSecurityCheckWhitelistsOwnerAndBlacklistsUsers(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]interface{}{
		"owner":         []interface{}{int64(42)},
		"all_users":     []interface{}{},
		"bounding_mask": float64(ALL | EVERYONE),
		"masks": map[string]interface{}{
			"everyone_cmd": float64(EVERYONE),
		},
	}
	db.data["goroku.main"] = map[string]interface{}{
		"blacklist_users": []interface{}{int64(200)}, // Non-owner 200 is blacklisted
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
	db.data["goroku.security"] = map[string]interface{}{
		"owner":         []interface{}{int64(42)},
		"all_users":     []interface{}{},
		"bounding_mask": float64(ALL | EVERYONE), // Allow overrides to work
		"masks": map[string]interface{}{
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

func TestSecurityCheckTsecRules(t *testing.T) {
	db := NewDatabase(42)
	db.data["goroku.security"] = map[string]interface{}{
		"owner":         []interface{}{int64(42)},
		"all_users":     []interface{}{},
		"bounding_mask": float64(OWNER), // Default OWNER only
		"tsec_user": []interface{}{
			map[string]interface{}{
				"target":    float64(100),
				"rule_type": "command",
				"rule":      "test_cmd",
				"expires":   float64(0),
			},
		},
		"tsec_chat": []interface{}{
			map[string]interface{}{
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

