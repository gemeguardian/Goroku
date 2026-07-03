package goroku

import (
	"time"

	"goroku/goroku/cache"
)

func (c *CustomTelegramClient) GetFullUser(entity interface{}, exp int64, force bool) (interface{}, error) {
	cacheKey := cache.NormalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuFullUserCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && !record.Expired() {
			return record.User, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	inputUser, err := cache.InputUserFromPeer(peer)
	if err != nil {
		return nil, err
	}

	fullUser, err := c.rawAPI.UsersGetFullUser(c.ctx, inputUser)
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	c.GorokuFullUserCache[cacheKey] = cache.CacheRecordFullUser{
		User: fullUser,
		Exp:  time.Now().Unix() + exp,
		TS:   time.Now().Unix(),
	}
	c.cacheMu.Unlock()

	return fullUser, nil
}
