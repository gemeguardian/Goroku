package goroku

import (
	"time"

	"goroku/goroku/cache"
)

// Entity cache limits.
//
// Records carry a 30-day Exp but nothing ever deleted them: the bot adds one to
// three entries (by ID, by username, aliases) for every new participant it sees
// in an active group, so the maps grew for the whole process lifetime and were
// only freed by a restart. Each cache is now swept for expired entries on write
// and hard-capped, following setAdminCache/sweepAdminCacheLocked in security.go.
//
// The caps are sized to be generous for a userbot in busy groups while still
// bounding worst-case memory: entity records are small, full user/channel
// records are considerably larger, so they are capped lower.
const (
	entityCacheMaxEntries      = 20000
	permsCacheMaxEntities      = 4096
	permsCacheMaxUsersPerChat  = 1024
	fullUserCacheMaxEntries    = 4096
	fullChannelCacheMaxEntries = 2048
)

// evictOldest drops entries until the map is under limit, oldest TS first.
// Used only when sweeping expired entries did not free enough room.
func evictOldest[V any](m map[cache.EntityCacheKey]V, limit int, ts func(V) int64) {
	for len(m) >= limit {
		var oldestKey cache.EntityCacheKey
		var oldestTS int64
		first := true
		for key, value := range m {
			if t := ts(value); first || t < oldestTS {
				oldestKey, oldestTS, first = key, t, false
			}
		}
		if first {
			return
		}
		delete(m, oldestKey)
	}
}

// sweepEntityCacheLocked drops expired entity records and, if the map is still
// at its limit, the oldest ones. c.cacheMu must be held for writing.
func (c *CustomTelegramClient) sweepEntityCacheLocked(now int64) {
	if len(c.GorokuEntityCache) < entityCacheMaxEntries {
		return
	}
	for key, record := range c.GorokuEntityCache {
		if record.Exp > 0 && record.Exp < now {
			delete(c.GorokuEntityCache, key)
		}
	}
	evictOldest(c.GorokuEntityCache, entityCacheMaxEntries, func(r cache.CacheRecordEntity) int64 { return r.TS })
}

// sweepPermsCacheLocked drops expired permission records. The outer level is
// dropped once its inner map empties, otherwise the outer map keeps growing
// with empty entries for every chat ever seen. c.cacheMu must be held.
func (c *CustomTelegramClient) sweepPermsCacheLocked(now int64) {
	for entityKey, users := range c.GorokuPermsCache {
		for userKey, record := range users {
			if record.Exp > 0 && record.Exp < now {
				delete(users, userKey)
			}
		}
		if len(users) == 0 {
			delete(c.GorokuPermsCache, entityKey)
			continue
		}
		evictOldest(users, permsCacheMaxUsersPerChat, func(r cache.CacheRecordPerms) int64 { return r.TS })
	}
	if len(c.GorokuPermsCache) >= permsCacheMaxEntities {
		// Evict whole chats by their most recent entry: a chat nobody has
		// touched in a while is the cheapest to re-fetch.
		evictOldestPerms(c.GorokuPermsCache, permsCacheMaxEntities)
	}
}

// evictOldestPerms drops least recently used chats from the two-level map.
func evictOldestPerms(m map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms, limit int) {
	newest := func(users map[cache.EntityCacheKey]cache.CacheRecordPerms) int64 {
		var max int64
		for _, record := range users {
			if record.TS > max {
				max = record.TS
			}
		}
		return max
	}
	for len(m) >= limit {
		var oldestKey cache.EntityCacheKey
		var oldestTS int64
		first := true
		for key, users := range m {
			if t := newest(users); first || t < oldestTS {
				oldestKey, oldestTS, first = key, t, false
			}
		}
		if first {
			return
		}
		delete(m, oldestKey)
	}
}

// sweepFullUserCacheLocked bounds the full-user cache. c.cacheMu must be held.
func (c *CustomTelegramClient) sweepFullUserCacheLocked(now int64) {
	if len(c.GorokuFullUserCache) < fullUserCacheMaxEntries {
		return
	}
	for key, record := range c.GorokuFullUserCache {
		if record.Exp > 0 && record.Exp < now {
			delete(c.GorokuFullUserCache, key)
		}
	}
	evictOldest(c.GorokuFullUserCache, fullUserCacheMaxEntries, func(r cache.CacheRecordFullUser) int64 { return r.TS })
}

// sweepFullChannelCacheLocked bounds the full-channel cache. c.cacheMu held.
func (c *CustomTelegramClient) sweepFullChannelCacheLocked(now int64) {
	if len(c.GorokuFullChannelCache) < fullChannelCacheMaxEntries {
		return
	}
	for key, record := range c.GorokuFullChannelCache {
		if record.Exp > 0 && record.Exp < now {
			delete(c.GorokuFullChannelCache, key)
		}
	}
	evictOldest(c.GorokuFullChannelCache, fullChannelCacheMaxEntries, func(r cache.CacheRecordFullChannel) int64 { return r.TS })
}

// sweepEntityCache is the exported-for-tests entry point using the wall clock.
func (c *CustomTelegramClient) sweepCachesNow() {
	now := time.Now().Unix()
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	c.sweepEntityCacheLocked(now)
	c.sweepPermsCacheLocked(now)
	c.sweepFullUserCacheLocked(now)
	c.sweepFullChannelCacheLocked(now)
}
