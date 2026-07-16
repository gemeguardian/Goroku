package goroku

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"goroku/goroku/inline"
	"goroku/goroku/web"
)

type lifecycleModule struct {
	unloads  atomic.Int32
	handler  CommandHandler
	watcher  WatcherHandler
	inline   inline.InlineHandler
	onUnload func() error
}

type failingContextCloser struct{ err error }

func (c failingContextCloser) Close(context.Context) error { return c.err }

func (m *lifecycleModule) Name() string                                { return "lifecycle" }
func (m *lifecycleModule) Strings() map[string]string                  { return nil }
func (m *lifecycleModule) Init(*CustomTelegramClient, *Database) error { return nil }
func (m *lifecycleModule) ClientReady() error                          { return nil }
func (m *lifecycleModule) OnUnload() error {
	m.unloads.Add(1)
	if m.onUnload != nil {
		return m.onUnload()
	}
	return nil
}
func (m *lifecycleModule) OnDlmod() error { return nil }
func (m *lifecycleModule) Commands() map[string]CommandHandler {
	return map[string]CommandHandler{"lifecycle": m.handler}
}
func (m *lifecycleModule) Watchers() []WatcherHandler {
	if m.watcher == nil {
		return nil
	}
	return []WatcherHandler{m.watcher}
}
func (m *lifecycleModule) InlineHandlers() map[string]inline.InlineHandler {
	if m.inline == nil {
		return nil
	}
	return map[string]inline.InlineHandler{"lifecycle": m.inline}
}

func TestGorokuShutdownClosesRuntimeIdempotently(t *testing.T) {
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	t.Cleanup(func() { BaseDir = originalBaseDir })

	db := NewDatabase(42)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	loader := NewModules(client, db)
	client.Loader = loader
	module := &lifecycleModule{handler: func(*Message) error { return nil }}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	loop := NewInfiniteLoop(func() error { return nil }, time.Hour, module.Name(), true)
	loader.RegisterLoop(loop)
	dispatcher := NewCommandDispatcher(loader, client, db)
	loader.SetDispatcher(dispatcher)
	inlineManager := inline.NewInlineManager(client, db, NewInlineModulesAdapter(loader))
	client.GorokuInline = inlineManager

	h := NewGoroku()
	h.Clients = append(h.Clients, client)
	h.DBs = append(h.DBs, db)
	h.Loaders = append(h.Loaders, loader)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	if module.unloads.Load() != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", module.unloads.Load())
	}
	if loop.IsRunning() {
		t.Fatal("module loop is still running")
	}
	if !db.closed {
		t.Fatal("database was not closed")
	}
	if !dispatcher.stopped {
		t.Fatal("dispatcher still accepts handlers")
	}
	select {
	case <-dispatcher.security.stopCh:
	default:
		t.Fatal("security manager was not stopped")
	}
	if _, ok := loader.Dispatch("lifecycle"); ok {
		t.Fatal("module command survived shutdown")
	}
}

func TestRunCancellationSharesExactlyOnceShutdown(t *testing.T) {
	loader := newInitializedTestModules(t)
	module := &lifecycleModule{}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	app := NewGoroku()
	app.Loaders = []*Modules{loader}
	started := make(chan struct{})
	app.start = func(context.Context) error { close(started); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan error, 2)
	go func() { results <- app.Run(ctx) }()
	<-started
	go func() { results <- app.Run(context.Background()) }()
	cancel()
	for i := 0; i < 2; i++ {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want canceled", err)
		}
	}
	if got := module.unloads.Load(); got != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", got)
	}
}

func TestDirectShutdownCancelsBlockedStartupAndWakesRun(t *testing.T) {
	app := NewGoroku()
	started := make(chan struct{})
	app.start = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(context.Background()) }()
	<-started
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run error after direct Shutdown = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("direct Shutdown did not wake Run")
	}
	if len(app.Clients) != 0 || len(app.DBs) != 0 || len(app.Loaders) != 0 || app.Web != nil {
		t.Fatal("resource survived canceled startup")
	}
}

func TestShutdownBeforeRunIsDeterministic(t *testing.T) {
	app := NewGoroku()
	var starts atomic.Int32
	app.start = func(context.Context) error { starts.Add(1); return nil }
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("startup calls after Shutdown = %d", got)
	}
}

func TestRunShutdownDeadlineBoundsBlockedComponent(t *testing.T) {
	cause := errors.New("early shutdown failure")
	failingLoader := newInitializedTestModules(t)
	if err := failingLoader.RegisterModule(&lifecycleModule{onUnload: func() error { return cause }}); err != nil {
		t.Fatal(err)
	}
	loader := newInitializedTestModules(t)
	unloadStarted := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{onUnload: func() error {
		close(unloadStarted)
		<-release
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	app := NewGoroku()
	app.Loaders = []*Modules{failingLoader, loader}
	app.ShutdownTimeout = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	cancel()
	<-unloadStarted
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cause) {
			t.Fatalf("Run error = %v, want cancellation, component failure, and shutdown deadline", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run exceeded its shutdown deadline")
	}
	close(release)
	if err := app.Shutdown(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("completed Shutdown error = %v, want component failure", err)
	}
}

func TestRunShutdownDeadlineBoundsUncooperativeStartup(t *testing.T) {
	app := NewGoroku()
	app.ShutdownTimeout = 25 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	app.start = func(context.Context) error {
		close(started)
		<-release
		if err := app.beginLifecycleOperation(); err == nil {
			app.endLifecycleOperation()
			app.Web = web.NewWebCore(web.WebConfig{})
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run error = %v, want cancellation and shutdown deadline", err)
		}
	case <-time.After(time.Second):
		t.Fatal("uncooperative startup kept Run blocked")
	}
	close(release)
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if app.Web != nil || len(app.Clients) != 0 || len(app.DBs) != 0 || len(app.Loaders) != 0 {
		t.Fatal("late resource survived shutdown intake closure")
	}
}

func TestConcurrentApplicationsRejectGlobalOwnership(t *testing.T) {
	first := NewGoroku()
	started := make(chan struct{})
	first.start = func(context.Context) error { close(started); return nil }
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Run(context.Background()) }()
	<-started

	second := NewGoroku()
	if err := second.Run(context.Background()); !errors.Is(err, ErrAppAlreadyRunning) {
		t.Fatalf("second Run error = %v, want ownership rejection", err)
	}
	first.RequestStop()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownClosesOwnedTelegramLoggerOnly(t *testing.T) {
	owned := &TelegramLogsHandler{}
	owned.InstallTGLog(nil, 1)
	other := &TelegramLogsHandler{}
	other.InstallTGLog(nil, 1)
	original := TGLogHandler
	TGLogHandler = other
	t.Cleanup(func() { TGLogHandler = original })

	app := NewGoroku()
	app.TGLogs = owned
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	owned.mu.Lock()
	ownedActive := owned.active
	owned.mu.Unlock()
	other.mu.Lock()
	otherActive := other.active
	other.mu.Unlock()
	if ownedActive || !otherActive {
		t.Fatalf("logger ownership mismatch: owned active=%v other active=%v", ownedActive, otherActive)
	}
	if err := other.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInitClientConnectSeamObservesCancellation(t *testing.T) {
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	t.Cleanup(func() { BaseDir = originalBaseDir })
	app := NewGoroku()
	started := make(chan struct{})
	app.connectClient = func(ctx context.Context, _ *CustomTelegramClient) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := app.initClientContext(ctx, 42, filepath.Join(BaseDir, "test.session"), nil)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("initClientContext error = %v, want canceled", err)
	}
	if len(app.Clients) != 0 || len(app.DBs) != 0 || len(app.Loaders) != 0 {
		t.Fatal("canceled connect registered runtime state")
	}
}

func TestTelegramLogCloseRetainsFailedFlushOnce(t *testing.T) {
	cause := errors.New("delivery failed")
	handler := &TelegramLogsHandler{buf: []string{"record"}}
	var attempts atomic.Int32
	handler.deliver = func([]string) error {
		attempts.Add(1)
		return cause
	}
	handler.InstallTGLog(nil, 1)
	if err := handler.Close(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("Close error = %v, want delivery failure", err)
	}
	if got := handler.Dump(); len(got) != 1 || got[0] != "record" {
		t.Fatalf("buffer after failed flush = %#v", got)
	}
	if err := handler.Close(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("repeated Close error = %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("delivery attempts = %d, want 1", got)
	}
}

func TestTelegramLogFlushRemovesOnlyDeliveredPrefix(t *testing.T) {
	handler := &TelegramLogsHandler{buf: []string{"first"}}
	handler.deliver = func(records []string) error {
		if len(records) != 1 || records[0] != "first" {
			t.Fatalf("delivery records = %#v", records)
		}
		_, _ = handler.Write([]byte("second"))
		return nil
	}
	if err := handler.flush(); err != nil {
		t.Fatal(err)
	}
	if got := handler.Dump(); len(got) != 1 || got[0] != "second" {
		t.Fatalf("buffer after successful flush = %#v", got)
	}
}

func TestShutdownWaitsForTelegramLogBeforeClientAndDatabase(t *testing.T) {
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	t.Cleanup(func() { BaseDir = originalBaseDir })
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	clientClosed := make(chan struct{})
	var closeOnce sync.Once
	client.runMu.Lock()
	client.cancel = func() { closeOnce.Do(func() { close(clientClosed) }) }
	client.runDone = make(chan struct{})
	close(client.runDone)
	client.runMu.Unlock()

	flushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	handler := &TelegramLogsHandler{buf: []string{"final record"}}
	handler.deliver = func([]string) error {
		close(flushStarted)
		<-releaseFlush
		return nil
	}
	handler.InstallTGLog(client, 1)

	app := NewGoroku()
	app.Clients = []*CustomTelegramClient{client}
	app.DBs = []*Database{db}
	app.TGLogs = handler
	app.ShutdownTimeout = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	cancel()
	<-flushStarted
	if err := <-done; !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want cancellation and shutdown deadline", err)
	}
	select {
	case <-clientClosed:
		t.Fatal("Telegram client closed while final log flush was blocked")
	default:
	}
	if db.closed {
		t.Fatal("database closed while final log flush was blocked")
	}

	close(releaseFlush)
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-clientClosed:
	default:
		t.Fatal("Telegram client was not closed after final log flush")
	}
	if !db.closed {
		t.Fatal("database was not closed after final log flush")
	}
}

func TestReleaseLoggingChecksOwnership(t *testing.T) {
	loggingMu.Lock()
	originalHandler := TGLogHandler
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	first := &TelegramLogsHandler{installedOutput: &ownedLogWriter{Writer: io.Discard}}
	second := &TelegramLogsHandler{
		previousOutput:  originalOutput,
		previousFlags:   originalFlags,
		installedOutput: &ownedLogWriter{Writer: io.Discard},
	}
	TGLogHandler = second
	log.SetOutput(second.installedOutput)
	loggingMu.Unlock()
	t.Cleanup(func() {
		loggingMu.Lock()
		TGLogHandler = originalHandler
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
		loggingMu.Unlock()
	})

	releaseLogging(first)
	if TGLogHandler != second || log.Writer() != second.installedOutput {
		t.Fatal("stale logger owner cleared its replacement")
	}
	releaseLogging(second)
	if TGLogHandler != nil || log.Writer() == second.installedOutput {
		t.Fatal("current logger owner was not released")
	}
}

func TestRunRestartWaitsForDrainAndReturnsTypedResult(t *testing.T) {
	loader := newInitializedTestModules(t)
	unloadStarted := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{onUnload: func() error {
		close(unloadStarted)
		<-release
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	app := NewGoroku()
	app.Loaders = []*Modules{loader}
	done := make(chan error, 1)
	go func() { done <- app.Run(context.Background()) }()
	if !app.RequestRestart() {
		t.Fatal("first restart request was rejected")
	}
	<-unloadStarted
	select {
	case err := <-done:
		t.Fatalf("Run returned before drain: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Run error = %v, want restart result", err)
	}
	if got := module.unloads.Load(); got != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", got)
	}
}

func TestLifecycleRequestsAreConcurrentIdempotent(t *testing.T) {
	app := NewGoroku()
	const callers = 32
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var ok bool
			if i%2 == 0 {
				ok = app.RequestStop()
			} else {
				ok = app.RequestRestart()
			}
			if ok {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted requests = %d, want 1", got)
	}
	if err := app.Run(context.Background()); err != nil && !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunPropagatesShutdownError(t *testing.T) {
	cause := errors.New("shutdown failed")
	loader := newInitializedTestModules(t)
	module := &lifecycleModule{onUnload: func() error { return cause }}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	app := NewGoroku()
	app.Loaders = []*Modules{loader}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := app.Run(ctx)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("Run error = %v, want cancellation and shutdown cause", err)
	}
}

func TestRunPropagatesStartupErrorAndStillShutsDown(t *testing.T) {
	cause := errors.New("startup failed")
	loader := newInitializedTestModules(t)
	module := &lifecycleModule{}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	app := NewGoroku()
	app.Loaders = []*Modules{loader}
	app.start = func(context.Context) error { return cause }
	if err := app.Run(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("Run error = %v, want startup cause", err)
	}
	if got := module.unloads.Load(); got != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", got)
	}
}

func TestClientCloseTimeoutIsRetryable(t *testing.T) {
	client := NewCustomTelegramClient(42)
	done := make(chan struct{})
	var cancels atomic.Int32
	client.runMu.Lock()
	client.runDone = done
	client.cancel = func() { cancels.Add(1) }
	client.runMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v, want deadline", err)
	}
	close(done)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("retry Close error = %v", err)
	}
	if got := cancels.Load(); got != 2 {
		t.Fatalf("cancel calls = %d, want 2", got)
	}
}

func TestSecurityCloseConcurrentWaitsForWorker(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	manager, err := NewSecurityManagerChecked(NewCustomTelegramClient(42), db)
	if err != nil {
		t.Fatal(err)
	}
	const callers = 16
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- manager.Close(context.Background()) }()
	}
	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-manager.done:
	default:
		t.Fatal("Close returned before the security worker completed")
	}
}

func TestPackageLifecycleDoesNotOwnSignalsOrTerminateProcess(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(".", entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if stmt, ok := node.(*ast.SelectStmt); ok && len(stmt.Body.List) == 0 {
				t.Errorf("%s contains an empty blocking select", path)
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			forbidden := pkg.Name == "os" && selector.Sel.Name == "Exit" ||
				pkg.Name == "syscall" && selector.Sel.Name == "Exec" ||
				pkg.Name == "signal" && strings.HasPrefix(selector.Sel.Name, "Notify")
			if forbidden {
				t.Errorf("%s contains forbidden lifecycle call %s.%s", path, pkg.Name, selector.Sel.Name)
			}
			return true
		})
	}
}

func TestGorokuShutdownDrainsInlineBeforeModulesAndDatabase(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	loader := NewModules(client, db)
	client.Loader = loader
	started := make(chan struct{})
	release := make(chan struct{})
	var handlerDone atomic.Bool
	var orderViolation atomic.Bool
	module := &lifecycleModule{onUnload: func() error {
		if !handlerDone.Load() || db.closed {
			orderViolation.Store(true)
		}
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	im := inline.NewInlineManager(client, db, NewInlineModulesAdapter(loader))
	client.GorokuInline = im
	im.StoreUnit("shutdown", &inline.Unit{Module: module.Name(), DisableSecurity: true, Buttons: [][]inline.Button{{{
		Data: "shutdown", Handler: func(inline.CallbackQuery) error {
			close(started)
			<-release
			handlerDone.Store(true)
			return nil
		},
	}}}})
	im.HandleUpdate(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		ID: "shutdown", From: &tgbotapi.User{}, Data: "shutdown",
	}})
	<-started

	h := NewGoroku()
	h.Clients = append(h.Clients, client)
	h.DBs = append(h.DBs, db)
	h.Loaders = append(h.Loaders, loader)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Shutdown error = %v, want deadline", err)
	}
	if module.unloads.Load() != 0 || db.closed {
		t.Fatalf("dependencies closed while inline handler active: unloads=%d db.closed=%v", module.unloads.Load(), db.closed)
	}
	close(release)
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if orderViolation.Load() {
		t.Fatal("module unload ran before inline drain or after database close")
	}
	if module.unloads.Load() != 1 || !db.closed {
		t.Fatalf("final teardown: unloads=%d db.closed=%v", module.unloads.Load(), db.closed)
	}
	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	if err := h.Shutdown(canceled); err != nil {
		t.Fatalf("completed Shutdown with canceled context = %v", err)
	}
}

func TestRemoveRuntimeDefersDependenciesUntilInlineDrain(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(43))
	client := NewCustomTelegramClient(43)
	client.GorokuDB = db
	loader := NewModules(client, db)
	client.Loader = loader
	started := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	im := inline.NewInlineManager(client, db, NewInlineModulesAdapter(loader))
	client.GorokuInline = im
	im.StoreUnit("partial", &inline.Unit{Module: module.Name(), DisableSecurity: true, Buttons: [][]inline.Button{{{
		Data: "partial", Handler: func(inline.CallbackQuery) error {
			close(started)
			<-release
			return nil
		},
	}}}})
	im.HandleUpdate(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "partial", From: &tgbotapi.User{}, Data: "partial"}})
	<-started

	h := NewGoroku()
	h.Clients = []*CustomTelegramClient{client}
	h.Loaders = []*Modules{loader}
	h.DBs = []*Database{db}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := h.removeAndShutdownRuntime(ctx, client, loader, db)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrRuntimeCleanupDeferred) {
		t.Fatalf("cleanup error = %v, want deadline and deferred cleanup", err)
	}
	if module.unloads.Load() != 0 || db.closed {
		t.Fatalf("dependencies destroyed before inline drain: unloads=%d closed=%v", module.unloads.Load(), db.closed)
	}
	close(release)
	if err := loader.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if module.unloads.Load() != 1 {
		t.Fatalf("deferred cleanup did not unload module: unloads=%d", module.unloads.Load())
	}
}

func TestInlineModuleHandlerLeaseDefersUnload(t *testing.T) {
	loader := newInitializedTestModules(t)
	started := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{inline: func(*inline.InlineQuery) ([]inline.InlineResult, error) {
		close(started)
		<-release
		return nil, nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	client := loader.client
	client.TGID = 42
	im := inline.NewInlineManager(client, loader.db, NewInlineModulesAdapter(loader))
	done := make(chan struct{})
	go func() {
		im.HandleUpdate(tgbotapi.Update{InlineQuery: &tgbotapi.InlineQuery{ID: "lease", From: &tgbotapi.User{ID: 42}, Query: "lifecycle"}})
		close(done)
	}()
	<-started
	if err := loader.UnloadModule(module.Name()); !errors.Is(err, ErrModuleUnloadInProgress) {
		t.Fatalf("unload error = %v, want in progress", err)
	}
	if module.unloads.Load() != 0 {
		t.Fatal("module unloaded while inline handler held its lease")
	}
	close(release)
	<-done
	if err := loader.UnloadModule(module.Name()); err != nil {
		t.Fatal(err)
	}
	if module.unloads.Load() != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", module.unloads.Load())
	}
}

func TestDispatcherCloseWaitsAndRejectsNewHandlers(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	loader := NewModules(client, db)
	client.Loader = loader
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	module := &lifecycleModule{handler: func(*Message) error {
		calls.Add(1)
		close(started)
		<-release
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewCommandDispatcher(loader, client, db)
	loader.SetDispatcher(dispatcher)
	message := &Message{SenderID: 42, ChatID: 42, Text: ".lifecycle", RawText: ".lifecycle", Out: true}
	dispatcher.HandleCommand(message)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- dispatcher.Close(context.Background()) }()
	select {
	case <-closed:
		t.Fatal("Close returned before the active handler finished")
	case <-time.After(20 * time.Millisecond):
	}
	dispatcher.HandleCommand(message)
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", calls.Load())
	}
}

func TestDispatcherCloseCancelsMessageContext(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	loader := NewModules(client, db)
	client.Loader = loader
	started := make(chan struct{})
	canceled := make(chan struct{})
	module := &lifecycleModule{handler: func(message *Message) error {
		close(started)
		<-message.Context().Done()
		close(canceled)
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCommandDispatcherWithConfig(loader, client, db, testDispatcherConfig(nil, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".lifecycle", RawText: ".lifecycle", Out: true})
	<-started
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("handler did not observe dispatcher cancellation")
	}
}

func TestDispatcherTaskContextDoesNotInheritConnectionCancellation(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	connectionCtx, cancelConnection := context.WithCancel(context.Background())
	client.ctx = connectionCtx
	cancelConnection()
	loader := NewModules(client, db)
	started := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{handler: func(message *Message) error {
		select {
		case <-message.Context().Done():
			t.Error("fresh dispatcher task inherited canceled connection context")
		default:
		}
		close(started)
		<-release
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCommandDispatcherWithConfig(loader, client, db, testDispatcherConfig(nil, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".lifecycle", RawText: ".lifecycle", Out: true})
	<-started
	close(release)
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherCloseTimeoutIsRetryable(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	loader := NewModules(client, db)
	started := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{handler: func(*Message) error {
		close(started)
		<-release
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCommandDispatcherWithConfig(loader, client, db, testDispatcherConfig(nil, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".lifecycle", RawText: ".lifecycle", Out: true})
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := dispatcher.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v, want deadline exceeded", err)
	}
	close(release)
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("retry Close error = %v", err)
	}
}

func TestDispatcherPanicReleasesExecutorSlotAndModuleLease(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	loader := NewModules(client, db)
	started := make(chan struct{})
	module := &lifecycleModule{handler: func(*Message) error {
		close(started)
		panic("dispatcher panic")
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCommandDispatcherWithConfig(loader, client, db, testDispatcherConfig(nil, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".lifecycle", RawText: ".lifecycle", Out: true})
	<-started
	deadline := time.Now().Add(time.Second)
	for dispatcher.commands.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := dispatcher.commands.Active(); got != 0 {
		t.Fatalf("active command slots after panic = %d, want 0", got)
	}
	if err := loader.UnloadModule("lifecycle"); err != nil {
		t.Fatalf("unload after panic: %v", err)
	}
	if got := module.unloads.Load(); got != 1 {
		t.Fatalf("OnUnload calls after panic = %d, want 1", got)
	}
}

func TestUnloadDefersTeardownForActiveCommandHandler(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner": []any{int64(42)}, "all_users": []any{}, "bounding_mask": float64(ALL),
	}
	db.data["goroku.main"] = map[string]any{"command_prefix": "."}
	client := NewCustomTelegramClient(42)
	loader := NewModules(client, db)
	client.Loader = loader
	started := make(chan struct{})
	release := make(chan struct{})
	module := &lifecycleModule{handler: func(*Message) error {
		close(started)
		<-release
		return nil
	}}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewCommandDispatcher(loader, client, db)
	t.Cleanup(dispatcher.security.Stop)
	dispatcher.HandleCommand(&Message{SenderID: 42, ChatID: 42, Text: ".lifecycle", RawText: ".lifecycle", Out: true})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	unloaded := make(chan error, 1)
	go func() { unloaded <- loader.UnloadModule("lifecycle") }()
	select {
	case err := <-unloaded:
		if !errors.Is(err, ErrModuleUnloadInProgress) {
			t.Fatalf("unload error = %v, want in-progress", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unload blocked on an active lease")
	}
	if module.unloads.Load() != 0 {
		t.Fatal("OnUnload ran before the active handler completed")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for module.unloads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if module.unloads.Load() != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", module.unloads.Load())
	}
}

func TestCommandCanUnloadItsOwnModule(t *testing.T) {
	loader := newInitializedTestModules(t)
	unloaded := make(chan struct{})
	moduleWithSignal := &registrationLifecycleModule{
		name:     "self-command",
		commands: map[string]CommandHandler{},
		onUnload: func() error {
			close(unloaded)
			return nil
		},
	}
	moduleWithSignal.commands["self-command"] = func(*Message) error {
		return loader.UnloadModule(moduleWithSignal.Name())
	}
	if err := loader.RegisterModule(moduleWithSignal); err != nil {
		t.Fatal(err)
	}
	handler, ok := loader.Dispatch("self-command")
	if !ok {
		t.Fatal("self-unloading command was not registered")
	}
	done := make(chan error, 1)
	go func() { done <- handler(&Message{}) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrModuleUnloadInProgress) {
			t.Fatalf("self-unload error = %v, want in-progress", err)
		}
	case <-time.After(time.Second):
		t.Fatal("self-unloading command deadlocked")
	}
	select {
	case <-unloaded:
	case <-time.After(time.Second):
		t.Fatal("self-unloading command did not finish deferred teardown")
	}
}

func TestWatcherCanUnloadItsOwnModule(t *testing.T) {
	loader := newInitializedTestModules(t)
	unloaded := make(chan struct{})
	module := &registrationLifecycleModule{name: "self-watcher", commands: map[string]CommandHandler{}, onUnload: func() error {
		close(unloaded)
		return nil
	}}
	module.watchers = []WatcherHandler{func(*Message) error { return loader.UnloadModule(module.Name()) }}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	watcher := loader.GetWatchers()[0]
	if !watcher.lease.acquire() {
		t.Fatal("self-unloading watcher lease was unavailable")
	}
	done := make(chan error, 1)
	go func() {
		defer watcher.lease.release()
		done <- watcher.Handler(&Message{})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrModuleUnloadInProgress) {
			t.Fatalf("self-unload error = %v, want in-progress", err)
		}
	case <-time.After(time.Second):
		t.Fatal("self-unloading watcher deadlocked")
	}
	select {
	case <-unloaded:
	case <-time.After(time.Second):
		t.Fatal("self-unloading watcher did not finish deferred teardown")
	}
}

func TestShutdownTimeoutCompletesDeferredUnloadExactlyOnce(t *testing.T) {
	loader := newInitializedTestModules(t)
	started := make(chan struct{})
	release := make(chan struct{})
	unloadCause := errors.New("shutdown unload failed")
	module := &lifecycleModule{
		handler: func(*Message) error {
			close(started)
			<-release
			return nil
		},
		onUnload: func() error { return unloadCause },
	}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	handler, ok := loader.Dispatch("lifecycle")
	if !ok {
		t.Fatal("command was not registered")
	}
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- handler(&Message{}) }()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := loader.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first shutdown error = %v, want canceled", err)
	}
	if module.unloads.Load() != 0 {
		t.Fatal("OnUnload ran while the command lease was active")
	}
	close(release)
	if err := <-handlerDone; err != nil {
		t.Fatal(err)
	}
	if err := loader.Shutdown(context.Background()); !errors.Is(err, unloadCause) {
		t.Fatalf("second shutdown error = %v, want unload cause", err)
	}
	if err := loader.Shutdown(context.Background()); !errors.Is(err, unloadCause) {
		t.Fatalf("third shutdown error = %v, want unload cause", err)
	}
	if module.unloads.Load() != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", module.unloads.Load())
	}
}

func TestSelfUnloadBlocksReplacementUntilTeardownCompletes(t *testing.T) {
	loader := newInitializedTestModules(t)
	unloadStarted := make(chan struct{})
	unloadRelease := make(chan struct{})
	old := &registrationLifecycleModule{name: "replace", commands: map[string]CommandHandler{}}
	old.onUnload = func() error {
		close(unloadStarted)
		<-unloadRelease
		return nil
	}
	old.commands["replace"] = func(*Message) error { return loader.UnloadModule(old.Name()) }
	if err := loader.RegisterModule(old); err != nil {
		t.Fatal(err)
	}
	handler, _ := loader.Dispatch("replace")
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- handler(&Message{}) }()
	if err := <-handlerDone; !errors.Is(err, ErrModuleUnloadInProgress) {
		t.Fatalf("self-unload error = %v, want in-progress", err)
	}
	<-unloadStarted

	replacement := &registrationLifecycleModule{name: "replace", commands: map[string]CommandHandler{"replace": func(*Message) error { return nil }}}
	if err := loader.RegisterModule(replacement); !errors.Is(err, ErrModuleUnloadInProgress) {
		t.Fatalf("replacement error = %v, want in-progress", err)
	}
	if replacement.initCalls.Load() != 0 {
		t.Fatal("replacement initialized before old OnUnload completed")
	}
	close(unloadRelease)
	if err := loader.UnloadModule("replace"); err != nil {
		t.Fatal(err)
	}
	if err := loader.RegisterModule(replacement); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredUnloadErrorIsObservable(t *testing.T) {
	loader := newInitializedTestModules(t)
	release := make(chan struct{})
	cause := errors.New("deferred unload failed")
	module := &lifecycleModule{
		handler:  func(*Message) error { <-release; return nil },
		onUnload: func() error { return cause },
	}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	handler, _ := loader.Dispatch("lifecycle")
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- handler(&Message{}) }()
	for {
		moduleLease := loader.leases["lifecycle"]
		moduleLease.mu.Lock()
		active := moduleLease.active
		moduleLease.mu.Unlock()
		if active == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := loader.UnloadModule("lifecycle"); !errors.Is(err, ErrModuleUnloadInProgress) {
		t.Fatalf("unload error = %v, want in-progress", err)
	}
	close(release)
	if err := <-handlerDone; err != nil {
		t.Fatal(err)
	}
	if err := loader.UnloadModule("lifecycle"); !errors.Is(err, cause) {
		t.Fatalf("repeated unload error = %v, want deferred cause", err)
	}
}

func TestInitClientReturnsDatabaseInitErrorWithoutRegisteringRuntime(t *testing.T) {
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	t.Cleanup(func() { BaseDir = originalBaseDir })
	t.Setenv("REDIS_URL", "://invalid")

	h := NewGoroku()
	_, err := h.initClient(42, BaseDir+"/test.session", nil)
	if err == nil || !strings.Contains(err.Error(), "initialize database") {
		t.Fatalf("initClient error = %v, want database initialization error", err)
	}
	if len(h.Clients) != 0 || len(h.DBs) != 0 || len(h.Loaders) != 0 {
		t.Fatal("failed initialization registered partial runtime state")
	}
	if _, statErr := os.Stat(BaseDir + "/config-42.json"); statErr == nil {
		t.Fatal("invalid Redis configuration unexpectedly initialized the database")
	}
}

func TestDatabaseCleanupJoinsCloseFailure(t *testing.T) {
	primary := errors.New("primary initialization failure")
	closeErr := errors.New("database close failure")
	err := joinDatabaseCloseError(primary, failingContextCloser{err: closeErr})
	if !errors.Is(err, primary) || !errors.Is(err, closeErr) {
		t.Fatalf("cleanup error = %v, want primary and close failures", err)
	}
}

func TestDuplicateWebRegistrationCleansOnlyNewRuntime(t *testing.T) {
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	t.Cleanup(func() { BaseDir = originalBaseDir })

	existingDB := NewDatabase(42)
	if err := existingDB.Init(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = existingDB.Close(context.Background()) })
	existingClient := NewCustomTelegramClient(42)
	existingClient.GorokuDB = existingDB
	existingLoader := NewModules(existingClient, existingDB)
	existingClient.Loader = existingLoader

	newDB := NewDatabase(42)
	if err := newDB.Init(""); err != nil {
		t.Fatal(err)
	}
	newClient := NewCustomTelegramClient(42)
	newClient.GorokuDB = newDB
	newLoader := NewModules(newClient, newDB)
	newClient.Loader = newLoader
	newModule := &lifecycleModule{}
	if err := newLoader.RegisterModule(newModule); err != nil {
		t.Fatal(err)
	}
	clientCtx, clientCancel := context.WithCancel(context.Background())
	newClient.ctx = clientCtx
	newClient.cancel = clientCancel

	h := NewGoroku()
	h.Web = web.NewWebCore(web.WebConfig{})
	h.Clients = append(h.Clients, existingClient, newClient)
	h.DBs = append(h.DBs, existingDB, newDB)
	h.Loaders = append(h.Loaders, existingLoader, newLoader)
	if err := h.Web.RegisterClient(web.RuntimeClient{ID: 42, Client: existingClient, Loader: existingLoader, Database: existingDB}); err != nil {
		t.Fatal(err)
	}

	err := h.registerWebRuntime(newClient)
	if !errors.Is(err, web.ErrDuplicateClient) {
		t.Fatalf("error = %v, want typed duplicate error", err)
	}
	if len(h.Clients) != 1 || h.Clients[0] != existingClient || len(h.DBs) != 1 || h.DBs[0] != existingDB || len(h.Loaders) != 1 || h.Loaders[0] != existingLoader {
		t.Fatalf("new duplicate runtime was not removed: clients=%d dbs=%d loaders=%d", len(h.Clients), len(h.DBs), len(h.Loaders))
	}
	clients := h.Web.ListClients()
	if len(clients) != 1 || clients[0].Client != existingClient {
		t.Fatalf("existing web runtime was harmed: %#v", clients)
	}
	if !newDB.closed || existingDB.closed {
		t.Fatalf("database cleanup mismatch: new closed=%v existing closed=%v", newDB.closed, existingDB.closed)
	}
	if newModule.unloads.Load() != 1 {
		t.Fatalf("new runtime module unloads = %d, want 1", newModule.unloads.Load())
	}
	select {
	case <-clientCtx.Done():
	default:
		t.Fatal("new duplicate client was not stopped")
	}
}
