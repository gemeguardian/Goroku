package inline

import (
	"github.com/gotd/td/tg"
	"goroku/goroku/chatref"
)

// testInlineUserBot is a test helper that embeds an anonymous type with the
// minimal InlineUserBot methods so that tests can avoid spelling them all out.
type testInlineUserBot struct {
	TGIDVal int64
}

func (m *testInlineUserBot) TGIDValue() int64 { return m.TGIDVal }
func (m *testInlineUserBot) SendMessage(chat chatref.ChatRef, message string) (any, error) {
	return nil, nil
}
func (m *testInlineUserBot) CreateGorokuFolder(botID int64) error                   { return nil }
func (m *testInlineUserBot) InviteBotToChannel(channelPeer tg.InputPeerClass) error { return nil }
func (m *testInlineUserBot) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error  { return nil }

// testInlineModules is a test helper implementing inline.InlineModules.
type testInlineModules struct {
	modules map[string]any
}

func (m *testInlineModules) GetModules() map[string]any { return m.modules }

// testInlineDB is a test helper implementing inline.Database.
type testInlineDB struct {
	getFunc func(namespace, key string, defaultValue any) (any, error)
	setFunc func(namespace, key string, value any) bool
}

func (d *testInlineDB) Get(namespace, key string, defaultValue any) (any, error) {
	if d.getFunc != nil {
		return d.getFunc(namespace, key, defaultValue)
	}
	return defaultValue, nil
}

func (d *testInlineDB) Set(namespace, key string, value any) bool {
	if d.setFunc != nil {
		return d.setFunc(namespace, key, value)
	}
	return true
}
