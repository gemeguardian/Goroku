package goroku

import (
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

// GetEntity resolves entity to an InputPeer, using the entity cache when fresh.
// entity may be int64, string username, tg.InputPeerClass, ChatRef/EntityRef, or EntityCacheKey.
// requestTTL==0 means accept any present cache entry (see cache.UseCached).
func (c *CustomTelegramClient) GetEntity(entity any, exp int64, force bool) (tg.InputPeerClass, error) {
	cacheKey := cache.NormalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && cache.UseCached(exp, record.Expired()) {
			return record.Entity, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	record := cache.CacheRecordEntity{
		Entity: peer,
		Exp:    cache.CacheExpiryUnix(now, exp),
		TS:     now,
	}
	c.cacheMu.Lock()
	if c.GorokuEntityCache == nil {
		c.GorokuEntityCache = make(map[cache.EntityCacheKey]cache.CacheRecordEntity)
	}
	c.GorokuEntityCache[cacheKey] = record
	cache.CachePeerAliases(c.GorokuEntityCache, peer, record)
	c.cacheMu.Unlock()
	return peer, nil
}

// GetEntityRef is the typed ChatRef/EntityRef entry point for GetEntity.
func (c *CustomTelegramClient) GetEntityRef(entity ChatRef, exp int64, force bool) (tg.InputPeerClass, error) {
	return c.GetEntity(entity, exp, force)
}
