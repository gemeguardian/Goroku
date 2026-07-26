package goroku

import (
	"fmt"
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

// GetFullChannel returns a full channel profile, using the full-channel cache when fresh.
// requestTTL==0 means accept any present cache entry (see cache.UseCached).
func (c *CustomTelegramClient) GetFullChannel(entity any, exp int64, force bool) (*tg.MessagesChatFull, error) {
	cacheKey := cache.NormalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuFullChannelCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && cache.UseCached(exp, record.Expired()) {
			return record.Channel, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	channelPeer, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, fmt.Errorf("entity %v is not a channel", entity)
	}

	fullChannel, err := c.rawAPI.ChannelsGetFullChannel(c.ctx, &tg.InputChannel{ChannelID: channelPeer.ChannelID, AccessHash: channelPeer.AccessHash})
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	c.cacheMu.Lock()
	if c.GorokuFullChannelCache == nil {
		c.GorokuFullChannelCache = make(map[cache.EntityCacheKey]cache.CacheRecordFullChannel)
	}
	c.sweepFullChannelCacheLocked(now)
	c.GorokuFullChannelCache[cacheKey] = cache.CacheRecordFullChannel{
		Channel: fullChannel,
		Exp:     cache.CacheExpiryUnix(now, exp),
		TS:      now,
	}
	c.cacheMu.Unlock()

	return fullChannel, nil
}

// GetFullChannelRef is the typed ChannelRef entry point for GetFullChannel.
func (c *CustomTelegramClient) GetFullChannelRef(entity ChannelRef, exp int64, force bool) (*tg.MessagesChatFull, error) {
	return c.GetFullChannel(entity, exp, force)
}
