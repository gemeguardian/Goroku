package goroku

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// PointerList is a generic persisted list backed by the Database.
type PointerList[T any] struct {
	mu     sync.RWMutex
	db     *Database
	module string
	key    string
	values []T
}

// NewPointerListChecked loads or creates a persisted list for the given type.
func NewPointerListChecked[T any](db *Database, module, key string, defaultValue []T) (*PointerList[T], error) {
	if db == nil {
		return nil, databaseError("get", module, key, "", ErrDatabaseNotInitialized, nil)
	}
	raw, err := db.Get(module, key, defaultValue)
	if err != nil {
		return nil, err
	}
	var slice []T
	rawBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal pointer list %s/%s: %w", module, key, err)
	}
	if err := json.Unmarshal(rawBytes, &slice); err != nil {
		return nil, fmt.Errorf("decode pointer list %s/%s: %w", module, key, err)
	}
	if slice == nil {
		slice = []T{}
	}
	return &PointerList[T]{
		db:     db,
		module: module,
		key:    key,
		values: slice,
	}, nil
}

// NewPointerList preserves the original constructor contract. On load failure
// it logs and uses a defensive copy of defaultValue; use NewPointerListChecked
// when an empty/default value is not safe.
func NewPointerList[T any](db *Database, module, key string, defaultValue []T) *PointerList[T] {
	pointer, err := NewPointerListChecked(db, module, key, defaultValue)
	if err == nil {
		return pointer
	}
	L().Error("PointerList load failed; using caller-provided fallback", zap.String("owner", module), zap.String("key", key), zap.Error(err))
	values := clonePointerValue(defaultValue)
	if values == nil {
		values = []T{}
	}
	return &PointerList[T]{db: db, module: module, key: key, values: values}
}

func (p *PointerList[T]) Save() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := clonePointerValue(p.values)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerList[T]) Append(val T) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := clonePointerValue(p.values)
	candidate = append(candidate, clonePointerValue(val))
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerList[T]) Extend(vals []T) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := append(clonePointerValue(p.values), clonePointerValue(vals)...)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerList[T]) Set(index int, val T) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.values) {
		return nil
	}
	candidate := clonePointerValue(p.values)
	candidate[index] = clonePointerValue(val)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

// Get returns the value at index and whether it exists.
func (p *PointerList[T]) Get(index int) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var zero T
	if index >= 0 && index < len(p.values) {
		return clonePointerValue(p.values[index]), true
	}
	return zero, false
}

func (p *PointerList[T]) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.values)
}

func (p *PointerList[T]) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := []T{}
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerList[T]) Remove(index int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.values) {
		return nil
	}
	candidate := clonePointerValue(p.values)
	candidate = append(candidate[:index], candidate[index+1:]...)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerList[T]) ToSlice() []T {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return clonePointerValue(p.values)
}

// PointerDict is a generic persisted string-keyed map backed by the Database.
type PointerDict[T any] struct {
	mu     sync.RWMutex
	db     *Database
	module string
	key    string
	values map[string]T
}

// NewPointerDictChecked loads or creates a persisted map for the given value type.
func NewPointerDictChecked[T any](db *Database, module, key string, defaultValue map[string]T) (*PointerDict[T], error) {
	if db == nil {
		return nil, databaseError("get", module, key, "", ErrDatabaseNotInitialized, nil)
	}
	raw, err := db.Get(module, key, defaultValue)
	if err != nil {
		return nil, err
	}
	dict := make(map[string]T)
	rawBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal pointer dict %s/%s: %w", module, key, err)
	}
	if err := json.Unmarshal(rawBytes, &dict); err != nil {
		return nil, fmt.Errorf("decode pointer dict %s/%s: %w", module, key, err)
	}
	if dict == nil {
		dict = make(map[string]T)
	}
	return &PointerDict[T]{
		db:     db,
		module: module,
		key:    key,
		values: dict,
	}, nil
}

// NewPointerDict preserves the original constructor contract. On load failure
// it logs and uses a defensive copy of defaultValue; use NewPointerDictChecked
// when an empty/default value is not safe.
func NewPointerDict[T any](db *Database, module, key string, defaultValue map[string]T) *PointerDict[T] {
	pointer, err := NewPointerDictChecked(db, module, key, defaultValue)
	if err == nil {
		return pointer
	}
	L().Error("PointerDict load failed; using caller-provided fallback", zap.String("owner", module), zap.String("key", key), zap.Error(err))
	values := clonePointerValue(defaultValue)
	if values == nil {
		values = make(map[string]T)
	}
	return &PointerDict[T]{db: db, module: module, key: key, values: values}
}

func (p *PointerDict[T]) Save() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := clonePointerValue(p.values)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerDict[T]) Set(key string, val T) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := clonePointerValue(p.values)
	candidate[key] = clonePointerValue(val)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

// Get returns the value for key and whether it exists.
func (p *PointerDict[T]) Get(key string) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	val, ok := p.values[key]
	return clonePointerValue(val), ok
}

func (p *PointerDict[T]) Delete(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := clonePointerValue(p.values)
	delete(candidate, key)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerDict[T]) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate := make(map[string]T)
	err := p.db.Set(p.module, p.key, candidate)
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	p.values = candidate
	return err
}

func (p *PointerDict[T]) ToMap() map[string]T {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return clonePointerValue(p.values)
}

func clonePointerValue[T any](value T) T {
	cloned := deepCopyValue(value)
	if cloned == nil {
		var zero T
		return zero
	}
	return cloned.(T)
}
