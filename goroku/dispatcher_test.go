package goroku

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestTranslateLayout(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ghbdtn", "привет"},
		{"/pfnm", "|зать"},
		{"привет", "ghbdtn"},
	}

	for _, tc := range tests {
		got := translateLayout(tc.input)
		if got != tc.expected {
			t.Errorf("translateLayout(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestWatcherTagsMatch(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	cd := NewCommandDispatcher(modules, client, db)

	// Case 1: OnlyPM
	metaPM := CommandMeta{OnlyPM: true}
	msgPM := &Message{RawText: "hello", IsPrivate: true}
	msgGroup := &Message{RawText: "hello", IsGroup: true}

	if !cd.watcherTagsMatch(msgPM, metaPM) {
		t.Error("Expected watcherTagsMatch=true for OnlyPM with private message")
	}
	if cd.watcherTagsMatch(msgGroup, metaPM) {
		t.Error("Expected watcherTagsMatch=false for OnlyPM with group message")
	}

	// Case 2: StartsWith / EndsWith / Contains
	metaString := CommandMeta{
		StartsWith: "hello",
		Contains:   "world",
		EndsWith:   "!",
	}
	msgMatch := &Message{RawText: "hello world!"}
	msgMismatch := &Message{RawText: "hello world"}

	if !cd.watcherTagsMatch(msgMatch, metaString) {
		t.Error("Expected string rules to match")
	}
	if cd.watcherTagsMatch(msgMismatch, metaString) {
		t.Error("Expected string rules to not match due to EndsWith")
	}

	// Case 3: FromID / ChatID
	metaID := CommandMeta{
		FromID: []int64{100, 200},
		ChatID: []int64{-999},
	}
	msgIDMatch := &Message{SenderID: 200, ChatID: -999}
	msgIDMismatch := &Message{SenderID: 300, ChatID: -999}

	if !cd.watcherTagsMatch(msgIDMatch, metaID) {
		t.Error("Expected FromID/ChatID match")
	}
	if cd.watcherTagsMatch(msgIDMismatch, metaID) {
		t.Error("Expected FromID mismatch")
	}
}

func TestHandleGrep(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	cd := NewCommandDispatcher(modules, client, db)

	// Grep query
	msg := &Message{Text: "logs output | grep error", RawText: "logs output | grep error"}
	msg = cd.handleGrep(msg)
	if msg.GrepQuery != "error" || msg.GrepInvert != false || msg.Text != "logs output " {
		t.Errorf("Grep query extraction failed: %+v", msg)
	}

	// Inverted Grep query
	msgInv := &Message{Text: "logs output | grep -v debug", RawText: "logs output | grep -v debug"}
	msgInv = cd.handleGrep(msgInv)
	if msgInv.GrepQuery != "debug" || msgInv.GrepInvert != true || msgInv.Text != "logs output " {
		t.Errorf("Inverted grep extraction failed: %+v", msgInv)
	}

	// Cut lines
	msgCut := &Message{Text: "logs output | cut 10", RawText: "logs output | cut 10"}
	msgCut = cd.handleGrep(msgCut)
	if msgCut.CutLines != 10 || msgCut.Text != "logs output " {
		t.Errorf("Cut lines extraction failed: %+v", msgCut)
	}

	// Split output
	msgSplit := &Message{Text: "logs output | split", RawText: "logs output | split"}
	msgSplit = cd.handleGrep(msgSplit)
	if !msgSplit.SplitOutput || msgSplit.Text != "logs output " {
		t.Errorf("Split output flag failed: %+v", msgSplit)
	}
}

type dummyModule struct {
	commands    map[string]CommandHandler
	watchers    []WatcherHandler
	permissions map[string]int
	ratelimited map[string]bool
}

func (dm *dummyModule) Name() string                                          { return "Dummy" }
func (dm *dummyModule) Strings() map[string]string                            { return nil }
func (dm *dummyModule) Init(client *CustomTelegramClient, db *Database) error { return nil }
func (dm *dummyModule) ClientReady() error                                    { return nil }
func (dm *dummyModule) OnUnload() error                                       { return nil }
func (dm *dummyModule) OnDlmod() error                                        { return nil }
func (dm *dummyModule) Commands() map[string]CommandHandler                   { return dm.commands }
func (dm *dummyModule) Watchers() []WatcherHandler                            { return dm.watchers }
func (dm *dummyModule) CommandPermissions() map[string]int                    { return dm.permissions }
func (dm *dummyModule) RatelimitedCommands() map[string]bool                  { return dm.ratelimited }

func testDispatcherConfig(clock func() time.Time, commandCapacity, watcherCapacity int) DispatcherConfig {
	return DispatcherConfig{
		RateLimiter: RateLimiterConfig{
			UserLimit:  100,
			ChatLimit:  100,
			UserWindow: time.Minute,
			ChatWindow: time.Minute,
			MaxEntries: 100,
			Clock:      clock,
		},
		CommandCapacity: commandCapacity,
		WatcherCapacity: watcherCapacity,
	}
}

func newExecutionTestDispatcher(t *testing.T, module Module, commandCapacity, watcherCapacity int) (*CommandDispatcher, *Modules) {
	t.Helper()
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL | EVERYONE),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	client.Loader = modules
	if err := modules.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCommandDispatcherWithConfig(modules, client, db, testDispatcherConfig(nil, commandCapacity, watcherCapacity))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })
	return dispatcher, modules
}

func TestHandleCommandSuccess(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL | EVERYONE),
	}
	db.data["goroku.main"] = map[string]any{
		"command_prefix": ".",
	}

	client := &CustomTelegramClient{
		TGID: 42,
	}
	modules := NewModules(client, db)
	called := make(chan struct{}, 1)
	dm := &dummyModule{
		commands: map[string]CommandHandler{
			"ping": func(m *Message) error {
				called <- struct{}{}
				return nil
			},
		},
	}
	if err := modules.RegisterModule(dm); err != nil {
		t.Fatalf("register module: %v", err)
	}

	cd := NewCommandDispatcher(modules, client, db)
	msg := &Message{
		SenderID: 42,
		ChatID:   1,
		Text:     ".ping",
		RawText:  ".ping",
		Out:      true,
	}
	cd.HandleCommand(msg)

	select {
	case <-called:
		// Handler was called
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected command handler to be called")
	}
}

func TestHandleRatelimit(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := &CustomTelegramClient{
		TGID: 42,
	}
	modules := NewModules(client, db)
	cd := NewCommandDispatcher(modules, client, db)

	// Set low limits for quick testing.
	cd.rateLimiter = newTestRateLimiter(t, &fakeRateLimitClock{now: time.Unix(1, 0)}, 10, 10, 2)

	msg := &Message{
		SenderID: 999, // non-owner to trigger rate limit check
		ChatID:   123,
	}

	// First call should succeed
	if !cd.handleRatelimit(msg, "any_cmd") {
		t.Fatal("expected ratelimit check to succeed initially")
	}

	// Flood to exceed limit
	for i := 0; i < 20; i++ {
		cd.handleRatelimit(msg, "any_cmd")
	}

	// Should now be blocked
	if cd.handleRatelimit(msg, "any_cmd") {
		t.Fatal("expected ratelimit check to fail after flooding")
	}
}

func TestDispatcherRateLimitWeightsOwnerBypassAndExpiry(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(10, 0)}
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL | EVERYONE),
	}
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	config := testDispatcherConfig(clock.Now, 1, 1)
	config.RateLimiter.UserLimit = 10
	config.RateLimiter.ChatLimit = 10
	config.RateLimiter.UserWindow = 10 * time.Second
	config.RateLimiter.ChatWindow = 10 * time.Second
	dispatcher, err := NewCommandDispatcherWithConfig(modules, client, db, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })

	owner := &Message{SenderID: 42, ChatID: 1}
	for range 20 {
		if !dispatcher.handleRegistrationRatelimit(owner, &commandRegistration{Ratelimited: true}) {
			t.Fatal("owner was rate limited")
		}
	}
	if got := dispatcher.rateLimiter.EntryCount(); got != 0 {
		t.Fatalf("owner bypass created %d entries", got)
	}

	normal := &Message{SenderID: 100, ChatID: 100}
	for range 5 {
		if !dispatcher.handleRegistrationRatelimit(normal, &commandRegistration{}) {
			t.Fatal("normal weight reached its boundary early")
		}
	}
	if dispatcher.handleRegistrationRatelimit(normal, &commandRegistration{}) {
		t.Fatal("normal weight exceeded user limit")
	}
	limited := &Message{SenderID: 101, ChatID: 101}
	for range 2 {
		if !dispatcher.handleRegistrationRatelimit(limited, &commandRegistration{Ratelimited: true}) {
			t.Fatal("registered rate-limit weight reached its boundary early")
		}
	}
	if dispatcher.handleRegistrationRatelimit(limited, &commandRegistration{Ratelimited: true}) {
		t.Fatal("registered rate-limit weight exceeded user limit")
	}

	clock.Advance(10 * time.Second)
	if !dispatcher.handleRegistrationRatelimit(normal, &commandRegistration{}) {
		t.Fatal("normal command remained limited after expiry")
	}
	if dispatcher.handleRegistrationRatelimit(limited, &commandRegistration{Ratelimited: true}) {
		t.Fatal("ratelimited command lost its sustained legacy retention")
	}
	clock.Advance(15 * time.Second)
	if !dispatcher.handleRegistrationRatelimit(limited, &commandRegistration{Ratelimited: true}) {
		t.Fatal("ratelimited command remained limited after severe expiry")
	}
}

func TestDispatcherOwnerChecksUseStrictIdentity(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42), int64(44)}, "sudo": []any{int64(100)}, "all_users": []any{int64(101)},
		"bounding_mask": float64(ALL), "default": float64(EVERYONE),
		"masks": map[string]any{"anything": float64(EVERYONE)},
	}
	client := NewCustomTelegramClient(42)
	client.GorokuMe = &tg.User{ID: 43}
	modules := NewModules(client, db)
	dispatcher, err := NewCommandDispatcherWithConfig(modules, client, db, testDispatcherConfig(nil, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })
	if err := dispatcher.security.AddRule("user", 101, "command", "anything", 0); err != nil {
		t.Fatal(err)
	}
	reg := &commandRegistration{Name: "anything", ownerKey: "dummy", Meta: CommandMeta{OnlyOwner: true}}

	for _, message := range []*Message{
		{SenderID: 100, Out: true},
		{SenderID: 101},
		{SenderID: 102},
		{SenderID: 103},
	} {
		if dispatcher.handleRegistrationTags(message, reg) {
			t.Fatalf("non-owner identity %+v passed OnlyOwner", message)
		}
		before := dispatcher.rateLimiter.EntryCount()
		dispatcher.handleRegistrationRatelimit(message, &commandRegistration{})
		if after := dispatcher.rateLimiter.EntryCount(); after <= before {
			t.Fatalf("non-owner identity %+v bypassed rate limiting", message)
		}
	}
	for _, id := range []int64{42, 43, 44} {
		message := &Message{SenderID: id}
		if !dispatcher.handleRegistrationTags(message, reg) || !dispatcher.handleRegistrationRatelimit(message, reg) {
			t.Fatalf("account owner %d was rejected", id)
		}
	}
}

func TestDangerousCapabilitiesIgnoreEveryoneMask(t *testing.T) {
	// M4.3: security mask EVERYONE must not grant eval/terminal/dlmod/loadpreset/trustmod.
	db := initializedTestDatabase(t, NewDatabase(77))
	dangerous := []string{"eval", "evalpy", "terminal", "dlmod", "loadmod", "loadpreset", "trustmod", "allowmod", "external"}
	masks := make(map[string]any, len(dangerous))
	for _, name := range dangerous {
		masks[name] = float64(EVERYONE)
	}
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(77)},
		"sudo":          []any{int64(200)},
		"all_users":     []any{int64(201)},
		"bounding_mask": float64(ALL | EVERYONE),
		"default":       float64(EVERYONE),
		"masks":         masks,
	}
	client := NewCustomTelegramClient(77)
	client.GorokuMe = &tg.User{ID: 77}
	modules := NewModules(client, db)
	dispatcher, err := NewCommandDispatcherWithConfig(modules, client, db, testDispatcherConfig(nil, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })

	nonOwners := []*Message{
		{SenderID: 200, Out: true},
		{SenderID: 201},
		{SenderID: 999},
	}
	for _, name := range dangerous {
		reg := &commandRegistration{Name: name, ownerKey: "core", Meta: CommandMeta{OnlyOwner: true}, Permission: EVERYONE}
		for _, msg := range nonOwners {
			if dispatcher.security.checkRegistration(msg, reg) && dispatcher.handleRegistrationTags(msg, reg) {
				t.Fatalf("non-owner %+v ran dangerous %s via EVERYONE mask", msg, name)
			}
			if dispatcher.handleRegistrationTags(msg, reg) {
				t.Fatalf("non-owner %+v passed OnlyOwner for %s", msg, name)
			}
		}
		owner := &Message{SenderID: 77}
		if !dispatcher.handleRegistrationTags(owner, reg) {
			t.Fatalf("owner rejected for %s", name)
		}
	}
}

func TestDispatcherClosedRejectionDoesNotConsumeRateQuota(t *testing.T) {
	module := &dummyModule{
		commands:    map[string]CommandHandler{"work": func(*Message) error { return nil }},
		permissions: map[string]int{"work": EVERYONE},
	}
	dispatcher, _ := newExecutionTestDispatcher(t, module, 1, 1)
	dispatcher.Stop()
	dispatcher.HandleCommand(&Message{SenderID: 100, ChatID: 100, Text: ".work", RawText: ".work", IsPrivate: true})
	if got := dispatcher.rateLimiter.EntryCount(); got != 0 {
		t.Fatalf("closed rejection consumed quota entries = %d", got)
	}
}

func TestDispatcherCommandConcurrencyAndImmediateOverflow(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	module := &dummyModule{commands: map[string]CommandHandler{
		"work": func(*Message) error {
			calls.Add(1)
			started <- struct{}{}
			<-release
			return nil
		},
	}}
	dispatcher, _ := newExecutionTestDispatcher(t, module, 2, 1)
	t.Cleanup(func() { close(release) })
	message := &Message{SenderID: 42, ChatID: 42, Text: ".work", RawText: ".work", Out: true}

	dispatcher.HandleCommand(message)
	dispatcher.HandleCommand(message)
	<-started
	<-started
	dispatcher.HandleCommand(message)
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls after overflow = %d, want 2", got)
	}
	if got := dispatcher.commands.Active(); got != 2 {
		t.Fatalf("active commands = %d, want 2", got)
	}
}

func TestDispatcherWatcherCapacityIsIndependent(t *testing.T) {
	commandStarted := make(chan struct{})
	watcherStarted := make(chan struct{}, 2)
	commandRelease := make(chan struct{})
	watcherRelease := make(chan struct{})
	var watcherCalls atomic.Int32
	module := &dummyModule{
		commands: map[string]CommandHandler{"work": func(*Message) error {
			close(commandStarted)
			<-commandRelease
			return nil
		}},
		watchers: []WatcherHandler{func(*Message) error {
			watcherCalls.Add(1)
			watcherStarted <- struct{}{}
			<-watcherRelease
			return nil
		}},
	}
	dispatcher, _ := newExecutionTestDispatcher(t, module, 1, 2)
	t.Cleanup(func() { close(commandRelease) })
	t.Cleanup(func() { close(watcherRelease) })
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".work", RawText: ".work", Out: true})
	<-commandStarted
	message := &Message{SenderID: 42, ChatID: 42, RawText: "watch"}
	dispatcher.HandleIncoming(message)
	dispatcher.HandleIncoming(message)
	<-watcherStarted
	<-watcherStarted
	dispatcher.HandleIncoming(message)
	if got := watcherCalls.Load(); got != 2 {
		t.Fatalf("watcher calls after overflow = %d, want 2", got)
	}
	if dispatcher.commands.Active() != 1 || dispatcher.watchers.Active() != 2 {
		t.Fatalf("independent active counts: commands=%d watchers=%d", dispatcher.commands.Active(), dispatcher.watchers.Active())
	}
}

func TestDispatcherBurstGoroutinesStayBounded(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	module := &dummyModule{commands: map[string]CommandHandler{"burst": func(*Message) error {
		started <- struct{}{}
		<-release
		return nil
	}}}
	dispatcher, _ := newExecutionTestDispatcher(t, module, 4, 1)
	t.Cleanup(func() { close(release) })
	before := runtime.NumGoroutine()
	message := &Message{SenderID: 42, ChatID: 42, Text: ".burst", RawText: ".burst", Out: true}
	for range 2_000 {
		dispatcher.HandleCommand(message)
	}
	for range 4 {
		<-started
	}
	if got := dispatcher.commands.Active(); got != 4 {
		t.Fatalf("active commands after burst = %d, want 4", got)
	}
	if got := runtime.NumGoroutine(); got > before+8 {
		t.Fatalf("goroutines grew from %d to %d for bounded burst", before, got)
	}
}

func TestDispatcherCapacityRejectionDoesNotConsumeRateQuota(t *testing.T) {
	started := make(chan int64, 2)
	release := make(chan struct{})
	module := &dummyModule{
		commands: map[string]CommandHandler{"work": func(m *Message) error {
			started <- m.SenderID
			<-release
			return nil
		}},
		permissions: map[string]int{"work": EVERYONE},
	}
	dispatcher, _ := newExecutionTestDispatcher(t, module, 1, 1)
	dispatcher.rateLimiter = newTestRateLimiter(t, &fakeRateLimitClock{now: time.Unix(1, 0)}, 2, 10, 10)
	owner := &Message{SenderID: 42, ChatID: 42, Text: ".work", RawText: ".work", Out: true}
	normal := &Message{SenderID: 100, ChatID: 100, Text: ".work", RawText: ".work", IsPrivate: true}

	dispatcher.HandleCommand(owner)
	<-started
	dispatcher.HandleCommand(normal)
	if got := dispatcher.rateLimiter.EntryCount(); got != 0 {
		t.Fatalf("capacity rejection consumed quota entries = %d", got)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for dispatcher.commands.Active() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	dispatcher.HandleCommand(normal)
	select {
	case id := <-started:
		if id != 100 {
			t.Fatalf("started sender = %d, want 100", id)
		}
	case <-time.After(time.Second):
		t.Fatal("command was artificially flood-blocked after capacity freed")
	}
}

type recordingInvoker struct {
	mu       sync.Mutex
	messages []string
}

func (r *recordingInvoker) Invoke(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if request, ok := input.(*tg.MessagesSendMessageRequest); ok {
		r.messages = append(r.messages, request.Message)
	}
	return errors.New("stop test RPC")
}

func TestDispatcherBusyResponseBypassesPipeline(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	module := &dummyModule{commands: map[string]CommandHandler{"work": func(*Message) error {
		close(started)
		<-release
		return nil
	}}}
	dispatcher, _ := newExecutionTestDispatcher(t, module, 1, 1)
	t.Cleanup(func() { close(release) })
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".work", RawText: ".work", Out: true})
	<-started
	invoker := &recordingInvoker{}
	dispatcher.client.GorokuDB = dispatcher.db
	dispatcher.client.rawAPI = tg.NewClient(invoker)
	payload := &Message{
		SenderID: 42, ChatID: 42, Text: ".work | grep never", RawText: ".work | grep never",
		Client: dispatcher.client, IsPrivate: true, GrepQuery: "never", GrepInvert: true, CutLines: 1, SplitOutput: true,
	}
	dispatcher.HandleCommand(payload)
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	if len(invoker.messages) != 1 || invoker.messages[0] != "⚠️ Busy, try again shortly." {
		t.Fatalf("busy RPC messages = %#v", invoker.messages)
	}
	if dispatcher.commands.Active() != 1 {
		t.Fatalf("busy response changed active commands to %d", dispatcher.commands.Active())
	}
}
