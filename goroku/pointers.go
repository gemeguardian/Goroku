package goroku

import (
	"encoding/json"
	"sync"
)

type PointerList struct {
	mu     sync.RWMutex
	db     *Database
	module string
	key    string
	values []interface{}
}

func NewPointerList(db *Database, module, key string, defaultValue []interface{}) *PointerList {
	raw := db.Get(module, key, defaultValue)
	var slice []interface{}
	if rawBytes, err := json.Marshal(raw); err == nil {
		json.Unmarshal(rawBytes, &slice)
	}
	if slice == nil {
		slice = []interface{}{}
	}
	return &PointerList{
		db:     db,
		module: module,
		key:    key,
		values: slice,
	}
}

func (p *PointerList) Save() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.db.Set(p.module, p.key, p.values)
}

func (p *PointerList) Append(val interface{}) {
	p.mu.Lock()
	p.values = append(p.values, val)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList) Extend(vals []interface{}) {
	p.mu.Lock()
	p.values = append(p.values, vals...)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList) Set(index int, val interface{}) {
	p.mu.Lock()
	if index >= 0 && index < len(p.values) {
		p.values[index] = val
	}
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList) Get(index int) interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if index >= 0 && index < len(p.values) {
		return p.values[index]
	}
	return nil
}

func (p *PointerList) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.values)
}

func (p *PointerList) Clear() {
	p.mu.Lock()
	p.values = []interface{}{}
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList) Remove(index int) {
	p.mu.Lock()
	if index >= 0 && index < len(p.values) {
		p.values = append(p.values[:index], p.values[index+1:]...)
	}
	p.mu.Unlock()
	p.Save()
}

func (p *PointerList) ToSlice() []interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make([]interface{}, len(p.values))
	copy(res, p.values)
	return res
}

type PointerDict struct {
	mu     sync.RWMutex
	db     *Database
	module string
	key    string
	values map[string]interface{}
}

func NewPointerDict(db *Database, module, key string, defaultValue map[string]interface{}) *PointerDict {
	raw := db.Get(module, key, defaultValue)
	dict := make(map[string]interface{})
	if rawBytes, err := json.Marshal(raw); err == nil {
		json.Unmarshal(rawBytes, &dict)
	}
	if dict == nil {
		dict = make(map[string]interface{})
	}
	return &PointerDict{
		db:     db,
		module: module,
		key:    key,
		values: dict,
	}
}

func (p *PointerDict) Save() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.db.Set(p.module, p.key, p.values)
}

func (p *PointerDict) Set(key string, val interface{}) {
	p.mu.Lock()
	p.values[key] = val
	p.mu.Unlock()
	p.Save()
}

func (p *PointerDict) Get(key string) interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.values[key]
}

func (p *PointerDict) Delete(key string) {
	p.mu.Lock()
	delete(p.values, key)
	p.mu.Unlock()
	p.Save()
}

func (p *PointerDict) Clear() {
	p.mu.Lock()
	p.values = make(map[string]interface{})
	p.mu.Unlock()
	p.Save()
}

func (p *PointerDict) ToMap() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make(map[string]interface{})
	for k, v := range p.values {
		res[k] = v
	}
	return res
}
