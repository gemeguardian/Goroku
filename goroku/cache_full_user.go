package goroku

import (
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

// GetFullUser returns a full user profile, using the full-user cache when fresh.
// requestTTL==0 means accept any present cache entry (see cache.UseCached).
func (c *CustomTelegramClient) GetFullUser(entity any, exp int64, force bool) (*tg.UsersUserFull, error) {
	cacheKey := cache.NormalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuFullUserCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && cache.UseCached(exp, record.Expired()) {
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

	now := time.Now().Unix()
	c.cacheMu.Lock()
	if c.GorokuFullUserCache == nil {
		c.GorokuFullUserCache = make(map[cache.EntityCacheKey]cache.CacheRecordFullUser)
	}
	c.GorokuFullUserCache[cacheKey] = cache.CacheRecordFullUser{
		User: fullUser,
		Exp:  cache.CacheExpiryUnix(now, exp),
		TS:   now,
	}
	c.cacheMu.Unlock()

	return fullUser, nil
}

// GetFullUserRef is the typed UserRef entry point for GetFullUser.
func (c *CustomTelegramClient) GetFullUserRef(entity UserRef, exp int64, force bool) (*tg.UsersUserFull, error) {
	return c.GetFullUser(entity, exp, force)
}
