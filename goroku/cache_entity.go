package goroku

import (
	"time"

	"goroku/goroku/cache"
)

func (c *CustomTelegramClient) GetEntity(entity interface{}, exp int64, force bool) (interface{}, error) {
	cacheKey := cache.NormalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && (exp == 0 || !record.Expired()) {
			return record.Entity, nil
		}
	}

	// Resolve actual peer info if possible
	peer, err := c.ResolvePeer(entity)
	if err == nil {
		record := cache.CacheRecordEntity{
			Entity: peer,
			Exp:    time.Now().Unix() + exp,
			TS:     time.Now().Unix(),
		}
		c.cacheMu.Lock()
		c.GorokuEntityCache[cacheKey] = record
		cache.CachePeerAliases(c.GorokuEntityCache, peer, record)
		c.cacheMu.Unlock()
		return peer, nil
	}

	return nil, err
}
