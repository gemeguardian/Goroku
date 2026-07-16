package cache

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
)

// EntityCacheKey is a normalized key for entity cache maps.
// It can represent either a numeric ID (user/chat/channel) or a username.
type EntityCacheKey struct {
	ID       int64
	Username string
}

// IsUsername reports whether the key is a username lookup.
func (k EntityCacheKey) IsUsername() bool { return k.Username != "" }

func (k EntityCacheKey) String() string {
	if k.IsUsername() {
		return k.Username
	}
	return fmt.Sprintf("%d", k.ID)
}

func (k EntityCacheKey) IsZero() bool { return k.ID == 0 && k.Username == "" }

// TelegramChannelChatID returns the bot API style channel chat ID.
func TelegramChannelChatID(channelID int64) int64 {
	return -1000000000000 - channelID
}

// legacyEntity is implemented by chatref.ChatRef (and aliases EntityRef/UserRef/ChannelRef).
type legacyEntity interface {
	AsLegacy() any
}

// NormalizeEntityCacheKey converts peers, IDs, usernames, ChatRef, and EntityCacheKey into a typed key.
func NormalizeEntityCacheKey(entity any) EntityCacheKey {
	switch v := entity.(type) {
	case string:
		s := strings.ToLower(strings.TrimPrefix(v, "@"))
		if strings.HasPrefix(s, "-100") {
			if id, err := strconv.ParseInt(strings.TrimPrefix(s, "-100"), 10, 64); err == nil {
				return EntityCacheKey{ID: id}
			}
		}
		return EntityCacheKey{Username: s}
	case int:
		return NormalizeEntityCacheKey(int64(v))
	case int64:
		return EntityCacheKey{ID: normalizeID(v)}
	case tg.InputPeerClass:
		return peerCacheKey(v)
	case EntityCacheKey:
		if v.Username != "" {
			return EntityCacheKey{Username: strings.ToLower(strings.TrimPrefix(v.Username, "@"))}
		}
		return EntityCacheKey{ID: normalizeID(v.ID)}
	case legacyEntity:
		return NormalizeEntityCacheKey(v.AsLegacy())
	}
	return EntityCacheKey{}
}

func normalizeID(v int64) int64 {
	if v < -1000000000000 {
		return -(v + 1000000000000)
	}
	if v < 0 {
		return -v
	}
	return v
}

func peerCacheKey(peer tg.InputPeerClass) EntityCacheKey {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return EntityCacheKey{ID: p.UserID}
	case *tg.InputPeerChannel:
		return EntityCacheKey{ID: p.ChannelID}
	case *tg.InputPeerChat:
		return EntityCacheKey{ID: p.ChatID}
	}
	return EntityCacheKey{}
}

// CachePeerAliases stores a record under all common aliases for the peer.
func CachePeerAliases(cache map[EntityCacheKey]CacheRecordEntity, peer tg.InputPeerClass, record CacheRecordEntity) {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		cache[EntityCacheKey{ID: p.UserID}] = record
	case *tg.InputPeerChannel:
		cache[EntityCacheKey{ID: p.ChannelID}] = record
		cache[EntityCacheKey{ID: TelegramChannelChatID(p.ChannelID)}] = record
	case *tg.InputPeerChat:
		cache[EntityCacheKey{ID: p.ChatID}] = record
		cache[EntityCacheKey{ID: -p.ChatID}] = record
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
