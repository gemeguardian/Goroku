package goroku

import (
	"testing"
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

// The entity cache used to have no delete anywhere in the package: a bot in
// active groups added entries per participant until a restart freed them.
func TestEntityCacheStaysWithinItsLimit(t *testing.T) {
	client := NewCustomTelegramClient(42)
	now := time.Now().Unix()

	for i := 0; i < entityCacheMaxEntries+500; i++ {
		client.cacheMu.Lock()
		client.sweepEntityCacheLocked(now)
		client.GorokuEntityCache[cache.EntityCacheKey{ID: int64(i)}] = cache.CacheRecordEntity{
			Entity: &tg.InputPeerUser{UserID: int64(i)},
			Exp:    now + 3600,
			TS:     now + int64(i),
		}
		client.cacheMu.Unlock()
	}

	if got := len(client.GorokuEntityCache); got > entityCacheMaxEntries {
		t.Fatalf("entity cache holds %d records, limit is %d", got, entityCacheMaxEntries)
	}
}

func TestEntityCacheSweepDropsExpiredAndKeepsFresh(t *testing.T) {
	client := NewCustomTelegramClient(42)
	now := time.Now().Unix()

	client.cacheMu.Lock()
	// Fill to the limit so the sweep triggers, with every second record stale.
	for i := 0; i < entityCacheMaxEntries; i++ {
		exp := now + 3600
		if i%2 == 0 {
			exp = now - 3600
		}
		client.GorokuEntityCache[cache.EntityCacheKey{ID: int64(i)}] = cache.CacheRecordEntity{
			Entity: &tg.InputPeerUser{UserID: int64(i)},
			Exp:    exp,
			TS:     now,
		}
	}
	fresh := cache.EntityCacheKey{ID: -1}
	client.GorokuEntityCache[fresh] = cache.CacheRecordEntity{
		Entity: &tg.InputPeerUser{UserID: 1},
		Exp:    now + 86400,
		TS:     now + 1000,
	}
	client.sweepEntityCacheLocked(now)
	client.cacheMu.Unlock()

	if _, ok := client.GorokuEntityCache[cache.EntityCacheKey{ID: 0}]; ok {
		t.Fatal("expired record survived the sweep")
	}
	if _, ok := client.GorokuEntityCache[fresh]; !ok {
		t.Fatal("fresh record was dropped by the sweep")
	}
}

// Perms is a two-level map: emptying the inner map must drop the outer entry,
// otherwise the outer level grows with one empty map per chat ever seen.
func TestPermsCacheDropsEmptiedOuterEntries(t *testing.T) {
	client := NewCustomTelegramClient(42)
	now := time.Now().Unix()

	chat := cache.EntityCacheKey{ID: -100500}
	client.cacheMu.Lock()
	client.GorokuPermsCache[chat] = map[cache.EntityCacheKey]cache.CacheRecordPerms{
		{ID: 1}: {Perms: "admin", Exp: now - 60, TS: now - 120},
		{ID: 2}: {Perms: "admin", Exp: now - 60, TS: now - 120},
	}
	client.sweepPermsCacheLocked(now)
	client.cacheMu.Unlock()

	if _, ok := client.GorokuPermsCache[chat]; ok {
		t.Fatal("outer perms entry survived after its inner map emptied")
	}
}

func TestPermsCacheStaysWithinItsLimit(t *testing.T) {
	client := NewCustomTelegramClient(42)
	now := time.Now().Unix()

	client.cacheMu.Lock()
	for i := 0; i < permsCacheMaxEntities+200; i++ {
		client.sweepPermsCacheLocked(now)
		key := cache.EntityCacheKey{ID: int64(-i - 1)}
		client.GorokuPermsCache[key] = map[cache.EntityCacheKey]cache.CacheRecordPerms{
			{ID: 1}: {Perms: "member", Exp: now + 3600, TS: now + int64(i)},
		}
	}
	size := len(client.GorokuPermsCache)
	client.cacheMu.Unlock()

	if size > permsCacheMaxEntities {
		t.Fatalf("perms cache holds %d chats, limit is %d", size, permsCacheMaxEntities)
	}
}

func TestFullUserAndChannelCachesStayWithinLimits(t *testing.T) {
	client := NewCustomTelegramClient(42)
	now := time.Now().Unix()

	client.cacheMu.Lock()
	for i := 0; i < fullUserCacheMaxEntries+200; i++ {
		client.sweepFullUserCacheLocked(now)
		client.GorokuFullUserCache[cache.EntityCacheKey{ID: int64(i)}] = cache.CacheRecordFullUser{
			User: &tg.UsersUserFull{},
			Exp:  now + 3600,
			TS:   now + int64(i),
		}
	}
	for i := 0; i < fullChannelCacheMaxEntries+200; i++ {
		client.sweepFullChannelCacheLocked(now)
		client.GorokuFullChannelCache[cache.EntityCacheKey{ID: int64(-i - 1)}] = cache.CacheRecordFullChannel{
			Channel: &tg.MessagesChatFull{},
			Exp:     now + 3600,
			TS:      now + int64(i),
		}
	}
	users, channels := len(client.GorokuFullUserCache), len(client.GorokuFullChannelCache)
	client.cacheMu.Unlock()

	if users > fullUserCacheMaxEntries {
		t.Fatalf("full user cache holds %d records, limit is %d", users, fullUserCacheMaxEntries)
	}
	if channels > fullChannelCacheMaxEntries {
		t.Fatalf("full channel cache holds %d records, limit is %d", channels, fullChannelCacheMaxEntries)
	}
}

// cacheEntities runs on every update; it is the main growth path.
func TestCacheEntitiesStaysBounded(t *testing.T) {
	client := NewCustomTelegramClient(42)

	for batch := 0; batch < 30; batch++ {
		users := make([]tg.User, 0, 1000)
		for i := 0; i < 1000; i++ {
			id := int64(batch*1000 + i)
			users = append(users, tg.User{ID: id, AccessHash: id})
		}
		entities := tg.Entities{Users: map[int64]*tg.User{}}
		for i := range users {
			entities.Users[users[i].ID] = &users[i]
		}
		client.cacheEntities(entities)
	}

	if got := len(client.GorokuEntityCache); got > entityCacheMaxEntries {
		t.Fatalf("entity cache grew to %d records through cacheEntities, limit is %d", got, entityCacheMaxEntries)
	}
}
