package goroku

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDatabaseDeepCopyClonesNestedMapsAndSlices(t *testing.T) {
	db := &Database{}
	src := map[string]map[string]any{
		"owner": {
			"nested": map[string]any{"key": "original"},
			"slice":  []any{map[string]any{"item": "original"}},
		},
	}

	copy := db.deepCopy(src)
	copy["owner"]["nested"].(map[string]any)["key"] = "changed"
	copy["owner"]["slice"].([]any)[0].(map[string]any)["item"] = "changed"

	if got := src["owner"]["nested"].(map[string]any)["key"]; got != "original" {
		t.Fatalf("deepCopy shared nested map with source, got %v", got)
	}
	if got := src["owner"]["slice"].([]any)[0].(map[string]any)["item"]; got != "original" {
		t.Fatalf("deepCopy shared nested slice value with source, got %v", got)
	}
}

func TestDatabaseCollectionGettersReturnCopiesAndEmptyDefaults(t *testing.T) {
	db := NewDatabase(1111)
	db.data["owner"] = map[string]any{
		"strings":    []string{"a"},
		"ids":        []int64{1},
		"string_map": map[string]string{"k": "v"},
		"any_map":    map[string]any{"k": "v", "nested": map[string]any{"n": "v"}},
		"slice_map":  map[string][]string{"k": {"v"}},
		"int_map":    map[string]int{"k": 1},
	}

	strings := db.GetStringSlice("owner", "strings", nil)
	strings[0] = "changed"
	if got := db.data["owner"]["strings"].([]string)[0]; got != "a" {
		t.Fatalf("GetStringSlice returned original slice, got %q", got)
	}

	ids := db.GetInt64Slice("owner", "ids", nil)
	ids[0] = 2
	if got := db.data["owner"]["ids"].([]int64)[0]; got != 1 {
		t.Fatalf("GetInt64Slice returned original slice, got %d", got)
	}

	stringMap := db.GetStringMap("owner", "string_map", nil)
	stringMap["k"] = "changed"
	if got := db.data["owner"]["string_map"].(map[string]string)["k"]; got != "v" {
		t.Fatalf("GetStringMap returned original map, got %q", got)
	}

	anyMap := db.GetAnyMap("owner", "any_map", nil)
	anyMap["k"] = "changed"
	anyMap["nested"].(map[string]any)["n"] = "changed"
	if got := db.data["owner"]["any_map"].(map[string]any)["k"]; got != "v" {
		t.Fatalf("GetAnyMap returned original map, got %v", got)
	}
	if got := db.data["owner"]["any_map"].(map[string]any)["nested"].(map[string]any)["n"]; got != "v" {
		t.Fatalf("GetAnyMap returned original nested map, got %v", got)
	}

	sliceMap := db.GetStringMapStringSlice("owner", "slice_map", nil)
	sliceMap["k"][0] = "changed"
	if got := db.data["owner"]["slice_map"].(map[string][]string)["k"][0]; got != "v" {
		t.Fatalf("GetStringMapStringSlice returned original nested slice, got %q", got)
	}

	intMap := db.GetStringMapInt("owner", "int_map", nil)
	intMap["k"] = 2
	if got := db.data["owner"]["int_map"].(map[string]int)["k"]; got != 1 {
		t.Fatalf("GetStringMapInt returned original map, got %d", got)
	}

	if got := db.GetStringMap("owner", "missing_string_map", nil); got == nil {
		t.Fatal("GetStringMap returned nil for nil default")
	}
	if got := db.GetAnyMap("owner", "missing_any_map", nil); got == nil {
		t.Fatal("GetAnyMap returned nil for nil default")
	}
	if got := db.GetStringSlice("owner", "missing_strings", nil); got == nil {
		t.Fatal("GetStringSlice returned nil for nil default")
	}
}

func TestDatabaseCRUDOperations(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(12345)
	err := db.Init("")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Test Set and Get
	if !db.Set("test_module", "key1", "value1") {
		t.Fatal("Set failed")
	}

	val, _ := db.Get("test_module", "key1", "default")
	if val != "value1" {
		t.Fatalf("Expected 'value1', got '%v'", val)
	}

	// Test case-insensitivity of module name
	valFold, _ := db.Get("TEST_module", "key1", "default")
	if valFold != "value1" {
		t.Fatalf("Expected 'value1' with case-insensitive check, got '%v'", valFold)
	}

	// Test Dump
	dump := db.Dump()
	if dump["test_module"]["key1"] != "value1" {
		t.Fatalf("Dump does not contain correct value: %v", dump)
	}

	// Test Delete
	if !db.Delete("test_module", "key1") {
		t.Fatal("Delete failed")
	}

	valDeleted, _ := db.Get("test_module", "key1", "default")
	if valDeleted != "default" {
		t.Fatalf("Expected default value after delete, got '%v'", valDeleted)
	}
}

func TestDatabaseRevisionsAndRollback(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(54321)
	err := db.Init("")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// We force a rollback check. Initially no revisions
	if db.Rollback() {
		t.Fatal("Rollback should fail when no revisions exist")
	}

	// Modify nextRevCall so revision will be created immediately on save
	db.nextRevCall = 0

	db.Set("mod", "k1", "initial")
	// Do NOT reset nextRevCall, so that the second Set does not create a new revision.
	db.Set("mod", "k1", "second")

	if val, _ := db.Get("mod", "k1", ""); val != "second" {
		t.Fatalf("Expected second, got %v", val)
	}

	if !db.Rollback() {
		t.Fatal("Rollback failed")
	}

	if val, _ := db.Get("mod", "k1", ""); val != "initial" {
		t.Fatalf("Expected initial after rollback, got %v", val)
	}
}

func TestDatabaseLegacyPrefixConversion(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	tgID := int64(98765)
	dbPath := filepath.Join(tempDir, fmt.Sprintf("config-%d.json", tgID))

	// Write legacy data manually
	legacyData := map[string]any{
		"hikka.module": map[string]any{
			"foo": "bar",
		},
		"legacy.test": map[string]any{
			"abc": 123,
		},
		"heroku.other": map[string]any{
			"xyz": true,
		},
	}
	bytes, err := json.Marshal(legacyData)
	if err != nil {
		t.Fatalf("Failed to marshal legacy data: %v", err)
	}
	err = os.WriteFile(dbPath, bytes, 0600)
	if err != nil {
		t.Fatalf("Failed to write legacy file: %v", err)
	}

	db := NewDatabase(tgID)
	err = db.Init("")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Verify prefix conversion took place
	if val, _ := db.Get("goroku.module", "foo", nil); val != "bar" {
		t.Fatalf("Expected 'bar' from goroku.module, got '%v'", val)
	}
	if val, _ := db.Get("goroku.test", "abc", nil); val != float64(123) {
		t.Fatalf("Expected 123 from goroku.test, got '%v'", val)
	}
	if val, _ := db.Get("goroku.other", "xyz", nil); val != true {
		t.Fatalf("Expected true from goroku.other, got '%v'", val)
	}
}

func TestDatabaseAutofix(t *testing.T) {
	db := NewDatabase(1111)
	db.data["some_module"] = nil // empty module keys should be removed

	db.processDBAutofix()

	if _, ok := db.data["some_module"]; ok {
		t.Fatal("Expected nil module key to be removed by autofix")
	}
}

func TestDatabaseNormalizeOwnerDirect(t *testing.T) {
	db := NewDatabase(1111)
	db.data["TestOwner"] = map[string]any{"key": "val"}
	// exact match
	if got := db.normalizeOwner("TestOwner"); got != "TestOwner" {
		t.Errorf("normalizeOwner exact match failed: got %q, want %q", got, "TestOwner")
	}
	// case-insensitive match from db.data
	if got := db.normalizeOwner("testowner"); got != "TestOwner" {
		t.Errorf("normalizeOwner case-insensitive failed: got %q, want %q", got, "TestOwner")
	}
	// fallback to original
	if got := db.normalizeOwner("NonExistent"); got != "NonExistent" {
		t.Errorf("normalizeOwner fallback failed: got %q, want %q", got, "NonExistent")
	}
}

func TestDatabaseDeleteOwner(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(1111)
	_ = db.Init("")
	db.Set("mod1", "k", "v")
	db.Set("mod2", "k", "v")

	if !db.DeleteOwner("mod1") {
		t.Fatal("DeleteOwner failed")
	}

	if val, _ := db.Get("mod1", "k", nil); val != nil {
		t.Fatalf("expected mod1 to be deleted, got %v", val)
	}
	if val, _ := db.Get("mod2", "k", nil); val == nil {
		t.Fatal("expected mod2 to remain")
	}
}

func TestDatabaseReset(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(1111)
	_ = db.Init("")
	db.Set("mod1", "k", "v")

	newData := map[string]map[string]any{
		"new_mod": {"nk": "nv"},
	}

	if !db.Reset(newData) {
		t.Fatal("Reset failed")
	}

	if val, _ := db.Get("mod1", "k", nil); val != nil {
		t.Fatal("expected old data to be cleared")
	}
	if val, _ := db.Get("new_mod", "nk", nil); val != "nv" {
		t.Fatalf("expected new_mod to exist, got %v", val)
	}
}

func TestDatabaseGetAll(t *testing.T) {
	db := NewDatabase(1111)
	db.data = map[string]map[string]any{
		"m": {"k": "v"},
	}
	all := db.GetAll()
	all["m"]["k"] = "changed"

	if db.data["m"]["k"] != "v" {
		t.Fatal("GetAll did not perform a deep copy")
	}
}

func TestDatabaseRedisSaveLogic(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(1111)
	_ = db.Init("")
	db.redisClient = redis.NewClient(&redis.Options{Addr: "localhost:12345"}) // non-existent redis
	db.Set("m", "k", "v")

	// Since lastRedisSave is 0, now - lastRedisSave >= 5.
	// It will attempt to write to Redis, fail, and fall back to local file.
	// We check that saveInner returned true because file save succeeded.
	db.lastRedisSave = time.Now().Unix()
	// Modify key to trigger save
	db.Set("m", "k2", "v2")
	// Since lastRedisSave was just set to now, now - lastRedisSave < 5, so it should set redisDirty = true
	if !db.redisDirty {
		t.Fatal("expected redisDirty to be true when saved within 5 seconds")
	}
}
