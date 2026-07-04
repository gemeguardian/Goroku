package goroku

import (
	"fmt"
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

func (c *CustomTelegramClient) GetPermsCached(entity any, user any, exp int64, force bool) (any, error) {
	entityKey := cache.NormalizeEntityCacheKey(entity)
	userKey := cache.NormalizeEntityCacheKey(user)
	if !force {
		c.cacheMu.RLock()
		var record cache.CacheRecordPerms
		var ok bool
		if subMap, exists := c.GorokuPermsCache[entityKey]; exists {
			record, ok = subMap[userKey]
		}
		c.cacheMu.RUnlock()
		if ok && (exp == 0 || !record.Expired()) {
			return record.Perms, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	if user == nil {
		user = c.TGID
		userKey = cache.EntityCacheKey{ID: c.TGID}
	}
	userPeer, err := c.ResolvePeer(user)
	if err != nil {
		return nil, err
	}

	perms, err := c.fetchPermissions(peer, userPeer)
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	if _, ok := c.GorokuPermsCache[entityKey]; !ok {
		c.GorokuPermsCache[entityKey] = make(map[cache.EntityCacheKey]cache.CacheRecordPerms)
	}

	c.GorokuPermsCache[entityKey][userKey] = cache.CacheRecordPerms{
		Perms: perms,
		Exp:   time.Now().Unix() + exp,
		TS:    time.Now().Unix(),
	}
	c.cacheMu.Unlock()

	return perms, nil
}

func (c *CustomTelegramClient) fetchPermissions(peer tg.InputPeerClass, userPeer tg.InputPeerClass) (any, error) {
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		res, err := c.rawAPI.ChannelsGetParticipant(c.ctx, &tg.ChannelsGetParticipantRequest{
			Channel:     &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
			Participant: userPeer,
		})
		if err != nil {
			return nil, err
		}
		return res.Participant, nil
	case *tg.InputPeerChat:
		res, err := c.rawAPI.MessagesGetFullChat(c.ctx, p.ChatID)
		if err != nil {
			return nil, err
		}
		full, ok := res.FullChat.(*tg.ChatFull)
		if !ok {
			return nil, fmt.Errorf("unexpected full chat type %T", res.FullChat)
		}
		participants, ok := full.Participants.AsNotForbidden()
		if !ok {
			return nil, fmt.Errorf("chat participants are forbidden")
		}
		userID := cache.InputPeerUserID(userPeer)
		if userID == 0 {
			userID = c.TGID
		}
		for _, participant := range participants.Participants {
			if cache.ChatParticipantUserID(participant) == userID {
				return participant, nil
			}
		}
		return nil, fmt.Errorf("participant %d not found", userID)
	case *tg.InputPeerUser, *tg.InputPeerSelf:
		return map[string]any{"is_private": true}, nil
	default:
		return nil, fmt.Errorf("unsupported peer type %T", peer)
	}
}
