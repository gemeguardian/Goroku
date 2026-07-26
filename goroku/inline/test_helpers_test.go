package inline

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"
	"goroku/goroku/chatref"
)

// testInlineUserBot is a test helper that embeds an anonymous type with the
// minimal InlineUserBot methods so that tests can avoid spelling them all out.
type testInlineUserBot struct {
	TGIDVal      int64
	sendCalls    int
	folderCalls  int
	inviteCalls  int
	promoteCalls int
	webViewCalls int
}

func (m *testInlineUserBot) TGIDValue() int64 { return m.TGIDVal }
func (m *testInlineUserBot) SendMessage(chat chatref.ChatRef, message string) (chatref.SentMessage, error) {
	m.sendCalls++
	return nil, nil
}
func (m *testInlineUserBot) CreateGorokuFolder(botID int64) error {
	m.folderCalls++
	return nil
}
func (m *testInlineUserBot) InviteBotToChannel(channelPeer tg.InputPeerClass) error {
	m.inviteCalls++
	return nil
}
func (m *testInlineUserBot) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error {
	m.promoteCalls++
	return nil
}
func (m *testInlineUserBot) GetSecurityManager() SecurityChecker { return nil }
func (m *testInlineUserBot) RequestWebView(peerUsername, platform, webURL string) (string, error) {
	m.webViewCalls++
	return "", errors.New("unexpected web view request")
}

// testInlineModules is a test helper implementing inline.InlineModules.
type testInlineModules struct {
	modules map[string]any
}

func (m *testInlineModules) ModuleNames() []string {
	names := make([]string, 0, len(m.modules))
	for name := range m.modules {
		names = append(names, name)
	}
	return names
}
func (m *testInlineModules) WithModule(name string, fn func(any)) bool {
	module, ok := m.modules[name]
	if ok {
		fn(module)
	}
	return ok
}

// testInlineDB is a test helper implementing inline.Database.
type testInlineDB struct {
	getFunc func(namespace, key string, defaultValue any) (any, error)
	setFunc func(namespace, key string, value any) error
}

func (d *testInlineDB) Get(namespace, key string, defaultValue any) (any, error) {
	if d.getFunc != nil {
		return d.getFunc(namespace, key, defaultValue)
	}
	return defaultValue, nil
}

func (d *testInlineDB) Set(namespace, key string, value any) error {
	if d.setFunc != nil {
		return d.setFunc(namespace, key, value)
	}
	return nil
}

func TestBootstrapPropagatesDatabaseSetError(t *testing.T) {
	injected := errors.New("injected database failure")
	db := &testInlineDB{setFunc: func(string, string, any) error { return injected }}
	im := NewInlineManager(&testInlineUserBot{}, db, &testInlineModules{})
	im.BotUsername = "helper_bot"
	im.BotID = 42

	if err := im.bootstrapUserBotSide(false); !errors.Is(err, injected) {
		t.Fatalf("bootstrap error = %v", err)
	}
}

func TestBootstrapReadFailuresHaveNoSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failKey  string
		injected error
	}{
		{name: "uninitialized database", failKey: "bootstrapped_group", injected: errors.New("database is not initialized")},
		{name: "closed database", failKey: "folder_created", injected: errors.New("database is closed")},
		{name: "closed database channel", failKey: "channel_id", injected: errors.New("database is closed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sets := 0
			db := &testInlineDB{
				getFunc: func(namespace, key string, defaultValue any) (any, error) {
					if key == tc.failKey {
						return nil, tc.injected
					}
					return defaultValue, nil
				},
				setFunc: func(string, string, any) error {
					sets++
					return nil
				},
			}
			client := &testInlineUserBot{}
			im := NewInlineManager(client, db, &testInlineModules{})
			im.BotUsername = "helper_bot"
			im.BotID = 42

			if err := im.bootstrapUserBotSide(false); !errors.Is(err, tc.injected) {
				t.Fatalf("bootstrap error = %v", err)
			}
			if client.sendCalls+client.folderCalls+client.inviteCalls+client.promoteCalls != 0 || sets != 0 {
				t.Fatalf("side effects after read failure: client=%+v sets=%d", client, sets)
			}
		})
	}
}

func TestLastBotIDReadFailureDoesNotResetBootstrap(t *testing.T) {
	injected := errors.New("database is closed")
	sets := 0
	db := &testInlineDB{
		getFunc: func(string, string, any) (any, error) { return nil, injected },
		setFunc: func(string, string, any) error {
			sets++
			return nil
		},
	}
	im := NewInlineManager(&testInlineUserBot{}, db, &testInlineModules{})

	if err := im.resetBootstrapForBot(42); !errors.Is(err, injected) {
		t.Fatalf("reset error = %v", err)
	}
	if sets != 0 {
		t.Fatalf("reset wrote %d values after read failure", sets)
	}
}

func TestTokenReadFailuresDoNotContactBotFather(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failKey   string
		tokenCall func(*InlineManager) (bool, error)
	}{
		{name: "uninitialized token database", failKey: "bot_token", tokenCall: func(im *InlineManager) (bool, error) {
			return im.AssertToken(true, false)
		}},
		{name: "closed custom bot database", failKey: "custom_bot", tokenCall: func(im *InlineManager) (bool, error) {
			return im.CreateBot()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			injected := errors.New(tc.name)
			db := &testInlineDB{getFunc: func(namespace, key string, defaultValue any) (any, error) {
				if key == tc.failKey {
					return nil, injected
				}
				return defaultValue, nil
			}}
			client := &testInlineUserBot{}
			im := NewInlineManager(client, db, &testInlineModules{})

			if _, err := tc.tokenCall(im); !errors.Is(err, injected) {
				t.Fatalf("token method error = %v", err)
			}
			if client.webViewCalls != 0 {
				t.Fatalf("BotFather received %d WebView requests after read failure", client.webViewCalls)
			}
		})
	}
}

func TestRegistrationStopsOnTokenReadFailure(t *testing.T) {
	injected := errors.New("database is not initialized")
	sets := 0
	db := &testInlineDB{
		getFunc: func(string, string, any) (any, error) { return nil, injected },
		setFunc: func(string, string, any) error {
			sets++
			return nil
		},
	}
	im := NewInlineManager(&testInlineUserBot{}, db, &testInlineModules{})

	if err := im.RegisterManager(false, false); !errors.Is(err, injected) {
		t.Fatalf("registration error = %v", err)
	}
	if sets != 0 || im.GetBotAPI() != nil || im.IsComplete() {
		t.Fatalf("registration had side effects: sets=%d bot=%v complete=%v", sets, im.GetBotAPI(), im.IsComplete())
	}
}

func TestMissingInlineKeysUseDefaults(t *testing.T) {
	db := &testInlineDB{}
	im := NewInlineManager(&testInlineUserBot{}, db, &testInlineModules{})

	token, err := im.getToken()
	if err != nil || token != "" {
		t.Fatalf("getToken() = %q, %v", token, err)
	}
	authorized, err := im.isUserAuthorizedForInline(99)
	if err != nil || authorized {
		t.Fatalf("authorization = %v, %v", authorized, err)
	}
}

func TestInlineAuthorizationReadFailureIsDiagnosticAndFailClosed(t *testing.T) {
	injected := errors.New("database is closed")
	db := &testInlineDB{getFunc: func(string, string, any) (any, error) { return nil, injected }}
	im := NewInlineManager(&testInlineUserBot{TGIDVal: 1}, db, &testInlineModules{})

	authorized, err := im.isUserAuthorizedForInline(99)
	if authorized || !errors.Is(err, injected) {
		t.Fatalf("authorization = %v, %v", authorized, err)
	}
}
