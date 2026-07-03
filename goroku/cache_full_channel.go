package goroku

import (
	"fmt"
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

func (c *CustomTelegramClient) GetFullChannel(entity interface{}, exp int64, force bool) (interface{}, error) {
	cacheKey := cache.NormalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuFullChannelCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && !record.Expired() {
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

	c.cacheMu.Lock()
	c.GorokuFullChannelCache[cacheKey] = cache.CacheRecordFullChannel{
		Channel: fullChannel,
		Exp:     time.Now().Unix() + exp,
		TS:      time.Now().Unix(),
	}
	c.cacheMu.Unlock()

	return fullChannel, nil
}
