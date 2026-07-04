package cache

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestCacheRecordExpired(t *testing.T) {
	now := time.Now().Unix()

	// Entity Record
	rEntityExpired := CacheRecordEntity{Exp: now - 10}
	rEntityActive := CacheRecordEntity{Exp: now + 10}
	if !rEntityExpired.Expired() {
		t.Error("expected entity record to be expired")
	}
	if rEntityActive.Expired() {
		t.Error("expected entity record not to be expired")
	}

	// Full Channel Record
	rChanExpired := CacheRecordFullChannel{Exp: now - 10}
	rChanActive := CacheRecordFullChannel{Exp: now + 10}
	if !rChanExpired.Expired() {
		t.Error("expected channel record to be expired")
	}
	if rChanActive.Expired() {
		t.Error("expected channel record not to be expired")
	}

	// Full User Record
	rUserExpired := CacheRecordFullUser{Exp: now - 10}
	rUserActive := CacheRecordFullUser{Exp: now + 10}
	if !rUserExpired.Expired() {
		t.Error("expected user record to be expired")
	}
	if rUserActive.Expired() {
		t.Error("expected user record not to be expired")
	}

	// Perms Record
	rPermsExpired := CacheRecordPerms{Exp: now - 10}
	rPermsActive := CacheRecordPerms{Exp: now + 10}
	if !rPermsExpired.Expired() {
		t.Error("expected perms record to be expired")
	}
	if rPermsActive.Expired() {
		t.Error("expected perms record not to be expired")
	}
}

func TestNormalizeEntityCacheKey(t *testing.T) {
	// string normalize
	if got := NormalizeEntityCacheKey("@username"); got != (EntityCacheKey{Username: "username"}) {
		t.Errorf("NormalizeEntityCacheKey failed for username: got %v, want %q", got, "username")
	}
	if got := NormalizeEntityCacheKey("-10012345"); got != (EntityCacheKey{ID: 12345}) {
		t.Errorf("NormalizeEntityCacheKey failed for -100 ID: got %v, want 12345", got)
	}

	// int64 normalize
	if got := NormalizeEntityCacheKey(int64(-1000000000042)); got != (EntityCacheKey{ID: 42}) {
		t.Errorf("NormalizeEntityCacheKey failed for channel ID: got %v, want 42", got)
	}
	if got := NormalizeEntityCacheKey(int64(-50)); got != (EntityCacheKey{ID: 50}) {
		t.Errorf("NormalizeEntityCacheKey failed for group ID: got %v, want 50", got)
	}
	if got := NormalizeEntityCacheKey(int64(777)); got != (EntityCacheKey{ID: 777}) {
		t.Errorf("NormalizeEntityCacheKey failed for user ID: got %v, want 777", got)
	}

	// tg.InputPeerClass normalize
	peerUser := &tg.InputPeerUser{UserID: 111}
	if got := NormalizeEntityCacheKey(peerUser); got != (EntityCacheKey{ID: 111}) {
		t.Errorf("NormalizeEntityCacheKey failed for user peer: got %v, want 111", got)
	}

	peerChan := &tg.InputPeerChannel{ChannelID: 222}
	if got := NormalizeEntityCacheKey(peerChan); got != (EntityCacheKey{ID: 222}) {
		t.Errorf("NormalizeEntityCacheKey failed for channel peer: got %v, want 222", got)
	}

	peerChat := &tg.InputPeerChat{ChatID: 333}
	if got := NormalizeEntityCacheKey(peerChat); got != (EntityCacheKey{ID: 333}) {
		t.Errorf("NormalizeEntityCacheKey failed for chat peer: got %v, want 333", got)
	}
}

func TestCachePeerAliases(t *testing.T) {
	c := make(map[EntityCacheKey]CacheRecordEntity)
	rec := CacheRecordEntity{Entity: &tg.InputPeerSelf{}}

	// User
	CachePeerAliases(c, &tg.InputPeerUser{UserID: 100}, rec)
	if c[EntityCacheKey{ID: 100}].Entity == nil {
		t.Error("expected user alias in cache")
	}

	// Channel
	CachePeerAliases(c, &tg.InputPeerChannel{ChannelID: 200}, rec)
	if c[EntityCacheKey{ID: 200}].Entity == nil || c[EntityCacheKey{ID: -1000000000200}].Entity == nil {
		t.Error("expected channel alias in cache")
	}

	// Chat
	CachePeerAliases(c, &tg.InputPeerChat{ChatID: 300}, rec)
	if c[EntityCacheKey{ID: 300}].Entity == nil || c[EntityCacheKey{ID: -300}].Entity == nil {
		t.Error("expected chat alias in cache")
	}
}

func TestInputPeerUserID(t *testing.T) {
	if got := InputPeerUserID(&tg.InputPeerUser{UserID: 123}); got != 123 {
		t.Errorf("expected 123, got %d", got)
	}
	if got := InputPeerUserID(&tg.InputPeerSelf{}); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestChatParticipantUserID(t *testing.T) {
	if got := ChatParticipantUserID(&tg.ChatParticipant{UserID: 1}); got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
	if got := ChatParticipantUserID(&tg.ChatParticipantAdmin{UserID: 2}); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
	if got := ChatParticipantUserID(&tg.ChatParticipantCreator{UserID: 3}); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestInputUserFromPeer(t *testing.T) {
	res, err := InputUserFromPeer(&tg.InputPeerSelf{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.(*tg.InputUserSelf); !ok {
		t.Error("expected InputUserSelf")
	}

	resUser, err := InputUserFromPeer(&tg.InputPeerUser{UserID: 123, AccessHash: 456})
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := resUser.(*tg.InputUser); !ok || u.UserID != 123 {
		t.Error("expected InputUser with ID 123")
	}

	_, err = InputUserFromPeer(&tg.InputPeerChat{})
	if err == nil {
		t.Error("expected error for non-user peer")
	}
}
