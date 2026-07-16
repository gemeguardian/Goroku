package goroku

import (
	"errors"
	"reflect"
	"testing"
)

func newPointerTestDatabase(t *testing.T) *Database {
	t.Helper()
	return initializedTestDatabase(t, NewDatabase(99))
}

func installPostRenameWarning(t *testing.T, db *Database, cause error) {
	t.Helper()
	db.writeLocal = func(path string, data []byte) error {
		if err := writeFileAtomic(path, data); err != nil {
			return err
		}
		return errors.Join(errAtomicWriteCommitted, cause)
	}
	t.Cleanup(func() {
		db.writeLocal = writeFileAtomic
		_ = db.Save()
	})
}

func TestPointerList(t *testing.T) {
	db := newPointerTestDatabase(t)
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

	if err := p.Set(1, "c"); err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Get(1); !ok || v != "c" {
		t.Errorf("Expected 'c', got '%v'", v)
	}

	if err := p.Append("d"); err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Get(2); p.Len() != 3 || !ok || v != "d" {
		t.Errorf("Append failed: len=%d, val=%v", p.Len(), v)
	}

	if err := p.Extend([]string{"e", "f"}); err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Get(4); p.Len() != 5 || !ok || v != "f" {
		t.Errorf("Extend failed: len=%d", p.Len())
	}

	if err := p.Remove(2); err != nil { // removes "d"
		t.Fatal(err)
	}
	if v, ok := p.Get(2); p.Len() != 4 || !ok || v != "e" {
		t.Errorf("Remove failed: len=%d, val at 2=%v", p.Len(), v)
	}

	slice := p.ToSlice()
	expected := []string{"a", "c", "e", "f"}
	if !reflect.DeepEqual(slice, expected) {
		t.Errorf("ToSlice failed: expected %v, got %v", expected, slice)
	}

	if err := p.Clear(); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 0 {
		t.Errorf("Clear failed, len = %d", p.Len())
	}
}

func TestPointerDict(t *testing.T) {
	db := newPointerTestDatabase(t)
	db.data["mod"] = map[string]any{
		"dict": map[string]any{"k1": "v1", "k2": "v2"},
	}

	p := NewPointerDict[string](db, "mod", "dict", nil)

	if v, ok := p.Get("k1"); !ok || v != "v1" {
		t.Errorf("Expected 'v1', got '%v'", v)
	}

	if err := p.Set("k3", "v3"); err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Get("k3"); !ok || v != "v3" {
		t.Errorf("Expected 'v3', got '%v'", v)
	}

	if err := p.Delete("k1"); err != nil {
		t.Fatal(err)
	}
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

	if err := p.Clear(); err != nil {
		t.Fatal(err)
	}
	if len(p.ToMap()) != 0 {
		t.Errorf("Clear failed, got %v", p.ToMap())
	}
}

func TestPointerListInt64(t *testing.T) {
	db := newPointerTestDatabase(t)
	p := NewPointerList[int64](db, "mod", "intlist", nil)
	if err := p.Append(1); err != nil {
		t.Fatal(err)
	}
	if err := p.Append(2); err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Get(0); !ok || v != 1 {
		t.Errorf("Expected 1, got %v", v)
	}
	slice := p.ToSlice()
	if !reflect.DeepEqual(slice, []int64{1, 2}) {
		t.Errorf("Expected [1 2], got %v", slice)
	}
}

func TestCheckedPointerConstructorsReportLifecycleErrors(t *testing.T) {
	db := NewDatabase(100)
	if _, err := NewPointerListChecked[int64](db, "mod", "list", nil); !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized list error = %v", err)
	}
	if _, err := NewPointerDictChecked[int64](db, "mod", "dict", nil); !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized dict error = %v", err)
	}
	if _, err := db.PointerChecked("mod", "list", []any{}); !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized database pointer error = %v", err)
	}

	db = newPointerTestDatabase(t)
	if err := db.Close(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPointerListChecked[int64](db, "mod", "list", nil); !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("closed list error = %v", err)
	}
}

func TestPointerMutationFailureDoesNotChangeLocalState(t *testing.T) {
	db := newPointerTestDatabase(t)
	db.data["mod"] = map[string]any{"list": []any{"old"}}
	p := NewPointerList[string](db, "mod", "list", nil)
	injected := errors.New("injected persistence failure")
	db.writeLocal = func(string, []byte) error { return injected }

	if err := p.Append("new"); !errors.Is(err, injected) {
		t.Fatalf("Append error = %v", err)
	}
	if got := p.ToSlice(); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("pointer diverged after failed write: %#v", got)
	}
}

func TestPointerMutationsPublishPostRenameWarnings(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		db := newPointerTestDatabase(t)
		db.data["mod"] = map[string]any{"list": []any{"old"}}
		p := NewPointerList[string](db, "mod", "list", nil)
		cause := errors.New("post-rename list warning")
		installPostRenameWarning(t, db, cause)

		if err := p.Append("new"); err != nil {
			t.Fatalf("Append returned a post-rename warning: %v", err)
		}
		assertCommittedWarning(t, db.DurabilityWarning(), cause)
		if got := p.ToSlice(); !reflect.DeepEqual(got, []string{"old", "new"}) {
			t.Fatalf("list did not publish committed candidate: %#v", got)
		}
	})

	t.Run("dict", func(t *testing.T) {
		db := newPointerTestDatabase(t)
		db.data["mod"] = map[string]any{"dict": map[string]any{"old": "value"}}
		p := NewPointerDict[string](db, "mod", "dict", nil)
		cause := errors.New("post-rename dict warning")
		installPostRenameWarning(t, db, cause)

		if err := p.Set("new", "value"); err != nil {
			t.Fatalf("Set returned a post-rename warning: %v", err)
		}
		assertCommittedWarning(t, db.DurabilityWarning(), cause)
		if got := p.ToMap(); !reflect.DeepEqual(got, map[string]string{"old": "value", "new": "value"}) {
			t.Fatalf("dict did not publish committed candidate: %#v", got)
		}
	})
}

func assertCommittedWarning(t *testing.T, err, cause error) {
	t.Helper()
	if !errors.Is(err, ErrDatabaseCommitUncertain) || !errors.Is(err, ErrDatabasePersistence) || cause != nil && !errors.Is(err, cause) {
		t.Fatalf("error = %v, want committed warning preserving cause", err)
	}
	var dbErr *DatabaseError
	if !errors.As(err, &dbErr) || !dbErr.Committed {
		t.Fatalf("error = %v, want committed DatabaseError", err)
	}
}

type pointerNestedValue struct {
	Items map[string][]int
}

func TestPointerListDeepCopiesNestedBoundaries(t *testing.T) {
	db := newPointerTestDatabase(t)
	p := NewPointerList[pointerNestedValue](db, "mod", "list", nil)
	input := pointerNestedValue{Items: map[string][]int{"numbers": {1}}}
	if err := p.Append(input); err != nil {
		t.Fatal(err)
	}
	input.Items["numbers"][0] = 2
	got, ok := p.Get(0)
	if !ok || got.Items["numbers"][0] != 1 {
		t.Fatalf("Append retained caller alias: %#v", got)
	}
	got.Items["numbers"][0] = 3
	copy := p.ToSlice()
	copy[0].Items["numbers"][0] = 4
	again, _ := p.Get(0)
	if again.Items["numbers"][0] != 1 {
		t.Fatalf("read boundary exposed pointer state: %#v", again)
	}
}

func TestPointerDictDeepCopiesNestedBoundariesOnSuccessAndFailure(t *testing.T) {
	db := newPointerTestDatabase(t)
	p := NewPointerDict[pointerNestedValue](db, "mod", "dict", nil)
	input := pointerNestedValue{Items: map[string][]int{"numbers": {1}}}
	if err := p.Set("ok", input); err != nil {
		t.Fatal(err)
	}
	input.Items["numbers"][0] = 2
	got, _ := p.Get("ok")
	if got.Items["numbers"][0] != 1 {
		t.Fatalf("Set retained caller alias: %#v", got)
	}

	injected := errors.New("injected persistence failure")
	db.writeLocal = func(string, []byte) error { return injected }
	failed := pointerNestedValue{Items: map[string][]int{"numbers": {5}}}
	if err := p.Set("failed", failed); !errors.Is(err, injected) {
		t.Fatalf("Set error = %v", err)
	}
	failed.Items["numbers"][0] = 6
	if _, ok := p.Get("failed"); ok {
		t.Fatal("failed Set changed pointer state")
	}
	copy := p.ToMap()
	copy["ok"].Items["numbers"][0] = 7
	again, _ := p.Get("ok")
	if again.Items["numbers"][0] != 1 {
		t.Fatalf("ToMap exposed pointer state: %#v", again)
	}
}

type pointerUnexportedMutable struct {
	Visible string
	hidden  map[string][]int
}

func TestPointerRejectsUnexportedMutableValuesWithoutPublishingAliases(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		db := newPointerTestDatabase(t)
		p := NewPointerList[pointerUnexportedMutable](db, "mod", "list", nil)
		input := pointerUnexportedMutable{Visible: "json-visible", hidden: map[string][]int{"n": {1}}}
		if err := p.Append(input); !errors.Is(err, ErrDatabaseInvalidValue) {
			t.Fatalf("Append error = %v, want ErrDatabaseInvalidValue", err)
		}
		input.hidden["n"][0] = 2
		if p.Len() != 0 || len(p.ToSlice()) != 0 {
			t.Fatalf("rejected Append changed pointer state: %#v", p.ToSlice())
		}
		if got, _ := db.Get("mod", "list", nil); got != nil {
			t.Fatalf("rejected Append changed database: %#v", got)
		}
	})

	t.Run("dict", func(t *testing.T) {
		db := newPointerTestDatabase(t)
		p := NewPointerDict[pointerUnexportedMutable](db, "mod", "dict", nil)
		input := pointerUnexportedMutable{Visible: "json-visible", hidden: map[string][]int{"n": {1}}}
		if err := p.Set("bad", input); !errors.Is(err, ErrDatabaseInvalidValue) {
			t.Fatalf("Set error = %v, want ErrDatabaseInvalidValue", err)
		}
		input.hidden["n"][0] = 2
		if _, ok := p.Get("bad"); ok || len(p.ToMap()) != 0 {
			t.Fatalf("rejected Set changed pointer state: %#v", p.ToMap())
		}
		if got, _ := db.Get("mod", "dict", nil); got != nil {
			t.Fatalf("rejected Set changed database: %#v", got)
		}
	})
}
