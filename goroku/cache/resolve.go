package cache

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
)

func TelegramChannelChatID(channelID int64) int64 {
	return -1000000000000 - channelID
}

func NormalizeEntityCacheKey(entity interface{}) interface{} {
	switch v := entity.(type) {
	case string:
		s := strings.ToLower(strings.TrimPrefix(v, "@"))
		if strings.HasPrefix(s, "-100") {
			if id, err := strconv.ParseInt(strings.TrimPrefix(s, "-100"), 10, 64); err == nil {
				return id
			}
		}
		return s
	case int:
		return NormalizeEntityCacheKey(int64(v))
	case int64:
		if v < -1000000000000 {
			return -(v + 1000000000000)
		}
		if v < 0 {
			return -v
		}
		return v
	case tg.InputPeerClass:
		switch p := v.(type) {
		case *tg.InputPeerUser:
			return p.UserID
		case *tg.InputPeerChannel:
			return p.ChannelID
		case *tg.InputPeerChat:
			return p.ChatID
		}
	}
	return entity
}

func CachePeerAliases(cache map[interface{}]CacheRecordEntity, peer tg.InputPeerClass, record CacheRecordEntity) {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		cache[p.UserID] = record
	case *tg.InputPeerChannel:
		cache[p.ChannelID] = record
		cache[TelegramChannelChatID(p.ChannelID)] = record
	case *tg.InputPeerChat:
		cache[p.ChatID] = record
		cache[-p.ChatID] = record
	}
}

func InputPeerUserID(peer tg.InputPeerClass) int64 {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return p.UserID
	case *tg.InputPeerSelf:
		return 0
	}
	return 0
}

func ChatParticipantUserID(participant tg.ChatParticipantClass) int64 {
	switch p := participant.(type) {
	case *tg.ChatParticipant:
		return p.UserID
	case *tg.ChatParticipantAdmin:
		return p.UserID
	case *tg.ChatParticipantCreator:
		return p.UserID
	}
	return 0
}

func InputUserFromPeer(peer tg.InputPeerClass) (tg.InputUserClass, error) {
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		return &tg.InputUserSelf{}, nil
	case *tg.InputPeerUser:
		return &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}, nil
	default:
		return nil, fmt.Errorf("peer %T is not a user", peer)
	}
}
