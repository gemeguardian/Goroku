package cache

import "time"

// PrivateChatPerms is the typed permission snapshot for private (user) peers.
// Channel/chat peers store tg.ChannelParticipantClass / tg.ChatParticipantClass.
type PrivateChatPerms struct {
	IsPrivate bool
}

// CacheRecordPerms holds a permission snapshot.
// Perms is one of:
//   - tg.ChannelParticipantClass
//   - tg.ChatParticipantClass
//   - PrivateChatPerms
//
// Residual: gotd participant unions remain interface types (not a single concrete struct).
type CacheRecordPerms struct {
	Perms any
	Exp   int64
	TS    int64
}

func (r CacheRecordPerms) Expired() bool {
	return r.Exp < time.Now().Unix()
}
