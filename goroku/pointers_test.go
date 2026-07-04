package goroku

import (
	"reflect"
	"testing"
)

func TestPointerList(t *testing.T) {
	db := NewDatabase(99)
	db.data["mod"] = map[string]any{
		"list": []any{"a", "b"},
	}

	p := NewPointerList[string](db, "mod", "list", nil)

	if p.Len() != 2 {
		t.Errorf("Expected length 2, got %d", p.Len())
	}
	v0, ok0 := p.Get(0)
	v1, ok1 := p.Get(1)
	if !ok0 || v0 != "a" || !ok1 || v1 != "b" {
		t.Errorf("Expected 'a' and 'b', got '%v' and '%v'", v0, v1)
	}

	p.Set(1, "c")
	if v, ok := p.Get(1); !ok || v != "c" {
		t.Errorf("Expected 'c', got '%v'", v)
	}

	p.Append("d")
	if v, ok := p.Get(2); p.Len() != 3 || !ok || v != "d" {
		t.Errorf("Append failed: len=%d, val=%v", p.Len(), v)
	}

	p.Extend([]string{"e", "f"})
	if v, ok := p.Get(4); p.Len() != 5 || !ok || v != "f" {
		t.Errorf("Extend failed: len=%d", p.Len())
	}

	p.Remove(2) // removes "d"
	if v, ok := p.Get(2); p.Len() != 4 || !ok || v != "e" {
		t.Errorf("Remove failed: len=%d, val at 2=%v", p.Len(), v)
	}

	slice := p.ToSlice()
	expected := []string{"a", "c", "e", "f"}
	if !reflect.DeepEqual(slice, expected) {
		t.Errorf("ToSlice failed: expected %v, got %v", expected, slice)
	}

	p.Clear()
	if p.Len() != 0 {
		t.Errorf("Clear failed, len = %d", p.Len())
	}
}

func TestPointerDict(t *testing.T) {
	db := NewDatabase(99)
	db.data["mod"] = map[string]any{
		"dict": map[string]any{"k1": "v1", "k2": "v2"},
	}

	p := NewPointerDict[string](db, "mod", "dict", nil)

	if v, ok := p.Get("k1"); !ok || v != "v1" {
		t.Errorf("Expected 'v1', got '%v'", v)
	}

	p.Set("k3", "v3")
	if v, ok := p.Get("k3"); !ok || v != "v3" {
		t.Errorf("Expected 'v3', got '%v'", v)
	}

	p.Delete("k1")
	if _, ok := p.Get("k1"); ok {
		t.Errorf("Delete failed, key 'k1' still exists")
	}

	m := p.ToMap()
	expected := map[string]string{
		"k2": "v2",
		"k3": "v3",
	}
	if !reflect.DeepEqual(m, expected) {
		t.Errorf("ToMap failed: expected %v, got %v", expected, m)
	}

	p.Clear()
	if len(p.ToMap()) != 0 {
		t.Errorf("Clear failed, got %v", p.ToMap())
	}
}

func TestPointerListInt64(t *testing.T) {
	db := NewDatabase(99)
	p := NewPointerList[int64](db, "mod", "intlist", nil)
	p.Append(1)
	p.Append(2)
	if v, ok := p.Get(0); !ok || v != 1 {
		t.Errorf("Expected 1, got %v", v)
	}
	slice := p.ToSlice()
	if !reflect.DeepEqual(slice, []int64{1, 2}) {
		t.Errorf("Expected [1 2], got %v", slice)
	}
}
