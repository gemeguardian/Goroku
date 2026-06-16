package goroku

import (
	"reflect"
	"testing"
)

func TestPointerList(t *testing.T) {
	db := NewDatabase(99)
	db.data["mod"] = map[string]interface{}{
		"list": []interface{}{"a", "b"},
	}

	p := NewPointerList(db, "mod", "list", []interface{}{})

	// Test Len and Get
	if p.Len() != 2 {
		t.Errorf("Expected length 2, got %d", p.Len())
	}
	if p.Get(0) != "a" || p.Get(1) != "b" {
		t.Errorf("Expected 'a' and 'b', got '%v' and '%v'", p.Get(0), p.Get(1))
	}

	// Test Set
	p.Set(1, "c")
	if p.Get(1) != "c" {
		t.Errorf("Expected 'c', got '%v'", p.Get(1))
	}

	// Test Append
	p.Append("d")
	if p.Len() != 3 || p.Get(2) != "d" {
		t.Errorf("Append failed: len=%d, val=%v", p.Len(), p.Get(2))
	}

	// Test Extend
	p.Extend([]interface{}{"e", "f"})
	if p.Len() != 5 || p.Get(4) != "f" {
		t.Errorf("Extend failed: len=%d", p.Len())
	}

	// Test Remove
	p.Remove(2) // removes "d"
	if p.Len() != 4 || p.Get(2) != "e" {
		t.Errorf("Remove failed: len=%d, val at 2=%v", p.Len(), p.Get(2))
	}

	// Test ToSlice
	slice := p.ToSlice()
	expected := []interface{}{"a", "c", "e", "f"}
	if !reflect.DeepEqual(slice, expected) {
		t.Errorf("ToSlice failed: expected %v, got %v", expected, slice)
	}

	// Test Clear
	p.Clear()
	if p.Len() != 0 {
		t.Errorf("Clear failed, len = %d", p.Len())
	}
}

func TestPointerDict(t *testing.T) {
	db := NewDatabase(99)
	db.data["mod"] = map[string]interface{}{
		"dict": map[string]interface{}{"k1": "v1", "k2": "v2"},
	}

	p := NewPointerDict(db, "mod", "dict", map[string]interface{}{})

	// Test Get
	if p.Get("k1") != "v1" {
		t.Errorf("Expected 'v1', got '%v'", p.Get("k1"))
	}

	// Test Set
	p.Set("k3", "v3")
	if p.Get("k3") != "v3" {
		t.Errorf("Expected 'v3', got '%v'", p.Get("k3"))
	}

	// Test Delete
	p.Delete("k1")
	if p.Get("k1") != nil {
		t.Errorf("Delete failed, key 'k1' still exists: %v", p.Get("k1"))
	}

	// Test ToMap
	m := p.ToMap()
	expected := map[string]interface{}{
		"k2": "v2",
		"k3": "v3",
	}
	if !reflect.DeepEqual(m, expected) {
		t.Errorf("ToMap failed: expected %v, got %v", expected, m)
	}

	// Test Clear
	p.Clear()
	if len(p.ToMap()) != 0 {
		t.Errorf("Clear failed, got %v", p.ToMap())
	}
}

func TestQRCode(t *testing.T) {
	qr := NewQRCode()
	qr.AddData("test_data")
	if qr.data != "test_data" {
		t.Errorf("Expected 'test_data', got %q", qr.data)
	}

	// PrintASCII should not panic
	qr.PrintASCII(false)
}

func TestReplaceAllRefs(t *testing.T) {
	obj1 := "hello"
	obj2 := "world"
	res := ReplaceAllRefs(obj1, obj2)
	if res != obj1 {
		t.Errorf("Expected %v, got %v", obj1, res)
	}
}

func TestFormatString(t *testing.T) {
	text := "Hello {name}, your age is {age}."
	kwargs := map[string]interface{}{
		"name": "Bob",
		"age":  25,
	}
	formatted := FormatString(text, kwargs)
	expected := "Hello Bob, your age is 25."
	if formatted != expected {
		t.Errorf("Expected %q, got %q", expected, formatted)
	}
}

func TestBaseTranslatorGetPackRaw(t *testing.T) {
	bt := &BaseTranslator{}

	// Test JSON parsing
	jsonContent := `{
		"ModuleA": {
			"name": "Module A",
			"key1": "value1"
		}
	}`
	resJSON, err := bt.getPackRaw(jsonContent, ".json", "prefix_")
	if err != nil {
		t.Fatalf("JSON parse failed: %v", err)
	}
	if val := resJSON["prefix_ModuleA.key1"]; val != "value1" {
		t.Errorf("Expected 'value1', got '%v'", val)
	}
	if _, ok := resJSON["prefix_ModuleA.name"]; ok {
		t.Error("Expected 'name' key to be ignored/skipped")
	}

	// Test YAML parsing
	yamlContent := `
ModuleB:
  name: Module B
  key2: value2
`
	resYAML, err := bt.getPackRaw(yamlContent, ".yaml", "prefix_")
	if err != nil {
		t.Fatalf("YAML parse failed: %v", err)
	}
	if val := resYAML["prefix_ModuleB.key2"]; val != "value2" {
		t.Errorf("Expected 'value2', got '%v'", val)
	}
}
