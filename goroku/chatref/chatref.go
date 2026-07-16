package chatref

import (
	"github.com/gotd/td/tg"
)

// ChatRef is a typed reference to a Telegram chat that can be provided as a
// numeric ID, a username, or an already-resolved input peer. It replaces the
// previous chat any parameters in the high-level client methods.
type ChatRef struct {
	id       int64
	username string
	peer     tg.InputPeerClass
}

// EntityRef is the preferred name for a generic peer/entity reference (M7).
// It is an alias of ChatRef so cache and client APIs share one concrete type.
type EntityRef = ChatRef

// UserRef is a ChatRef that callers intend as a user identity.
type UserRef = ChatRef

// ChannelRef is a ChatRef that callers intend as a channel/supergroup identity.
type ChannelRef = ChatRef

// ID builds a reference from a numeric chat/user/channel ID.
func ID(id int64) ChatRef { return ChatRef{id: id} }

// Username builds a reference from a username (with or without @).
func Username(username string) ChatRef { return ChatRef{username: username} }

// Peer builds a reference from an already-resolved Telegram peer.
func Peer(peer tg.InputPeerClass) ChatRef { return ChatRef{peer: peer} }

// IsZero reports whether the reference has no value set.
func (r ChatRef) IsZero() bool { return r.id == 0 && r.username == "" && r.peer == nil }

// ID returns the numeric ID when the reference was built from one.
func (r ChatRef) ID() int64 { return r.id }

// Username returns the username when the reference was built from one.
func (r ChatRef) Username() string { return r.username }

// Peer returns the resolved peer when the reference was built from one.
func (r ChatRef) Peer() tg.InputPeerClass { return r.peer }

// AsLegacy returns the underlying untyped value previously accepted by
// ResolvePeer. This is kept for internal compatibility during migration.
func (r ChatRef) AsLegacy() any {
	if r.peer != nil {
		return r.peer
	}
	if r.username != "" {
		return r.username
	}
	return r.id
}
