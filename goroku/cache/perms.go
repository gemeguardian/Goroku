package cache

import (
	"time"

	"github.com/gotd/td/tg"
)

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

// AsPrivate returns PrivateChatPerms when the record stores a private-chat snapshot.
func (r CacheRecordPerms) AsPrivate() (PrivateChatPerms, bool) {
	if p, ok := r.Perms.(PrivateChatPerms); ok {
		return p, true
	}
	if p, ok := r.Perms.(*PrivateChatPerms); ok && p != nil {
		return *p, true
	}
	return PrivateChatPerms{}, false
}

// AsChannelParticipant returns a channel participant when stored under that union.
func (r CacheRecordPerms) AsChannelParticipant() (tg.ChannelParticipantClass, bool) {
	p, ok := r.Perms.(tg.ChannelParticipantClass)
	return p, ok
}

// AsChatParticipant returns a basic-chat participant when stored under that union.
func (r CacheRecordPerms) AsChatParticipant() (tg.ChatParticipantClass, bool) {
	p, ok := r.Perms.(tg.ChatParticipantClass)
	return p, ok
}
