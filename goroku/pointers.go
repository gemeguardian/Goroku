package goroku

import (
	"encoding/json"
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

// NewPointerList loads or creates a persisted list for the given type.
func NewPointerList[T any](db *Database, module, key string, defaultValue []T) *PointerList[T] {
	raw, _ := db.Get(module, key, defaultValue)
	var slice []T
	if rawBytes, err := json.Marshal(raw); err == nil {
		if err := json.Unmarshal(rawBytes, &slice); err != nil {
			L().Warn("PointerList: failed to unmarshal", zap.Error(err))
		}
	}
	if slice == nil {
		slice = []T{}
	}
	return &PointerList[T]{
		db:     db,
		module: module,
		key:    key,
		values: slice,
	}
}

func (p *PointerList[T]) Save() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.db.Set(p.module, p.key, p.values)
}

func (p *PointerList[T]) Append(val T) {
	p.mu.Lock()
	p.values = append(p.values, val)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList[T]) Extend(vals []T) {
	p.mu.Lock()
	p.values = append(p.values, vals...)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList[T]) Set(index int, val T) {
	p.mu.Lock()
	if index >= 0 && index < len(p.values) {
		p.values[index] = val
	}
	p.mu.Unlock()
	p.Save()
}

// Get returns the value at index and whether it exists.
func (p *PointerList[T]) Get(index int) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var zero T
	if index >= 0 && index < len(p.values) {
		return p.values[index], true
	}
	return zero, false
}

func (p *PointerList[T]) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.values)
}

func (p *PointerList[T]) Clear() {
	p.mu.Lock()
	p.values = []T{}
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList[T]) Remove(index int) {
	p.mu.Lock()
	if index >= 0 && index < len(p.values) {
		p.values = append(p.values[:index], p.values[index+1:]...)
	}
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList[T]) ToSlice() []T {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make([]T, len(p.values))
	copy(res, p.values)
	return res
}

// PointerDict is a generic persisted string-keyed map backed by the Database.
type PointerDict[T any] struct {
	mu     sync.RWMutex
	db     *Database
	module string
	key    string
	values map[string]T
}

// NewPointerDict loads or creates a persisted map for the given value type.
func NewPointerDict[T any](db *Database, module, key string, defaultValue map[string]T) *PointerDict[T] {
	raw, _ := db.Get(module, key, defaultValue)
	dict := make(map[string]T)
	if rawBytes, err := json.Marshal(raw); err == nil {
		if err := json.Unmarshal(rawBytes, &dict); err != nil {
			L().Warn("PointerDict: failed to unmarshal", zap.Error(err))
		}
	}
	if dict == nil {
		dict = make(map[string]T)
	}
	return &PointerDict[T]{
		db:     db,
		module: module,
		key:    key,
		values: dict,
	}
}

func (p *PointerDict[T]) Save() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.db.Set(p.module, p.key, p.values)
}

func (p *PointerDict[T]) Set(key string, val T) {
	p.mu.Lock()
	p.values[key] = val
	p.mu.Unlock()
	p.Save()
}

// Get returns the value for key and whether it exists.
func (p *PointerDict[T]) Get(key string) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	val, ok := p.values[key]
	return val, ok
}

func (p *PointerDict[T]) Delete(key string) {
	p.mu.Lock()
	delete(p.values, key)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerDict[T]) Clear() {
	p.mu.Lock()
	p.values = make(map[string]T)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerDict[T]) ToMap() map[string]T {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make(map[string]T, len(p.values))
	for k, v := range p.values {
		res[k] = v
	}
	return res
}
