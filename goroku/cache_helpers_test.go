package goroku

import (
	"context"
	"testing"
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type mockInvoker struct {
	onInvoke func(ctx context.Context, input bin.Encoder, output bin.Decoder) error
}

func (m *mockInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if m.onInvoke != nil {
		return m.onInvoke(ctx, input, output)
	}
	return nil
}

func TestEntitiesToHTML(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []tg.MessageEntityClass
		expected string
	}{
		{
			name: "Bold and Italic",
			text: "hello world",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityBold{Offset: 0, Length: 5},
				&tg.MessageEntityItalic{Offset: 6, Length: 5},
			},
			expected: "<b>hello</b> <i>world</i>",
		},
		{
			name: "Underline and Strike",
			text: "test message",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityUnderline{Offset: 0, Length: 4},
				&tg.MessageEntityStrike{Offset: 5, Length: 7},
			},
			expected: "<u>test</u> <s>message</s>",
		},
		{
			name: "Code and Pre",
			text: "code block",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityCode{Offset: 0, Length: 4},
				&tg.MessageEntityPre{Offset: 5, Length: 5},
			},
			expected: "<code>code</code> <pre>block</pre>",
		},
		{
			name: "Spoiler and Blockquote",
			text: "secret info quote",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntitySpoiler{Offset: 0, Length: 6},
				&tg.MessageEntityBlockquote{Offset: 7, Length: 4, Collapsed: true},
			},
			expected: "<tg-spoiler>secret</tg-spoiler> <blockquote expandable>info</blockquote> quote",
		},
		{
			name: "TextURL and CustomEmoji",
			text: "google emoji",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityTextURL{Offset: 0, Length: 6, URL: "https://google.com"},
				&tg.MessageEntityCustomEmoji{Offset: 7, Length: 5, DocumentID: 12345},
			},
			expected: "<a href=\"https://google.com\">google</a> <tg-emoji emoji-id=\"12345\">emoji</tg-emoji>",
		},
		{
			name: "MentionName",
			text: "user mention",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityMentionName{Offset: 0, Length: 4, UserID: 999},
			},
			expected: "<a href=\"tg://user?id=999\">user</a> mention",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EntitiesToHTML(tc.text, tc.entities)
			if got != tc.expected {
				t.Errorf("EntitiesToHTML(%q) = %q; want %q", tc.text, got, tc.expected)
			}
		})
	}
}

func TestGetEntityCached(t *testing.T) {
	client := &CustomTelegramClient{
		TGID:              42,
		GorokuEntityCache: make(map[cache.EntityCacheKey]cache.CacheRecordEntity),
	}

	client.GorokuEntityCache[cache.NormalizeEntityCacheKey(123)] = cache.CacheRecordEntity{
		Entity: &tg.InputPeerUser{UserID: 123},
		Exp:    time.Now().Unix() + 100,
	}

	res, err := client.GetEntity(int64(123), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	peer, ok := res.(*tg.InputPeerUser)
	if !ok || peer.UserID != 123 {
		t.Fatalf("expected cached user, got %v", res)
	}

	// Force resolve calling resolvePeer
	client.GorokuEntityCache = make(map[cache.EntityCacheKey]cache.CacheRecordEntity)
	_, err = client.GetEntity(int64(123), 100, false)
	if err == nil {
		t.Fatal("expected error since rawAPI is not set")
	}
}

func TestGetFullUserCached(t *testing.T) {
	client := &CustomTelegramClient{
		TGID:                42,
		ctx:                 context.Background(),
		GorokuEntityCache:   make(map[cache.EntityCacheKey]cache.CacheRecordEntity),
		GorokuFullUserCache: make(map[cache.EntityCacheKey]cache.CacheRecordFullUser),
	}

	// Mock cached full user
	client.GorokuFullUserCache[cache.NormalizeEntityCacheKey(123)] = cache.CacheRecordFullUser{
		User: &tg.UsersUserFull{FullUser: tg.UserFull{About: "about user"}},
		Exp:  time.Now().Unix() + 100,
	}

	res, err := client.GetFullUser(int64(123), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	fu, ok := res.(*tg.UsersUserFull)
	if !ok || fu.FullUser.About != "about user" {
		t.Fatalf("expected cached full user, got %v", res)
	}

	// Resolve calling API
	client.GorokuFullUserCache = make(map[cache.EntityCacheKey]cache.CacheRecordFullUser)
	client.GorokuEntityCache[cache.NormalizeEntityCacheKey(123)] = cache.CacheRecordEntity{
		Entity: &tg.InputPeerUser{UserID: 123, AccessHash: 456},
	}

	invoker := &mockInvoker{}
	client.rawAPI = tg.NewClient(invoker)

	invoker.onInvoke = func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		resp := &tg.UsersUserFull{
			FullUser: tg.UserFull{
				About: "retrieved user",
			},
		}
		buf := &bin.Buffer{}
		_ = resp.Encode(buf)
		return output.Decode(buf)
	}

	res2, err := client.GetFullUser(int64(123), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	fu2, ok := res2.(*tg.UsersUserFull)
	if !ok || fu2.FullUser.About != "retrieved user" {
		t.Fatalf("expected retrieved user, got %v", res2)
	}
}

func TestGetFullChannelCached(t *testing.T) {
	client := &CustomTelegramClient{
		TGID:                   42,
		ctx:                    context.Background(),
		GorokuEntityCache:      make(map[cache.EntityCacheKey]cache.CacheRecordEntity),
		GorokuFullChannelCache: make(map[cache.EntityCacheKey]cache.CacheRecordFullChannel),
	}

	// Mock cached channel
	client.GorokuFullChannelCache[cache.NormalizeEntityCacheKey(-100123)] = cache.CacheRecordFullChannel{
		Channel: &tg.MessagesChatFull{FullChat: &tg.ChannelFull{About: "about chan", ChatPhoto: &tg.PhotoEmpty{}}},
		Exp:     time.Now().Unix() + 100,
	}

	res, err := client.GetFullChannel(int64(-100123), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	cf, ok := res.(*tg.MessagesChatFull)
	if !ok || cf.FullChat.(*tg.ChannelFull).About != "about chan" {
		t.Fatalf("expected cached channel, got %v", res)
	}

	// Resolve calling API
	client.GorokuFullChannelCache = make(map[cache.EntityCacheKey]cache.CacheRecordFullChannel)
	client.GorokuEntityCache[cache.NormalizeEntityCacheKey(-100123)] = cache.CacheRecordEntity{
		Entity: &tg.InputPeerChannel{ChannelID: 123, AccessHash: 456},
	}

	invoker := &mockInvoker{}
	client.rawAPI = tg.NewClient(invoker)

	invoker.onInvoke = func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		resp := &tg.MessagesChatFull{
			FullChat: &tg.ChannelFull{About: "retrieved chan", ChatPhoto: &tg.PhotoEmpty{}},
		}
		buf := &bin.Buffer{}
		_ = resp.Encode(buf)
		return output.Decode(buf)
	}

	res2, err := client.GetFullChannel(int64(-100123), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	cf2, ok := res2.(*tg.MessagesChatFull)
	if !ok || cf2.FullChat.(*tg.ChannelFull).About != "retrieved chan" {
		t.Fatalf("expected retrieved chan, got %v", res2)
	}
}

func TestGetPermsCachedDirect(t *testing.T) {
	client := &CustomTelegramClient{
		TGID:              42,
		ctx:               context.Background(),
		GorokuEntityCache: make(map[cache.EntityCacheKey]cache.CacheRecordEntity),
		GorokuPermsCache:  make(map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms),
	}

	// Mock cached perms
	client.GorokuPermsCache[cache.NormalizeEntityCacheKey(-100123)] = map[cache.EntityCacheKey]cache.CacheRecordPerms{
		cache.NormalizeEntityCacheKey(200): {
			Perms: "admin_rights",
			Exp:   time.Now().Unix() + 100,
		},
	}

	res, err := client.GetPermsCached(int64(-100123), int64(200), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := res.(string); !ok || p != "admin_rights" {
		t.Fatalf("expected cached admin_rights, got %v", res)
	}

	// Mock fetch permissions for InputPeerUser (should return mapped "is_private": true)
	client.rawAPI = tg.NewClient(&mockInvoker{})
	client.GorokuPermsCache = make(map[cache.EntityCacheKey]map[cache.EntityCacheKey]cache.CacheRecordPerms)
	client.GorokuEntityCache[cache.NormalizeEntityCacheKey(123)] = cache.CacheRecordEntity{
		Entity: &tg.InputPeerUser{UserID: 123},
	}
	client.GorokuEntityCache[cache.NormalizeEntityCacheKey(200)] = cache.CacheRecordEntity{
		Entity: &tg.InputPeerUser{UserID: 200},
	}

	res2, err := client.GetPermsCached(int64(123), int64(200), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res2.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", res2)
	}
	isPrivate, exists := m["is_private"].(bool)
	if !exists || !isPrivate {
		t.Fatalf("expected is_private=true perms, got %v", res2)
	}
}
