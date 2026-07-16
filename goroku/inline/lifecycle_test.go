package inline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goroku/goroku/chatref"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

type lifecycleBot struct {
	api       *tgbotapi.BotAPI
	updates   chan tgbotapi.Update
	stopped   chan struct{}
	stopOnce  sync.Once
	stopCalls atomic.Int32
}

func newLifecycleBot() *lifecycleBot {
	return &lifecycleBot{
		api:     &tgbotapi.BotAPI{},
		updates: make(chan tgbotapi.Update, 8),
		stopped: make(chan struct{}),
	}
}

func (b *lifecycleBot) API() *tgbotapi.BotAPI { return b.api }
func (b *lifecycleBot) Self() tgbotapi.User {
	return tgbotapi.User{ID: 42, UserName: "lifecycle_bot"}
}
func (b *lifecycleBot) Updates(tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	return b.updates
}
func (b *lifecycleBot) StopPolling() {
	b.stopCalls.Add(1)
	b.stopOnce.Do(func() { close(b.stopped) })
}

type lifecycleDB struct {
	mu   sync.Mutex
	data map[string]any
}

func newLifecycleDB() *lifecycleDB {
	return &lifecycleDB{data: map[string]any{
		"bot_token":          "test-token",
		"last_bot_id":        int64(42),
		"folder_created":     true,
		"bootstrapped_group": nil,
		"channel_id":         nil,
	}}
}

func (d *lifecycleDB) Get(_ string, key string, defaultValue any) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	value, ok := d.data[key]
	if !ok {
		return defaultValue, nil
	}
	return value, nil
}

func (d *lifecycleDB) Set(_ string, key string, value any) error {
	d.mu.Lock()
	d.data[key] = value
	d.mu.Unlock()
	return nil
}

func registeredLifecycleManager(t *testing.T) (*InlineManager, *lifecycleBot) {
	t.Helper()
	bot := newLifecycleBot()
	im := NewInlineManager(&testInlineUserBot{}, newLifecycleDB(), &testInlineModules{})
	im.newBot = func(string) (inlineBot, error) { return bot, nil }
	im.cleanerInterval = time.Millisecond
	if err := im.RegisterManager(false, false); err != nil {
		t.Fatal(err)
	}
	return im, bot
}

func TestRegisterBootstrapRetryDoesNotDeadlock(t *testing.T) {
	db := newLifecycleDB()
	db.data["folder_created"] = false
	client := &testInlineUserBot{}
	im := NewInlineManager(client, db, &testInlineModules{})
	im.newBot = func(string) (inlineBot, error) { return newLifecycleBot(), nil }
	clientError := errors.New("start failed")

	// A failing first bootstrap used to recursively call RegisterManager while
	// registerMu was held. Force that path and require a bounded return.
	badClient := &failingBootstrapClient{testInlineUserBot: *client, err: clientError}
	im.client = badClient
	done := make(chan error, 1)
	go func() { done <- im.RegisterManager(false, false) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("registration unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("registration deadlocked in bootstrap retry")
	}
}

func TestRegisterBootstrapRetryStopsDiscardedBot(t *testing.T) {
	db := newLifecycleDB()
	db.data["folder_created"] = false
	im := NewInlineManager(&failingBootstrapClient{err: errors.New("start failed")}, db, &testInlineModules{})
	var bots []*lifecycleBot
	im.newBot = func(string) (inlineBot, error) {
		bot := newLifecycleBot()
		bots = append(bots, bot)
		return bot, nil
	}
	if err := im.RegisterManager(false, false); err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	if len(bots) != 1 {
		t.Fatalf("created bots = %d, want 1", len(bots))
	}
	for i, bot := range bots {
		if bot.stopCalls.Load() != 1 {
			t.Fatalf("bot %d stop calls = %d, want 1", i, bot.stopCalls.Load())
		}
	}
}

type failingBootstrapClient struct {
	testInlineUserBot
	err error
}

func (c *failingBootstrapClient) SendMessage(chat chatref.ChatRef, message string) (any, error) {
	return nil, c.err
}

func TestCloseConcurrentIsIdempotent(t *testing.T) {
	im, bot := registeredLifecycleManager(t)
	const callers = 16
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { errs <- im.Close(context.Background()) }()
	}
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if bot.stopCalls.Load() != 1 {
		t.Fatalf("StopPolling calls = %d, want 1", bot.stopCalls.Load())
	}
}

func TestCloseDisposesPublishedBotAPIAndIdentity(t *testing.T) {
	im, _ := registeredLifecycleManager(t)
	if im.GetBotAPI() == nil || im.BotUsernameStr() == "" || im.BotIDVal() == 0 {
		t.Fatal("registered bot identity was not published")
	}
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if im.GetBotAPI() != nil || im.BotUsernameStr() != "" || im.BotIDVal() != 0 {
		t.Fatal("closed manager still exposes bot API or identity")
	}
}

func TestPublicCallbackAnswerIsCanceledAndDrainedByClose(t *testing.T) {
	requestStarted := make(chan struct{})
	var requests atomic.Int32
	transport := &mockRoundTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"test"}}`))}, nil
		}
		close(requestStarted)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}}
	bot, err := tgbotapi.NewBotAPIWithOptions("mock_token", tgbotapi.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	im := NewInlineManager(&testInlineUserBot{}, newLifecycleDB(), &testInlineModules{})
	im.mu.Lock()
	im.bot = bot
	im.mu.Unlock()
	answerDone := make(chan error, 1)
	go func() { answerDone <- (CallbackQuery{ID: "public", Manager: im}).Answer("answer", false) }()
	<-requestStarted
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-answerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Answer error = %v, want canceled", err)
	}
	if im.GetBotAPI() != nil {
		t.Fatal("GetBotAPI exposed a bot after close")
	}
}

func TestCloseCompletedResultWinsCanceledCaller(t *testing.T) {
	im, _ := registeredLifecycleManager(t)
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := im.Close(ctx); err != nil {
		t.Fatalf("completed Close with canceled context = %v", err)
	}
}

func TestCloseWaitsForBlockedUpdateAndRejectsNewHandlers(t *testing.T) {
	im, bot := registeredLifecycleManager(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	im.mu.Lock()
	im.customMap["block"] = Button{Handler: func(CallbackQuery) error {
		calls.Add(1)
		close(started)
		<-release
		return nil
	}}
	im.mu.Unlock()
	update := tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		ID: "callback", From: &tgbotapi.User{ID: 0}, Data: "block",
	}}
	bot.updates <- update
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := im.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline", err)
	}
	bot.updates <- update
	close(release)
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls after close = %d, want 1", calls.Load())
	}
}

func TestCloseWaitsForDirectHandleUpdate(t *testing.T) {
	im, _ := registeredLifecycleManager(t)
	started := make(chan struct{})
	release := make(chan struct{})
	im.mu.Lock()
	im.customMap["direct"] = Button{Handler: func(CallbackQuery) error {
		close(started)
		<-release
		return nil
	}}
	im.mu.Unlock()
	im.HandleUpdate(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		ID: "direct", From: &tgbotapi.User{}, Data: "direct",
	}})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := im.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline", err)
	}
	close(release)
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRacingRegistrationCancelsPartialGeneration(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, newLifecycleDB(), &testInlineModules{})
	factoryStarted := make(chan struct{})
	releaseFactory := make(chan struct{})
	bot := newLifecycleBot()
	im.newBot = func(string) (inlineBot, error) {
		close(factoryStarted)
		<-releaseFactory
		return bot, nil
	}
	registerDone := make(chan error, 1)
	go func() { registerDone <- im.RegisterManager(false, false) }()
	<-factoryStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- im.Close(context.Background()) }()
	for {
		im.mu.RLock()
		closed := im.closed
		im.mu.RUnlock()
		if closed {
			break
		}
		runtime.Gosched()
	}
	close(releaseFactory)
	if err := <-registerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("registration error = %v, want canceled", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if im.IsComplete() || im.GetBotAPI() != nil {
		t.Fatal("partial registration state survived close")
	}
}

func TestFailedRegistrationAllowsControlledRetry(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, newLifecycleDB(), &testInlineModules{})
	injected := errors.New("factory failure")
	var calls atomic.Int32
	secondBot := newLifecycleBot()
	im.newBot = func(string) (inlineBot, error) {
		if calls.Add(1) == 1 {
			return nil, injected
		}
		return secondBot, nil
	}
	if err := im.RegisterManager(false, true); !errors.Is(err, injected) {
		t.Fatalf("first registration error = %v", err)
	}
	oldGeneration := im.generation
	if err := im.RegisterManager(false, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldGeneration.done:
	default:
		t.Fatal("old generation still running after retry")
	}
	if !im.IsComplete() {
		t.Fatal("retry did not complete")
	}
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestControlledRestartDrainsOldGenerationFirst(t *testing.T) {
	im, firstBot := registeredLifecycleManager(t)
	firstGeneration := im.generation
	if err := im.stopGeneration(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstGeneration.done:
	default:
		t.Fatal("old generation was not drained")
	}
	select {
	case <-firstBot.stopped:
	default:
		t.Fatal("old polling was not stopped")
	}

	secondBot := newLifecycleBot()
	im.newBot = func(string) (inlineBot, error) {
		select {
		case <-firstGeneration.done:
		default:
			t.Fatal("new bot created before old generation completed")
		}
		return secondBot, nil
	}
	if err := im.RegisterManager(false, false); err != nil {
		t.Fatal(err)
	}
	if im.generation == firstGeneration || !im.IsComplete() {
		t.Fatal("controlled restart did not install a new generation")
	}
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseStopsPollingCleanerAndUnloadWorker(t *testing.T) {
	im, bot := registeredLifecycleManager(t)
	unloaded := make(chan struct{})
	im.StoreUnit("expired", &Unit{TTL: time.Now().Add(-time.Second), OnUnload: func() { close(unloaded) }})
	select {
	case <-unloaded:
	case <-time.After(time.Second):
		t.Fatal("TTL cleaner did not run")
	}
	generation := im.generation
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-bot.stopped:
	default:
		t.Fatal("polling was not stopped")
	}
	select {
	case <-generation.done:
	default:
		t.Fatal("generation workers survived Close")
	}
}

type tokenLifecycleClient struct {
	testInlineUserBot
	webURL string
}

type blockedWebViewClient struct {
	testInlineUserBot
	started chan struct{}
	release chan struct{}
}

func (c *blockedWebViewClient) RequestWebView(string, string, string) (string, error) {
	close(c.started)
	<-c.release
	return "", errors.New("released")
}

func TestBlockedRequestWebViewDoesNotBlockClose(t *testing.T) {
	client := &blockedWebViewClient{started: make(chan struct{}), release: make(chan struct{})}
	im := NewInlineManager(client, newLifecycleDB(), &testInlineModules{})
	done := make(chan error, 1)
	go func() {
		_, err := im.ReassertToken()
		done <- err
	}()
	<-client.started

	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReassertToken error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked RequestWebView retained its transaction or generation lease")
	}
	close(client.release)
}

func TestBlockedRequestWebViewDuringRegistrationDoesNotRetainRegisterLock(t *testing.T) {
	client := &blockedWebViewClient{started: make(chan struct{}), release: make(chan struct{})}
	db := newLifecycleDB()
	db.data["bot_token"] = ""
	im := NewInlineManager(client, db, &testInlineModules{})
	registerDone := make(chan error, 1)
	go func() { registerDone <- im.RegisterManager(false, false) }()
	<-client.started

	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-registerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RegisterManager error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked RequestWebView retained registerMu")
	}
	close(client.release)
}

func TestCloseClearsLiveStateAndUnloadsEachUnitOnce(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, nil, &testInlineModules{})
	var unloads atomic.Int32
	for _, id := range []string{"one", "two"} {
		im.StoreUnit(id, &Unit{Buttons: [][]Button{{{Data: id}}}, OnUnload: func() { unloads.Add(1) }})
	}
	im.mu.Lock()
	im.activeInlineMessages["one"] = "inline"
	im.activeMessageIDs["one"] = MessageIDInfo{ChatID: 1, MessageID: 2}
	im.QueryGalleries["gallery"] = QueryGalleryItem{}
	im.webAuthTokens = append(im.webAuthTokens, "web")
	im.fsm["user"] = "state"
	im.errorEvents["one"] = make(chan error, 1)
	im.mu.Unlock()

	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if unloads.Load() != 2 {
		t.Fatalf("OnUnload calls = %d, want 2", unloads.Load())
	}
	im.mu.RLock()
	defer im.mu.RUnlock()
	if len(im.units)+len(im.activeInlineMessages)+len(im.activeMessageIDs)+len(im.customMap)+len(im.buttonUnits)+len(im.QueryGalleries)+len(im.webAuthTokens)+len(im.fsm)+len(im.errorEvents) != 0 {
		t.Fatal("live inline state survived Close")
	}
}

func TestBoundedCallbackExecutorOverflowAndShutdownDrain(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, nil, &testInlineModules{})
	old := im.generation
	old.stop()
	<-old.done
	im.callbackWorkers = 2
	im.callbackQueueCapacity = 2
	im.generation = im.newGeneration()

	started := make(chan struct{}, im.callbackWorkers)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	job := func(context.Context) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			oldMaximum := maximum.Load()
			if current <= oldMaximum || maximum.CompareAndSwap(oldMaximum, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
	}
	for i := 0; i < im.callbackWorkers; i++ {
		if !im.generation.submit(im.generation.callbackJobs, job) {
			t.Fatal("active callback rejected")
		}
	}
	for i := 0; i < im.callbackWorkers; i++ {
		<-started
	}
	for i := 0; i < im.callbackQueueCapacity; i++ {
		if !im.generation.submit(im.generation.callbackJobs, job) {
			t.Fatal("callback within queue capacity rejected")
		}
	}
	baselineGoroutines := runtime.NumGoroutine()
	for i := 0; i < 1000; i++ {
		if im.generation.submit(im.generation.callbackJobs, job) {
			t.Fatal("callback beyond queue capacity was accepted")
		}
	}
	if growth := runtime.NumGoroutine() - baselineGoroutines; growth > 1 {
		t.Fatalf("callback burst created %d goroutines", growth)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- im.Close(context.Background()) }()
	select {
	case <-closeDone:
		t.Fatal("Close did not wait for active callbacks")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() > int32(im.callbackWorkers) {
		t.Fatalf("maximum callbacks = %d, capacity = %d", maximum.Load(), im.callbackWorkers)
	}
}

func TestBoundedUpdateExecutorConcurrencyAndOverflow(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, nil, &testInlineModules{})
	old := im.generation
	old.stop()
	<-old.done
	im.updateWorkers = 2
	im.updateQueueCapacity = 2
	im.generation = im.newGeneration()

	release := make(chan struct{})
	started := make(chan struct{}, im.updateWorkers)
	var active atomic.Int32
	var maximum atomic.Int32
	job := func(context.Context) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			oldMaximum := maximum.Load()
			if current <= oldMaximum || maximum.CompareAndSwap(oldMaximum, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
	}
	for i := 0; i < im.updateWorkers; i++ {
		if !im.generation.submit(im.generation.updateJobs, job) {
			t.Fatal("active update rejected")
		}
	}
	for i := 0; i < im.updateWorkers; i++ {
		<-started
	}
	for i := 0; i < im.updateQueueCapacity; i++ {
		if !im.generation.submit(im.generation.updateJobs, job) {
			t.Fatal("update within queue capacity rejected")
		}
	}
	baselineGoroutines := runtime.NumGoroutine()
	for i := 0; i < 1000; i++ {
		if im.generation.submit(im.generation.updateJobs, job) {
			t.Fatal("update beyond queue capacity was accepted")
		}
	}
	if growth := runtime.NumGoroutine() - baselineGoroutines; growth > 1 {
		t.Fatalf("update burst created %d goroutines", growth)
	}
	close(release)
	if err := im.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() > int32(im.updateWorkers) {
		t.Fatalf("maximum updates = %d, capacity = %d", maximum.Load(), im.updateWorkers)
	}
}

func (c *tokenLifecycleClient) RequestWebView(string, string, string) (string, error) {
	return c.webURL, nil
}

func newTokenLifecycleManager(t *testing.T) (*InlineManager, *lifecycleBot, *atomic.Int32) {
	t.Helper()
	var canceled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `Main.init('hash');<a href="/botfather/bot/42">@Goroku_abcdef_bot</a>`)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("method") == "revokeAccessToken" {
			select {
			case <-time.After(20 * time.Millisecond):
			case <-r.Context().Done():
				canceled.Store(true)
				return
			}
			_, _ = fmt.Fprint(w, `{"ok":true,"token":"replacement-token"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)

	db := newLifecycleDB()
	client := &tokenLifecycleClient{webURL: server.URL}
	im := NewInlineManager(client, db, &testInlineModules{})
	firstBot := newLifecycleBot()
	var factories atomic.Int32
	im.newBot = func(token string) (inlineBot, error) {
		call := factories.Add(1)
		if call > 1 && token != "replacement-token" {
			return nil, fmt.Errorf("replacement factory token = %q", token)
		}
		if call == 1 {
			return firstBot, nil
		}
		return newLifecycleBot(), nil
	}
	if err := im.RegisterManager(false, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = im.Close(context.Background()) })
	t.Cleanup(func() {
		if canceled.Load() {
			t.Error("replacement HTTP request was canceled with old generation")
		}
	})
	return im, firstBot, &factories
}

func waitForFactoryCalls(t *testing.T, calls *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() < want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != want {
		t.Fatalf("bot factory calls = %d, want %d", calls.Load(), want)
	}
}

func TestDPRevokeTokenDirectUsesTwoPhaseRestart(t *testing.T) {
	im, oldBot, factories := newTokenLifecycleManager(t)
	ok, err := im.DPRevokeToken(true)
	if err != nil || !ok {
		t.Fatalf("DPRevokeToken = %v, %v", ok, err)
	}
	waitForFactoryCalls(t, factories, 2)
	if oldBot.stopCalls.Load() != 1 {
		t.Fatalf("old bot stop calls = %d, want 1", oldBot.stopCalls.Load())
	}
}

func TestReassertTokenFromTrackedCallbackUsesTwoPhaseRestart(t *testing.T) {
	im, oldBot, factories := newTokenLifecycleManager(t)
	result := make(chan error, 1)
	im.mu.Lock()
	im.customMap["reassert"] = Button{Handler: func(CallbackQuery) error {
		ok, err := im.ReassertToken()
		if err == nil && !ok {
			err = errors.New("reassert returned false")
		}
		result <- err
		return err
	}}
	im.mu.Unlock()
	oldBot.updates <- tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		ID: "tracked", From: &tgbotapi.User{}, Data: "reassert",
	}}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	waitForFactoryCalls(t, factories, 2)
}

func TestTokenAccessConcurrentWithRegistrationState(t *testing.T) {
	im := NewInlineManager(&testInlineUserBot{}, newLifecycleDB(), &testInlineModules{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				im.setToken(fmt.Sprintf("%d-%d", i, j))
				_ = im.tokenValue()
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentTokenTransactionsAreSerialized(t *testing.T) {
	im, _, _ := newTokenLifecycleManager(t)
	var active atomic.Int32
	var maximum atomic.Int32
	originalClient := im.client.(*tokenLifecycleClient)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `Main.init('hash');<a href="/botfather/bot/42">@Goroku_abcdef_bot</a>`)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("method") == "revokeAccessToken" {
			_, _ = fmt.Fprint(w, `{"ok":true,"token":"replacement-token"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	originalClient.webURL = server.URL

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ok, err := im.ReassertToken()
			if err == nil && !ok {
				err = errors.New("reassert returned false")
			}
			results <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("concurrent token HTTP operations = %d, want 1", maximum.Load())
	}
}
