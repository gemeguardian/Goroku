package goroku

import (
	"fmt"
	"testing"
	"time"
)

// The admin-rights cache stores the answer to "does this user satisfy this
// permission mask". Keying it on chat/user alone let a decision made for one
// command be replayed for another with different requirements.
func TestAdminCacheKeyDistinguishesPermissionMasks(t *testing.T) {
	const chatID, userID = int64(-100123), int64(456)

	pin := adminCacheKey(chatID, userID, GROUP_ADMIN_PIN_MSGS)
	ban := adminCacheKey(chatID, userID, GROUP_ADMIN_BAN_USERS)

	if pin == ban {
		t.Fatalf("commands requiring different rights share cache key %q: a user "+
			"holding only pin rights would be granted ban rights", pin)
	}

	if same := adminCacheKey(chatID, userID, GROUP_ADMIN_PIN_MSGS); same != pin {
		t.Errorf("same chat/user/mask must be stable: %q != %q", same, pin)
	}
	if other := adminCacheKey(chatID, userID+1, GROUP_ADMIN_PIN_MSGS); other == pin {
		t.Errorf("different users share cache key %q", pin)
	}
	if other := adminCacheKey(chatID+1, userID, GROUP_ADMIN_PIN_MSGS); other == pin {
		t.Errorf("different chats share cache key %q", pin)
	}
}

func TestSetAdminCacheEvictsExpiredEntries(t *testing.T) {
	sm := &SecurityManager{adminCache: make(map[string]adminCacheEntry)}

	past := time.Now().Unix() - 1
	for i := 0; i < adminCacheMaxEntries; i++ {
		sm.adminCache[fmt.Sprintf("stale/%d", i)] = adminCacheEntry{result: true, exp: past}
	}

	sm.setAdminCache("fresh", true)

	if len(sm.adminCache) > adminCacheMaxEntries {
		t.Fatalf("cache grew past bound: len=%d max=%d", len(sm.adminCache), adminCacheMaxEntries)
	}
	if len(sm.adminCache) != 1 {
		t.Errorf("expired entries not swept: len=%d, want 1", len(sm.adminCache))
	}
	entry, ok := sm.adminCache["fresh"]
	if !ok {
		t.Fatal("new entry missing after sweep")
	}
	if !entry.result {
		t.Error("new entry stored with wrong result")
	}
	if entry.exp <= time.Now().Unix() {
		t.Error("new entry stored already expired")
	}
}

// A cache full of live entries must still stay bounded.
func TestSetAdminCacheBoundedWhenNothingExpired(t *testing.T) {
	sm := &SecurityManager{adminCache: make(map[string]adminCacheEntry)}

	future := time.Now().Unix() + 3600
	for i := 0; i < adminCacheMaxEntries; i++ {
		sm.adminCache[fmt.Sprintf("live/%d", i)] = adminCacheEntry{result: true, exp: future}
	}

	sm.setAdminCache("overflow", true)

	if len(sm.adminCache) > adminCacheMaxEntries {
		t.Fatalf("cache grew past bound with no expired entries: len=%d max=%d",
			len(sm.adminCache), adminCacheMaxEntries)
	}
	if _, ok := sm.adminCache["overflow"]; !ok {
		t.Error("newest entry dropped instead of being stored")
	}
}
