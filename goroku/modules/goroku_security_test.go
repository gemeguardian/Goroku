package modules

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"goroku/goroku"
	"goroku/goroku/inline"
)

func newSecurityModuleTestDatabase(t *testing.T) *goroku.Database {
	t.Helper()
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = t.TempDir()
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })
	db := goroku.NewDatabase(99)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

func TestInterfaceToInt64(t *testing.T) {
	tests := []struct {
		input any
		want  int64
	}{
		{42, 42},
		{int64(42), 42},
		{float64(42.9), 42},
		{json.Number("42"), 42},
		{json.Number("invalid"), 0},
		{nil, 0},
		{"string", 0},
	}

	for _, tc := range tests {
		got := interfaceToInt64(tc.input)
		if got != tc.want {
			t.Errorf("interfaceToInt64(%v) = %d; want %d", tc.input, got, tc.want)
		}
	}
}

func TestPointerContainsID(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	if err := db.Set("mod", "list", []any{int64(1), int64(2), int64(3)}); err != nil {
		t.Fatal(err)
	}
	pl := goroku.NewPointerList[int64](db, "mod", "list", nil)

	if !pointerContainsID(pl, 2) {
		t.Error("Expected pointerContainsID to find 2")
	}
	if pointerContainsID(pl, 99) {
		t.Error("Expected pointerContainsID to not find 99")
	}
	if pointerContainsID(nil, 1) {
		t.Error("Expected pointerContainsID(nil) to return false")
	}
}

func TestPointerRemoveID(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	if err := db.Set("mod", "list", []any{int64(1), int64(2), int64(3)}); err != nil {
		t.Fatal(err)
	}
	pl := goroku.NewPointerList[int64](db, "mod", "list", nil)

	removed, err := pointerRemoveID(pl, 2)
	if err != nil || !removed {
		t.Error("Expected pointerRemoveID to remove 2")
	}
	if pointerContainsID(pl, 2) {
		t.Error("Expected 2 to be removed")
	}
	removed, err = pointerRemoveID(pl, 99)
	if err != nil || removed {
		t.Error("Expected pointerRemoveID to return false for 99")
	}
	removed, err = pointerRemoveID(nil, 1)
	if err != nil || removed {
		t.Error("Expected pointerRemoveID(nil) to return false")
	}
}

func TestExtractTime(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{[]string{"1d"}, 24 * 60 * 60},
		{[]string{"2h"}, 2 * 60 * 60},
		{[]string{"30m"}, 30 * 60},
		{[]string{"45s"}, 45},
		{[]string{"1d", "2h"}, 24 * 60 * 60}, // first match wins
		{[]string{"invalid"}, 0},
		{[]string{}, 0},
		{[]string{"0d"}, 0},
		{[]string{"1.5h"}, 0},    // float not parsed
		{[]string{"123"}, 0},     // no suffix
		{[]string{"-1h"}, -3600}, // negative
		{[]string{"1d2h"}, 0},    // combined without space
	}

	for _, tc := range tests {
		got := extractTime(tc.args)
		if got != tc.want {
			t.Errorf("extractTime(%v) = %d; want %d", tc.args, got, tc.want)
		}
	}
}

func TestSecurityMaskWriteFailureReturnsError(t *testing.T) {
	client, db := newFailingModuleTest(t)
	m := &GorokuSecurity{client: client, db: db}
	buttons := m.buildMarkupGlobal(false)
	err := buttons[0][0].Handler(inline.CallbackQuery{})
	if !errors.Is(err, goroku.ErrDatabasePersistence) {
		t.Fatalf("mask handler error = %v, want persistence failure", err)
	}
	if got := db.GetInt("goroku.security", "bounding_mask", goroku.DEFAULT_PERMISSIONS); got != goroku.DEFAULT_PERMISSIONS {
		t.Fatalf("failed mask write published value %d", got)
	}
	commandButtons := m.buildMarkupCommand("ping", false)
	err = commandButtons[0][0].Handler(inline.CallbackQuery{})
	if !errors.Is(err, goroku.ErrDatabasePersistence) {
		t.Fatalf("permission handler error = %v, want persistence failure", err)
	}
	if masks := db.GetStringMapInt("goroku.security", "masks", nil); len(masks) != 0 {
		t.Fatalf("failed permission write published masks: %v", masks)
	}
}

func TestAPIProtectionWriteFailureDoesNotChangeState(t *testing.T) {
	client, db := newFailingModuleTest(t)
	m := &APIProtection{}
	if err := m.Init(client, db); err != nil {
		t.Fatal(err)
	}
	err := m.AntifloodCmd(&goroku.Message{})
	if !errors.Is(err, goroku.ErrDatabasePersistence) {
		t.Fatalf("API protection error = %v, want persistence failure", err)
	}
	if !db.GetBool("APILimiter", "disable_protection", true) {
		t.Fatal("failed API protection write changed durable state")
	}
}

func TestAPIProtectionReadsPropagateLifecycleErrors(t *testing.T) {
	db := goroku.NewDatabase(2002)
	m := &APIProtection{db: db}
	if err := m.ConfigReady(map[string]any{}); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("ConfigReady() error = %v, want ErrDatabaseNotInitialized", err)
	}
	if err := m.AntifloodCmd(&goroku.Message{}); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("AntifloodCmd() error = %v, want ErrDatabaseNotInitialized", err)
	}
	if got := db.GetValue("APILimiter", "disable_protection", true); got != true {
		t.Fatalf("failed read derived a write: disable_protection = %v", got)
	}
}

func TestAPIProtectionMissingKeysUseDefaults(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	client := goroku.NewCustomTelegramClient(2003)
	m := &APIProtection{client: client, db: db}
	if err := m.ConfigReady(map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(m.forbiddenTypeIDs) != 2 || len(client.ForbiddenConstructors) != 2 {
		t.Fatalf("default forbidden methods not applied: module=%v client=%v", m.forbiddenTypeIDs, client.ForbiddenConstructors)
	}
}

func TestSecurityGroupsDistinguishEmptyFromUnavailable(t *testing.T) {
	uninitialized := goroku.NewDatabase(2005)
	m := &GorokuSecurity{db: uninitialized}
	if _, err := m.loadGroups(); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("loadGroups() error = %v, want ErrDatabaseNotInitialized", err)
	}
	if err := m.SgroupsCmd(&goroku.Message{}); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("SgroupsCmd() error = %v, want ErrDatabaseNotInitialized", err)
	}

	active := newSecurityModuleTestDatabase(t)
	m.db = active
	groups, err := m.loadGroups()
	if err != nil {
		t.Fatal(err)
	}
	if groups == nil || len(groups) != 0 {
		t.Fatalf("missing active configuration = %#v, want non-nil empty map", groups)
	}
}
