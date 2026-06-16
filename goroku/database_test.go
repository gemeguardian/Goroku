package goroku

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseDeepCopyClonesNestedMapsAndSlices(t *testing.T) {
	db := &Database{}
	src := map[string]map[string]interface{}{
		"owner": {
			"nested": map[string]interface{}{"key": "original"},
			"slice":  []interface{}{map[string]interface{}{"item": "original"}},
		},
	}

	copy := db.deepCopy(src)
	copy["owner"]["nested"].(map[string]interface{})["key"] = "changed"
	copy["owner"]["slice"].([]interface{})[0].(map[string]interface{})["item"] = "changed"

	if got := src["owner"]["nested"].(map[string]interface{})["key"]; got != "original" {
		t.Fatalf("deepCopy shared nested map with source, got %v", got)
	}
	if got := src["owner"]["slice"].([]interface{})[0].(map[string]interface{})["item"]; got != "original" {
		t.Fatalf("deepCopy shared nested slice value with source, got %v", got)
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

	val := db.Get("test_module", "key1", "default")
	if val != "value1" {
		t.Fatalf("Expected 'value1', got '%v'", val)
	}

	// Test case-insensitivity of module name
	valFold := db.Get("TEST_module", "key1", "default")
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

	valDeleted := db.Get("test_module", "key1", "default")
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

	if val := db.Get("mod", "k1", ""); val != "second" {
		t.Fatalf("Expected second, got %v", val)
	}

	if !db.Rollback() {
		t.Fatal("Rollback failed")
	}

	if val := db.Get("mod", "k1", ""); val != "initial" {
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
	legacyData := map[string]interface{}{
		"hikka.module": map[string]interface{}{
			"foo": "bar",
		},
		"legacy.test": map[string]interface{}{
			"abc": 123,
		},
		"heroku.other": map[string]interface{}{
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
	if val := db.Get("goroku.module", "foo", nil); val != "bar" {
		t.Fatalf("Expected 'bar' from goroku.module, got '%v'", val)
	}
	if val := db.Get("goroku.test", "abc", nil); val != float64(123) {
		t.Fatalf("Expected 123 from goroku.test, got '%v'", val)
	}
	if val := db.Get("goroku.other", "xyz", nil); val != true {
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

