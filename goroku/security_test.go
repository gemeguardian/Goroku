package goroku

import (
	"errors"
	"fmt"
	"testing"
)

func TestSecurityCheckDoesNotReloadRightsEveryCall(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":     []any{int64(42)},
		"all_users": []any{},
	}
	db.data["goroku.main"] = map[string]any{
		"command_prefixes": map[string]any{
			"999": []any{"."},
		},
	}

	sm := NewSecurityManager(NewCustomTelegramClient(42), db)

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
	db := initializedTestDatabase(t, NewDatabase(42))
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

	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
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
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL | EVERYONE), // Allow overrides to work
		"masks": map[string]any{
			"everyone_cmd": float64(EVERYONE),
			"pm_only_cmd":  float64(PM),
		},
	}

	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
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
	db := initializedTestDatabase(t, NewDatabase(42))
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

	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
	sm.Stop()

	if !sm.Check(&Message{SenderID: 100, ChatID: 1}, "sudo_cmd") {
		t.Fatal("sudo user should pass command with SUDO mask")
	}
	if sm.Check(&Message{SenderID: 101, ChatID: 1}, "sudo_cmd") {
		t.Fatal("non-sudo user should not pass command with SUDO mask")
	}
}

func TestSecurityCheckTsecRules(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
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

	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
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

func TestBareCommandMaskDoesNotTransferToDifferentModuleOwner(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
		"masks":         map[string]any{"shared": float64(EVERYONE)},
	}
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	client.Loader = modules
	first := &registrationTestModule{name: "first", commands: map[string]CommandHandler{"shared": testHandler("first")}}
	if err := modules.RegisterModule(first); err != nil {
		t.Fatal(err)
	}
	if owner := db.GetStringMap("goroku.security", "mask_owners", nil)["shared"]; owner != "first" {
		t.Fatalf("legacy mask owner = %q, want first", owner)
	}
	sm := NewSecurityManager(client, db)
	t.Cleanup(sm.Stop)
	msg := &Message{SenderID: 100, ChatID: 100, IsPrivate: true}
	if !sm.Check(msg, "shared") {
		t.Fatal("legacy bare mask did not apply to its first observed owner")
	}
	if err := modules.UnloadModule("first"); err != nil {
		t.Fatal(err)
	}
	second := &registrationTestModule{name: "second", commands: map[string]CommandHandler{"shared": testHandler("second")}}
	if err := modules.RegisterModule(second); err != nil {
		t.Fatal(err)
	}
	if sm.Check(msg, "shared") {
		t.Fatal("bare mask silently transferred to a different owner")
	}
	masks := db.GetStringMap("goroku.security", "masks", nil)
	masks["second.shared"] = "8192"
	db.SetStringMap("goroku.security", "masks", masks)
	if !sm.Check(msg, "shared") {
		t.Fatal("owner-qualified mask did not authorize the changed owner")
	}
}

func TestSecurityRulePersistenceFailuresDoNotPublishState(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":     []any{int64(42)},
		"all_users": []any{int64(42)},
		"tsec_user": []any{},
		"tsec_chat": []any{},
	}
	db.data["goroku.main"] = map[string]any{"command_prefixes": map[string]any{}}
	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
	t.Cleanup(sm.Stop)

	failure := errors.New("injected security write failure")
	db.writeLocal = func(string, []byte) error { return failure }
	if err := sm.AddRule("user", 100, "command", "ping", 0); !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("AddRule error = %v, want persistence failure", err)
	}
	if len(sm.GetUserRules()) != 0 || sm.IsUserInAllUsers(100) {
		t.Fatal("failed add published rule or all_users state")
	}

	db.writeLocal = writeFileAtomic
	if err := sm.AddRule("user", 100, "command", "ping", 0); err != nil {
		t.Fatal(err)
	}
	db.writeLocal = func(string, []byte) error { return fmt.Errorf("remove: %w", failure) }
	removed, err := sm.RemoveRule("user", 100, "ping")
	if removed || !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("RemoveRule = (%v, %v), want false and persistence failure", removed, err)
	}
	if len(sm.GetUserRules()) != 1 || !sm.IsUserInAllUsers(100) {
		t.Fatal("failed remove changed rule or all_users state")
	}

	if added, err := sm.AddOwner(200); added || !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("AddOwner = (%v, %v), want false and persistence failure", added, err)
	}
	if sm.IsOwner(200) {
		t.Fatal("failed owner add changed authorization state")
	}
	groups := map[string]SecurityGroup{
		"operators": {Users: []int64{300}, Permissions: []map[string]any{{"rule_type": "command", "rule": "ping"}}},
	}
	if err := sm.ApplySgroups(groups); !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("ApplySgroups error = %v, want persistence failure", err)
	}
	if sm.CheckTsec(300, "ping") {
		t.Fatal("failed group write changed authorization state")
	}
}

func TestSecurityCommittedWarningsPublishAuthorizationState(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":     []any{int64(42)},
		"all_users": []any{int64(42)},
		"tsec_user": []any{},
		"tsec_chat": []any{},
	}
	db.data["goroku.main"] = map[string]any{"command_prefixes": map[string]any{}}
	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
	t.Cleanup(sm.Stop)
	cause := errors.New("post-rename security warning")
	installPostRenameWarning(t, db, cause)

	err := sm.AddRule("user", 100, "command", "ping", 0)
	if err != nil {
		t.Fatalf("AddRule returned a post-rename warning: %v", err)
	}
	assertCommittedWarning(t, db.DurabilityWarning(), cause)
	if !sm.CheckTsec(100, "ping") || !sm.IsUserInAllUsers(100) {
		t.Fatal("committed rule warning did not publish authorization state")
	}

	removed, err := sm.RemoveRule("user", 100, "ping")
	if !removed || err != nil {
		t.Fatalf("committed removal = (%v, %v), want logical success", removed, err)
	}
	assertCommittedWarning(t, db.DurabilityWarning(), cause)
	if sm.CheckTsec(100, "ping") || sm.IsUserInAllUsers(100) {
		t.Fatal("committed rule removal warning did not publish authorization state")
	}

	added, err := sm.AddOwner(200)
	if !added || err != nil {
		t.Fatalf("committed owner add = (%v, %v), want logical success", added, err)
	}
	assertCommittedWarning(t, db.DurabilityWarning(), cause)
	if !sm.IsOwner(200) {
		t.Fatal("committed owner warning did not publish authorization state")
	}

	groups := map[string]SecurityGroup{
		"operators": {Users: []int64{300}, Permissions: []map[string]any{{"rule_type": "command", "rule": "ping"}}},
	}
	err = sm.ApplySgroups(groups)
	if err != nil {
		t.Fatalf("ApplySgroups returned a post-rename warning: %v", err)
	}
	assertCommittedWarning(t, db.DurabilityWarning(), cause)
	if !sm.CheckTsec(300, "ping") || !sm.IsUserInAllUsers(300) {
		t.Fatal("committed group warning did not publish authorization state")
	}
}

// newDelegationTestSecurity registers a stand-in for the GorokuSecurity module
// so module-scoped rules have a real registration to match against, and returns
// a manager reading the supplied security state.
func newDelegationTestSecurity(t *testing.T, security map[string]any) *SecurityManager {
	t.Helper()
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = security
	db.data["goroku.main"] = map[string]any{}

	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	client.Loader = modules
	mod := &registrationTestModule{
		name: "GorokuSecurity",
		commands: map[string]CommandHandler{
			"owneradd":  testHandler("owneradd"),
			"ownerlist": testHandler("ownerlist"),
			"sudolist":  testHandler("sudolist"),
		},
		metas: map[string]CommandMeta{"owneradd": {OnlyOwner: true}},
	}
	if err := modules.RegisterModule(mod); err != nil {
		t.Fatal(err)
	}
	sm := NewSecurityManager(client, db)
	t.Cleanup(sm.Stop)
	return sm
}

// A module-scoped delegation of a privileged module used to carry .owneradd
// with it, which let the delegate write themselves into the owner list and from
// there reach .eval and the session file.
func TestModuleRuleDoesNotDelegatePrivilegedModuleOwnerCommands(t *testing.T) {
	sm := newDelegationTestSecurity(t, map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
		"tsec_user": []any{
			map[string]any{"target": float64(100), "rule_type": "module", "rule": "GorokuSecurity", "expires": float64(0)},
			map[string]any{"target": float64(101), "rule_type": "command", "rule": "sudolist", "expires": float64(0)},
		},
	})

	moduleDelegate := &Message{SenderID: 100, ChatID: 100, IsPrivate: true}
	if sm.Check(moduleDelegate, "owneradd") {
		t.Fatal("module-scoped rule on GorokuSecurity granted owneradd: privilege escalation")
	}
	if !sm.Check(moduleDelegate, "ownerlist") {
		t.Fatal("module-scoped rule stopped granting the read-only ownerlist")
	}

	commandDelegate := &Message{SenderID: 101, ChatID: 101, IsPrivate: true}
	if !sm.Check(commandDelegate, "sudolist") {
		t.Fatal("explicit command-scoped rule stopped working")
	}
}

// The same deny applies to sgroups, which reach checkCommand by a second path.
func TestSgroupModuleRuleDoesNotDelegatePrivilegedModuleOwnerCommands(t *testing.T) {
	sm := newDelegationTestSecurity(t, map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
	})
	if err := sm.ApplySgroups(map[string]SecurityGroup{
		"helpers": {
			Name:        "helpers",
			Users:       []int64{100},
			Permissions: []map[string]any{{"rule_type": "module", "rule": "GorokuSecurity"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	delegate := &Message{SenderID: 100, ChatID: 100, IsPrivate: true}
	if sm.Check(delegate, "owneradd") {
		t.Fatal("sgroup module permission granted owneradd: privilege escalation")
	}
	if !sm.Check(delegate, "ownerlist") {
		t.Fatal("sgroup module permission stopped granting the read-only ownerlist")
	}
}

// checkTsec feeds the nickname exemption, so it has to deny the same rules.
func TestCheckTsecDoesNotDelegatePrivilegedModuleOwnerCommands(t *testing.T) {
	sm := newDelegationTestSecurity(t, map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
		"tsec_user": []any{
			map[string]any{"target": float64(100), "rule_type": "module", "rule": "GorokuSecurity", "expires": float64(0)},
		},
	})

	if sm.CheckTsec(100, "owneradd") {
		t.Fatal("checkTsec granted owneradd through a module-scoped rule")
	}
	if !sm.CheckTsec(100, "ownerlist") {
		t.Fatal("checkTsec stopped granting the read-only ownerlist")
	}
}

// A command the bounding mask leaves unreachable must stay unreachable: a tsec
// rule may not grant more than getBoundingMask() allows.
func TestBoundingMaskAlsoBoundsDelegatedRules(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(OWNER),
		"masks":         map[string]any{"clipped_cmd": float64(EVERYONE)},
		"tsec_user": []any{
			map[string]any{"target": float64(100), "rule_type": "command", "rule": "clipped_cmd", "expires": float64(0)},
			map[string]any{"target": float64(100), "rule_type": "command", "rule": "plain_cmd", "expires": float64(0)},
		},
	}
	db.data["goroku.main"] = map[string]any{}

	sm := NewSecurityManager(NewCustomTelegramClient(42), db)
	t.Cleanup(sm.Stop)

	delegate := &Message{SenderID: 100, ChatID: 100, IsPrivate: true}
	if sm.Check(delegate, "clipped_cmd") {
		t.Fatal("tsec rule granted a command the bounding mask clipped to nothing")
	}
	if !sm.Check(delegate, "plain_cmd") {
		t.Fatal("tsec rule stopped granting a command the bounding mask still allows")
	}
}

func TestIsPrivilegedModuleIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{"GorokuSecurity", "gorokusecurity", " Eval ", "TERMINAL", "Loader"} {
		if !IsPrivilegedModule(name) {
			t.Errorf("IsPrivilegedModule(%q) = false, want true", name)
		}
	}
	if IsPrivilegedModule("GorokuInfo") {
		t.Error("IsPrivilegedModule(GorokuInfo) = true, want false")
	}
}

// SenderID 0 is not an identity. Anonymous admins and channel-signed posts
// carry no user, and the client TGID is 0 until the session authorizes —
// comparing the two matched, so such a message passed as the owner.
func TestUnknownSenderDoesNotMatchAnUnauthorizedClient(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(0))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{}

	client := NewCustomTelegramClient(0)
	sm := NewSecurityManager(client, db)
	t.Cleanup(sm.Stop)

	anonymous := &Message{SenderID: 0, ChatID: -100500, IsChannel: true}
	if sm.Check(anonymous, "owneradd") {
		t.Fatal("a message with no sender passed as the owner of an unauthorized client")
	}

	// Once authorized, the owner's own messages still pass.
	client.SetTGID(42)
	if !sm.Check(&Message{SenderID: 42, ChatID: 1}, "ping") {
		t.Fatal("the owner stopped passing their own security check")
	}
}
