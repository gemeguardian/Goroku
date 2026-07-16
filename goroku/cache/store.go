package cache

import (
	"sync"

	"github.com/gotd/td/tg"
)

// EntityStore owns the entity / full-user / full-channel / perms maps and their
// mutex. CustomTelegramClient still embeds the maps for source compatibility
// (M6.5 residual); hot paths can migrate to EntityStore incrementally.
type EntityStore struct {
	mu          sync.RWMutex
	Entity      map[EntityCacheKey]CacheRecordEntity
	FullUser    map[EntityCacheKey]CacheRecordFullUser
	FullChannel map[EntityCacheKey]CacheRecordFullChannel
	Perms       map[EntityCacheKey]map[EntityCacheKey]CacheRecordPerms
}

// NewEntityStore allocates empty cache maps.
func NewEntityStore() *EntityStore {
	return &EntityStore{
		Entity:      make(map[EntityCacheKey]CacheRecordEntity),
		FullUser:    make(map[EntityCacheKey]CacheRecordFullUser),
		FullChannel: make(map[EntityCacheKey]CacheRecordFullChannel),
		Perms:       make(map[EntityCacheKey]map[EntityCacheKey]CacheRecordPerms),
	}
}

// RLock locks the store for reading.
func (s *EntityStore) RLock() {
	if s != nil {
		s.mu.RLock()
	}
}

// RUnlock unlocks the store after a read.
func (s *EntityStore) RUnlock() {
	if s != nil {
		s.mu.RUnlock()
	}
}

// Lock locks the store for writing.
func (s *EntityStore) Lock() {
	if s != nil {
		s.mu.Lock()
	}
}

// Unlock unlocks the store after a write.
func (s *EntityStore) Unlock() {
	if s != nil {
		s.mu.Unlock()
	}
}

// PutEntity stores a peer under the primary key and common aliases.
func (s *EntityStore) PutEntity(key EntityCacheKey, peer tg.InputPeerClass, exp, ts int64) {
	if s == nil {
		return
	}
	if s.Entity == nil {
		s.Entity = make(map[EntityCacheKey]CacheRecordEntity)
	}
	record := CacheRecordEntity{Entity: peer, Exp: exp, TS: ts}
	s.Entity[key] = record
	CachePeerAliases(s.Entity, peer, record)
}

// GetEntity returns a non-expired (per requestTTL) entity peer.
func (s *EntityStore) GetEntity(key EntityCacheKey, requestTTL int64) (tg.InputPeerClass, bool) {
	if s == nil || s.Entity == nil {
		return nil, false
	}
	record, ok := s.Entity[key]
	if !ok || !UseCached(requestTTL, record.Expired()) {
		return nil, false
	}
	return record.Entity, true
}
