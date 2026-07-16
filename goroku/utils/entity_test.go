package utils

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockUser struct {
	ID       int64
	Username string
}

type mockChannel struct {
	Id    int64
	Title string
}

type mockDB struct {
	data   map[string]map[string]any
	setErr error
}

func (m *mockDB) Get(owner, key string, defaultValue any) (any, error) {
	if mod, ok := m.data[owner]; ok {
		if val, ok := mod[key]; ok {
			return val, nil
		}
	}
	return defaultValue, nil
}

func (m *mockDB) GetInt64(owner, key string, def int64) int64 {
	if v, _ := m.Get(owner, key, def); v != nil {
		if n, ok := v.(int64); ok {
			return n
		}
	}
	return def
}

func (m *mockDB) GetAnyMap(owner, key string, def map[string]any) map[string]any {
	if v, _ := m.Get(owner, key, def); v != nil {
		if mm, ok := v.(map[string]any); ok {
			return mm
		}
	}
	return def
}

func (m *mockDB) SetAnyMap(owner, key string, value map[string]any) error {
	return m.Set(owner, key, value)
}

func (m *mockDB) Set(owner, key string, value any) error {
	if m.setErr != nil {
		return m.setErr
	}
	if _, ok := m.data[owner]; !ok {
		m.data[owner] = make(map[string]any)
	}
	m.data[owner][key] = value
	return nil
}

type mockChannelCreator struct {
	FindChannelByTitleCalled   bool
	CreateChannelCalled        bool
	InviteBotToChannelCalled   bool
	ToggleForumCalled          bool
	CreateForumTopicCalled     bool
	SearchForumTopicCalled     bool
	FindChannelByTitleFallback func(string) (any, error)
}

func resetChannelsCache(t *testing.T) {
	t.Helper()
	reset := func() {
		channelsCacheMu.Lock()
		channelsCache = make(map[channelCacheKey]CacheEntry)
		channelCalls = make(map[channelCacheKey]*channelCall)
		channelsCacheMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func (m *mockChannelCreator) FindChannelByTitle(title string) (any, error) {
	m.FindChannelByTitleCalled = true
	if m.FindChannelByTitleFallback != nil {
		return m.FindChannelByTitleFallback(title)
	}
	return mockChannel{Id: 987, Title: title}, nil
}

func (m *mockChannelCreator) CreateChannel(title, description string, megagroup, forum bool) (any, error) {
	m.CreateChannelCalled = true
	return mockChannel{Id: 9876, Title: title}, nil
}

func (m *mockChannelCreator) InviteBotToChannel(channelPeer any) error {
	m.InviteBotToChannelCalled = true
	return nil
}

func (m *mockChannelCreator) ToggleForum(channelPeer any, enabled bool) error {
	m.ToggleForumCalled = true
	return nil
}

func (m *mockChannelCreator) CreateForumTopic(channelPeer any, title, description string, iconEmojiID int64) (int64, error) {
	m.CreateForumTopicCalled = true
	return 555, nil
}

func (m *mockChannelCreator) SearchForumTopic(channelPeer any, title string) (int64, error) {
	m.SearchForumTopicCalled = true
	return 444, nil
}

func TestGetLangFlag(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ru", "🇷🇺"},
		{"US", "🇺🇸"},
		{"ua", "🇺🇦"},
		{"de", "🇩🇪"},
		{"jp", "🇯🇵"},
		{"invalid", "invalid"},
		{"a", "a"},
	}
	for _, tc := range tests {
		got := GetLangFlag(tc.input)
		if got != tc.expected {
			t.Errorf("GetLangFlag(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGetEntityURL(t *testing.T) {
	user := mockUser{ID: 12345, Username: "testuser"}
	urlUser := GetEntityURL(user, false)
	if urlUser != "tg://user?id=12345" {
		t.Errorf("Expected user URL, got %q", urlUser)
	}

	urlUserOpen := GetEntityURL(&user, true)
	if urlUserOpen != "tg://openmessage?id=12345" {
		t.Errorf("Expected user openmessage URL, got %q", urlUserOpen)
	}

	channel := mockChannel{Id: 6789, Title: "MyChannel"}
	urlChan := GetEntityURL(channel, false)
	// mockChannel doesn't have "User" in the name, so isUser=false. Username is empty.
	if urlChan != "" {
		t.Errorf("Expected empty URL, got %q", urlChan)
	}
}

func TestRemoveEmoji(t *testing.T) {
	input := "Hello 👋 World 🌍! Testing 🚀."
	expected := "Hello  World ! Testing ."
	got := RemoveEmoji(input)
	if got != expected {
		t.Errorf("RemoveEmoji(%q) = %q; want %q", input, got, expected)
	}
}

func TestEscapeHTML(t *testing.T) {
	input := "Hello & <world>!"
	expected := "Hello &amp; &lt;world&gt;!"
	got := EscapeHTML(input)
	if got != expected {
		t.Errorf("EscapeHTML(%q) = %q; want %q", input, got, expected)
	}
}

func TestEscapeQuotes(t *testing.T) {
	input := `Hello "Bob" & <world>`
	expected := `Hello &quot;Bob&quot; &amp; &lt;world&gt;`
	got := EscapeQuotes(input)
	if got != expected {
		t.Errorf("EscapeQuotes(%q) = %q; want %q", input, got, expected)
	}
}

func TestRemoveHTML(t *testing.T) {
	input := "<b>Hello</b> <a href='https://example.com'>world</a>! <emoji id=1>🚀</emoji>"

	// Keep Emojis
	gotKeep := RemoveHTML(input, false, true)
	expectedKeep := "Hello world! <emoji id=1>🚀</emoji>"
	if gotKeep != expectedKeep {
		t.Errorf("RemoveHTML(keepEmoji=true) = %q; want %q", gotKeep, expectedKeep)
	}

	// Remove Emojis
	gotRemove := RemoveHTML(input, false, false)
	expectedRemove := "Hello world! 🚀"
	if gotRemove != expectedRemove {
		t.Errorf("RemoveHTML(keepEmoji=false) = %q; want %q", gotRemove, expectedRemove)
	}

	// Escape
	gotEscape := RemoveHTML(input, true, false)
	expectedEscape := "Hello world! 🚀"
	if gotEscape != expectedEscape {
		t.Errorf("RemoveHTML(escape=true) = %q; want %q", gotEscape, expectedEscape)
	}
}

func TestCheckURL(t *testing.T) {
	if !CheckURL("https://google.com/search") {
		t.Error("Expected true for valid URL")
	}
	if CheckURL("invalid-url") {
		t.Error("Expected false for invalid URL")
	}
}

func TestGetLink(t *testing.T) {
	user := mockUser{ID: 12345, Username: "testuser"}
	got := GetLink(user)
	if got != "tg://user?id=12345" {
		t.Errorf("GetLink failed: expected tg://user?id=12345, got %q", got)
	}
}

func TestAssetChannel(t *testing.T) {
	resetChannelsCache(t)

	creator := &mockChannelCreator{}
	peerCreator, createdCreator := AssetChannel(creator, "hikka-test", "desc", false, false, false, true, "", 3600, false, false, "")
	if createdCreator {
		t.Error("Expected FindChannelByTitle to succeed and createdCreator=false")
	}
	if !creator.FindChannelByTitleCalled || !creator.InviteBotToChannelCalled {
		t.Errorf("Creator methods not called: Find=%t Invite=%t", creator.FindChannelByTitleCalled, creator.InviteBotToChannelCalled)
	}
	if pChan, ok := peerCreator.(mockChannel); !ok || pChan.Title != "goroku-test" {
		t.Errorf("Expected mockChannel, got %T", peerCreator)
	}

	peerCache, createdCache := AssetChannel(creator, "hikka-test", "desc", false, false, false, true, "", 3600, false, false, "")
	if createdCache {
		t.Error("Expected cache hit (created=false)")
	}
	if !reflect.DeepEqual(peerCreator, peerCache) {
		t.Error("Cache hit returned different peer object")
	}
}

func TestAssetChannelRejectsIncompatibleClient(t *testing.T) {
	resetChannelsCache(t)

	result, created := AssetChannel(nil, "test", "desc", false, false, false, false, "", 3600, false, false, "")
	if created {
		t.Error("incompatible client reported a created channel")
	}
	err, ok := result.(error)
	if !ok || !errors.Is(err, ErrIncompatibleChannelClient) {
		t.Fatalf("result = %#v; want ErrIncompatibleChannelClient", result)
	}
}

type concurrentChannelCreator struct {
	findCalls   atomic.Int32
	createCalls atomic.Int32
	createGate  <-chan struct{}
	accountID   int64
	peerID      int64
	panicCreate bool
}

func (m *concurrentChannelCreator) TGIDValue() int64 { return m.accountID }

func (m *concurrentChannelCreator) FindChannelByTitle(string) (any, error) {
	m.findCalls.Add(1)
	return nil, errors.New("not found")
}

func (m *concurrentChannelCreator) CreateChannel(title, description string, megagroup, forum bool) (any, error) {
	m.createCalls.Add(1)
	if m.createGate != nil {
		<-m.createGate
	}
	if m.panicCreate {
		panic("create panic")
	}
	peerID := m.peerID
	if peerID == 0 {
		peerID = 9876
	}
	return mockChannel{Id: peerID, Title: title}, nil
}

func (m *concurrentChannelCreator) InviteBotToChannel(any) error { return nil }
func (m *concurrentChannelCreator) ToggleForum(any, bool) error  { return nil }
func (m *concurrentChannelCreator) CreateForumTopic(any, string, string, int64) (int64, error) {
	return 0, nil
}
func (m *concurrentChannelCreator) SearchForumTopic(any, string) (int64, error) {
	return 0, nil
}

func TestAssetChannelConcurrentSameTitle(t *testing.T) {
	resetChannelsCache(t)

	gate := make(chan struct{})
	creator := &concurrentChannelCreator{createGate: gate}
	const workers = 100
	start := make(chan struct{})
	results := make(chan struct {
		peer    any
		created bool
	}, workers)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			peer, created := AssetChannel(creator, "shared-title", "desc", false, false, false, false, "", 3600, false, false, "")
			results <- struct {
				peer    any
				created bool
			}{peer, created}
		}()
	}
	close(start)

	deadline := time.Now().Add(time.Second)
	for creator.createCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()
	close(results)

	createdCount := 0
	for result := range results {
		if result.peer != (mockChannel{Id: 9876, Title: "shared-title"}) {
			t.Errorf("unexpected peer: %#v", result.peer)
		}
		if result.created {
			createdCount++
		}
	}
	if got := creator.findCalls.Load(); got != 1 {
		t.Errorf("FindChannelByTitle called %d times; want 1", got)
	}
	if got := creator.createCalls.Load(); got != 1 {
		t.Errorf("CreateChannel called %d times; want 1", got)
	}
	if createdCount != 1 {
		t.Errorf("created=true returned %d times; want 1", createdCount)
	}
}

func TestAssetChannelDoesNotLockDuringNetworkWork(t *testing.T) {
	resetChannelsCache(t)

	gate := make(chan struct{})
	blocked := &concurrentChannelCreator{createGate: gate}
	done := make(chan struct{})
	go func() {
		defer close(done)
		AssetChannel(blocked, "blocked-title", "", false, false, false, false, "", 3600, false, false, "")
	}()

	deadline := time.Now().Add(time.Second)
	for blocked.createCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	fast := &concurrentChannelCreator{}
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		AssetChannel(fast, "fast-title", "", false, false, false, false, "", 3600, false, false, "")
	}()

	select {
	case <-fastDone:
	case <-time.After(time.Second):
		t.Fatal("different title was blocked by network work")
	}
	close(gate)
	<-done
}

func TestAssetChannelIsolatesAccountsWithSameTitle(t *testing.T) {
	resetChannelsCache(t)

	gate := make(chan struct{})
	first := &concurrentChannelCreator{createGate: gate, accountID: 1, peerID: 101}
	second := &concurrentChannelCreator{createGate: gate, accountID: 2, peerID: 202}
	type result struct {
		peer    any
		created bool
	}
	results := make(chan result, 2)
	for _, creator := range []*concurrentChannelCreator{first, second} {
		go func() {
			peer, created := AssetChannel(creator, "same-title", "", false, false, false, false, "", 0, false, false, "")
			results <- result{peer: peer, created: created}
		}()
	}

	deadline := time.Now().Add(time.Second)
	for (first.createCalls.Load() == 0 || second.createCalls.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if first.createCalls.Load() != 1 || second.createCalls.Load() != 1 {
		t.Fatal("same-title calls from different accounts were single-flighted together")
	}
	close(gate)
	<-results
	<-results

	firstPeer, _ := AssetChannel(first, "same-title", "", false, false, false, false, "", 0, false, false, "")
	secondPeer, _ := AssetChannel(second, "same-title", "", false, false, false, false, "", 0, false, false, "")
	if firstPeer != (mockChannel{Id: 101, Title: "same-title"}) {
		t.Errorf("first account received %#v", firstPeer)
	}
	if secondPeer != (mockChannel{Id: 202, Title: "same-title"}) {
		t.Errorf("second account received %#v", secondPeer)
	}
}

func TestAssetChannelUsesStableAccountIdentity(t *testing.T) {
	resetChannelsCache(t)

	first := &concurrentChannelCreator{accountID: 77, peerID: 707}
	peer, _ := AssetChannel(first, "account-title", "", false, false, false, false, "", 0, false, false, "")
	second := &concurrentChannelCreator{accountID: 77, peerID: 999}
	cached, created := AssetChannel(second, "account-title", "", false, false, false, false, "", 0, false, false, "")

	if cached != peer || created {
		t.Errorf("same account cache result = (%#v, %t); want (%#v, false)", cached, created, peer)
	}
	if second.findCalls.Load() != 0 || second.createCalls.Load() != 0 {
		t.Error("second client instance did not use the stable account cache key")
	}
}

func TestAssetChannelLeaderPanicUnblocksWaiter(t *testing.T) {
	resetChannelsCache(t)

	gate := make(chan struct{})
	creator := &concurrentChannelCreator{createGate: gate, panicCreate: true}
	leaderPanicked := make(chan any, 1)
	go func() {
		defer func() { leaderPanicked <- recover() }()
		AssetChannel(creator, "panic-title", "", false, false, false, false, "", 0, false, false, "")
	}()

	deadline := time.Now().Add(time.Second)
	for creator.createCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	waiterDone := make(chan any, 1)
	go func() {
		peer, _ := AssetChannel(creator, "panic-title", "", false, false, false, false, "", 0, false, false, "")
		waiterDone <- peer
	}()

	key := channelCacheKey{client: creator, title: "panic-title"}
	deadline = time.Now().Add(time.Second)
	for {
		channelsCacheMu.Lock()
		waiters := channelCalls[key].waiters
		channelsCacheMu.Unlock()
		if waiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter did not join the in-flight call")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)

	select {
	case recovered := <-leaderPanicked:
		if recovered != "create panic" {
			t.Errorf("leader recovered %#v; want create panic", recovered)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not preserve panic behavior")
	}
	select {
	case peer := <-waiterDone:
		if peer != nil {
			t.Errorf("waiter peer = %#v; want nil", peer)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter remained blocked after leader panic")
	}
}

func TestAssetChannelRefreshesExpiredEntryAndCleansStaleEntries(t *testing.T) {
	resetChannelsCache(t)

	creator := &mockChannelCreator{}
	currentKey := channelCacheKey{client: creator, title: "current"}
	unrelatedKey := channelCacheKey{client: creator, title: "unrelated"}
	channelsCacheMu.Lock()
	channelsCache[currentKey] = CacheEntry{Peer: "old current", Exp: time.Now().Unix() - 1}
	channelsCache[unrelatedKey] = CacheEntry{Peer: "old unrelated", Exp: time.Now().Unix() - 1}
	channelsCacheMu.Unlock()

	peer, created := AssetChannel(creator, "current", "", false, false, false, false, "", 3600, false, false, "")
	if created {
		t.Error("existing channel reported as newly created")
	}
	if peer != (mockChannel{Id: 987, Title: "current"}) {
		t.Errorf("peer = %#v; want refreshed channel", peer)
	}

	channelsCacheMu.Lock()
	current := channelsCache[currentKey]
	_, unrelatedExists := channelsCache[unrelatedKey]
	channelsCacheMu.Unlock()
	if current.Peer != peer || current.Exp <= time.Now().Unix() {
		t.Errorf("current cache entry was not refreshed: %#v", current)
	}
	if unrelatedExists {
		t.Error("unrelated expired cache entry was not removed")
	}
}

func TestAssetChannelTimedEvictionReleasesClientKey(t *testing.T) {
	resetChannelsCache(t)

	oldTTL := channelCacheTTL
	channelCacheTTL = 10 * time.Millisecond
	t.Cleanup(func() { channelCacheTTL = oldTTL })
	creator := &mockChannelCreator{}
	AssetChannel(creator, "evicted", "", false, false, false, false, "", 0, false, false, "")
	key := channelCacheKey{client: creator, title: "evicted"}

	deadline := time.Now().Add(time.Second)
	for {
		channelsCacheMu.Lock()
		_, exists := channelsCache[key]
		channelsCacheMu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache did not evict the client identity after its TTL")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAssetForumTopic(t *testing.T) {
	db := &mockDB{data: make(map[string]map[string]any)}
	peer := mockChannel{Id: 987, Title: "goroku-userbot"}

	// 1. Stub client
	topicStub, err := AssetForumTopic(nil, db, peer, "TopicTitle", "desc", 0, false)
	if err != nil {
		t.Fatalf("Stub AssetForumTopic failed: %v", err)
	}
	if mTopic, ok := topicStub.(map[string]any); !ok || mTopic["Title"] != "TopicTitle" || mTopic["ID"] != int64(12345) {
		t.Errorf("Unexpected topic stub: %v", topicStub)
	}

	// 2. Creator client
	creator := &mockChannelCreator{}

	// Cache miss, search succeeds
	topicSearch, err := AssetForumTopic(creator, db, peer, "SearchTopic", "desc", 111, true)
	if err != nil {
		t.Fatalf("AssetForumTopic failed: %v", err)
	}
	if mTopic, ok := topicSearch.(map[string]any); !ok || mTopic["ID"] != int64(444) {
		t.Errorf("Expected topic ID 444 from search, got %v", topicSearch)
	}
	if !creator.SearchForumTopicCalled || !creator.ToggleForumCalled || !creator.InviteBotToChannelCalled {
		t.Error("Search/Toggle/Invite was not called")
	}

	// Verify cached
	cachedVal := GetTopicID(db, "SearchTopic")
	if cachedVal != int64(444) {
		t.Errorf("Expected SearchTopic to be cached in DB as 444, got %v", cachedVal)
	}
}

func TestAssetForumTopicPropagatesPersistenceError(t *testing.T) {
	injected := errors.New("injected persistence failure")
	db := &mockDB{data: make(map[string]map[string]any), setErr: injected}
	creator := &mockChannelCreator{}
	peer := mockChannel{Id: 1, Title: "Forum"}

	if _, err := AssetForumTopic(creator, db, peer, "Topic", "", 0, false); !errors.Is(err, injected) {
		t.Fatalf("AssetForumTopic error = %v", err)
	}
}

func TestWaitForContentChannel(t *testing.T) {
	db := &mockDB{data: map[string]map[string]any{
		"goroku.forums": {
			"channel_id": int64(112233),
		},
	}}

	// Since it exists, it should return instantly without looping
	got := WaitForContentChannel(db, 0.001)
	if got != 112233 {
		t.Errorf("Expected 112233, got %d", got)
	}
}

func TestGetChatID(t *testing.T) {
	// Nil safety
	if GetChatID(nil) != 0 {
		t.Error("Expected 0 for nil")
	}

	// Struct with ChatID
	type dummyMsg struct {
		ChatID int64
	}
	msg := dummyMsg{ChatID: 888999}
	if GetChatID(msg) != 888999 {
		t.Errorf("Expected 888999, got %d", GetChatID(msg))
	}
}
