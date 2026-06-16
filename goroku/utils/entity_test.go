package utils

import (
	"reflect"
	"testing"
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
	data map[string]map[string]interface{}
}

func (m *mockDB) Get(owner, key string, defaultValue interface{}) interface{} {
	if mod, ok := m.data[owner]; ok {
		if val, ok := mod[key]; ok {
			return val
		}
	}
	return defaultValue
}

func (m *mockDB) Set(owner, key string, value interface{}) bool {
	if _, ok := m.data[owner]; !ok {
		m.data[owner] = make(map[string]interface{})
	}
	m.data[owner][key] = value
	return true
}

type mockChannelCreator struct {
	FindChannelByTitleCalled   bool
	CreateChannelCalled        bool
	InviteBotToChannelCalled   bool
	ToggleForumCalled          bool
	CreateForumTopicCalled     bool
	SearchForumTopicCalled     bool
	FindChannelByTitleFallback func(string) (interface{}, error)
}

func (m *mockChannelCreator) FindChannelByTitle(title string) (interface{}, error) {
	m.FindChannelByTitleCalled = true
	if m.FindChannelByTitleFallback != nil {
		return m.FindChannelByTitleFallback(title)
	}
	return mockChannel{Id: 987, Title: title}, nil
}

func (m *mockChannelCreator) CreateChannel(title, description string, megagroup, forum bool) (interface{}, error) {
	m.CreateChannelCalled = true
	return mockChannel{Id: 9876, Title: title}, nil
}

func (m *mockChannelCreator) InviteBotToChannel(channelPeer interface{}) error {
	m.InviteBotToChannelCalled = true
	return nil
}

func (m *mockChannelCreator) ToggleForum(channelPeer interface{}, enabled bool) error {
	m.ToggleForumCalled = true
	return nil
}

func (m *mockChannelCreator) CreateForumTopic(channelPeer interface{}, title, description string, iconEmojiID int64) (int64, error) {
	m.CreateForumTopicCalled = true
	return 555, nil
}

func (m *mockChannelCreator) SearchForumTopic(channelPeer interface{}, title string) (int64, error) {
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
	// 1. Stub client (not satisfying ChannelCreator)
	peer, created := AssetChannel(nil, "hikka-test", "desc", false, false, false, false, "", 3600, false, false, "")
	if !created {
		t.Error("Expected created=true for first call with stub client")
	}
	mPeer, ok := peer.(map[string]interface{})
	if !ok || mPeer["Title"] != "goroku-test" {
		t.Errorf("Expected title 'goroku-test' (with replaced prefix), got %v", peer)
	}

	// Test cache hit
	peerCache, createdCache := AssetChannel(nil, "hikka-test", "desc", false, false, false, false, "", 3600, false, false, "")
	if createdCache {
		t.Error("Expected cache hit (created=false)")
	}
	if !reflect.DeepEqual(peer, peerCache) {
		t.Error("Cache hit returned different peer object")
	}

	// 2. Creator client
	creator := &mockChannelCreator{}
	peerCreator, createdCreator := AssetChannel(creator, "another-channel", "desc", false, false, false, true, "", 3600, false, false, "")
	if createdCreator {
		t.Error("Expected FindChannelByTitle to succeed and createdCreator=false")
	}
	if !creator.FindChannelByTitleCalled || !creator.InviteBotToChannelCalled {
		t.Errorf("Creator methods not called: Find=%t Invite=%t", creator.FindChannelByTitleCalled, creator.InviteBotToChannelCalled)
	}
	if pChan, ok := peerCreator.(mockChannel); !ok || pChan.Title != "another-channel" {
		t.Errorf("Expected mockChannel, got %T", peerCreator)
	}
}

func TestAssetForumTopic(t *testing.T) {
	db := &mockDB{data: make(map[string]map[string]interface{})}
	peer := mockChannel{Id: 987, Title: "goroku-userbot"}

	// 1. Stub client
	topicStub, err := AssetForumTopic(nil, db, peer, "TopicTitle", "desc", 0, false)
	if err != nil {
		t.Fatalf("Stub AssetForumTopic failed: %v", err)
	}
	if mTopic, ok := topicStub.(map[string]interface{}); !ok || mTopic["Title"] != "TopicTitle" || mTopic["ID"] != int64(12345) {
		t.Errorf("Unexpected topic stub: %v", topicStub)
	}

	// 2. Creator client
	creator := &mockChannelCreator{}
	
	// Cache miss, search succeeds
	topicSearch, err := AssetForumTopic(creator, db, peer, "SearchTopic", "desc", 111, true)
	if err != nil {
		t.Fatalf("AssetForumTopic failed: %v", err)
	}
	if mTopic, ok := topicSearch.(map[string]interface{}); !ok || mTopic["ID"] != int64(444) {
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

func TestWaitForContentChannel(t *testing.T) {
	db := &mockDB{data: map[string]map[string]interface{}{
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

func TestStubs(t *testing.T) {
	if !SetAvatar(nil, nil, "") {
		t.Error("SetAvatar should return true")
	}
	if GetTarget(nil, 0) != nil {
		t.Error("GetTarget should return nil")
	}
	if GetUser(nil) != nil {
		t.Error("GetUser should return nil")
	}
}
