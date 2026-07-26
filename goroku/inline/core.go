package inline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"goroku/goroku/chatref"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

var _ = zap.NewNop

type MessageIDInfo struct {
	ChatID    int64
	MessageID int64
}

type InlineManager struct {
	mu                    sync.RWMutex
	registerMu            sync.Mutex
	bot                   *tgbotapi.BotAPI
	client                InlineUserBot
	db                    Database
	allModules            InlineModules
	units                 map[string]*Unit
	activeInlineMessages  map[string]string
	activeMessageIDs      map[string]MessageIDInfo
	customMap             map[string]Button
	buttonUnits           map[string]string
	QueryGalleries        map[string]QueryGalleryItem
	webAuthTokens         []string
	fsm                   map[string]string
	errorEvents           map[string]chan error
	initComplete          bool
	token                 string
	BotUsername           string
	BotID                 int64
	markupTTL             time.Duration
	cleanerInterval       time.Duration
	generation            *inlineGeneration
	newBot                func(string) (inlineBot, error)
	closeOnce             sync.Once
	closeDone             chan struct{}
	closeErr              error
	closed                bool
	restartMu             sync.Mutex
	tokenTxnMu            sync.Mutex
	tokenRevision         uint64
	updateWorkers         int
	updateQueueCapacity   int
	callbackWorkers       int
	callbackQueueCapacity int
	webViewOnce           sync.Once
	webViewJobs           chan webViewJob
	lifecycleCtx          context.Context
	lifecycleCancel       context.CancelFunc
}

const (
	DefaultUpdateWorkers         = 4
	DefaultUpdateQueueCapacity   = 64
	DefaultCallbackWorkers       = 8
	DefaultCallbackQueueCapacity = 64
	defaultUnloadWorkers         = 4
	defaultWebViewQueueCapacity  = 1
)

type inlineBot interface {
	API() *tgbotapi.BotAPI
	Self() tgbotapi.User
	Updates(tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
	StopPolling()
}

type telegramInlineBot struct{ bot *tgbotapi.BotAPI }

func (b telegramInlineBot) API() *tgbotapi.BotAPI { return b.bot }
func (b telegramInlineBot) Self() tgbotapi.User   { return b.bot.Self }
func (b telegramInlineBot) Updates(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	return b.bot.GetUpdatesChan(config)
}
func (b telegramInlineBot) StopPolling() { b.bot.StopReceivingUpdates() }

type inlineGeneration struct {
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	accepting    bool
	bot          inlineBot
	wg           sync.WaitGroup
	stopOnce     sync.Once
	botOnce      sync.Once
	done         chan struct{}
	updateJobs   chan func(context.Context)
	callbackJobs chan func(context.Context)
}

func newInlineGeneration(updateQueueCapacity, callbackQueueCapacity int) *inlineGeneration {
	ctx, cancel := context.WithCancel(context.Background())
	return &inlineGeneration{
		ctx: ctx, cancel: cancel, accepting: true, done: make(chan struct{}),
		updateJobs:   make(chan func(context.Context), updateQueueCapacity),
		callbackJobs: make(chan func(context.Context), callbackQueueCapacity),
	}
}

func (g *inlineGeneration) startExecutors(updateWorkers, callbackWorkers int) {
	for i := 0; i < updateWorkers; i++ {
		g.start(func(ctx context.Context) { g.runJobs(ctx, g.updateJobs) })
	}
	for i := 0; i < callbackWorkers; i++ {
		g.start(func(ctx context.Context) { g.runJobs(ctx, g.callbackJobs) })
	}
}

func (g *inlineGeneration) runJobs(ctx context.Context, jobs <-chan func(context.Context)) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case job := <-jobs:
			if ctx.Err() != nil {
				return
			}
			job(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (g *inlineGeneration) submit(jobs chan func(context.Context), job func(context.Context)) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.accepting {
		return false
	}
	select {
	case jobs <- job:
		return true
	default:
		return false
	}
}

func (g *inlineGeneration) start(worker func(context.Context)) bool {
	if !g.acquire() {
		return false
	}
	go func() {
		defer g.release()
		worker(g.ctx)
	}()
	return true
}

func (g *inlineGeneration) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.accepting {
		return false
	}
	g.wg.Add(1)
	return true
}

func (g *inlineGeneration) release() { g.wg.Done() }

func (g *inlineGeneration) attachBot(bot inlineBot) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.accepting {
		return false
	}
	g.bot = bot
	return true
}

func (g *inlineGeneration) stop() {
	g.stopOnce.Do(func() {
		g.mu.Lock()
		g.accepting = false
		bot := g.bot
		g.mu.Unlock()
		g.cancel()
		g.stopPolling(bot)
		go func() {
			g.wg.Wait()
			close(g.done)
		}()
	})
}

func (g *inlineGeneration) stopPolling(bot inlineBot) {
	if bot != nil {
		g.botOnce.Do(bot.StopPolling)
	}
}

func NewInlineManager(client InlineUserBot, db Database, allModules InlineModules) *InlineManager {
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	im := &InlineManager{
		client:                client,
		db:                    db,
		allModules:            allModules,
		units:                 make(map[string]*Unit),
		activeInlineMessages:  make(map[string]string),
		activeMessageIDs:      make(map[string]MessageIDInfo),
		customMap:             make(map[string]Button),
		buttonUnits:           make(map[string]string),
		QueryGalleries:        make(map[string]QueryGalleryItem),
		webAuthTokens:         make([]string, 0),
		fsm:                   make(map[string]string),
		errorEvents:           make(map[string]chan error),
		markupTTL:             24 * time.Hour,
		cleanerInterval:       30 * time.Second,
		closeDone:             make(chan struct{}),
		updateWorkers:         DefaultUpdateWorkers,
		updateQueueCapacity:   DefaultUpdateQueueCapacity,
		callbackWorkers:       DefaultCallbackWorkers,
		callbackQueueCapacity: DefaultCallbackQueueCapacity,
		lifecycleCtx:          lifecycleCtx,
		lifecycleCancel:       lifecycleCancel,
	}
	im.generation = im.newGeneration()
	im.newBot = func(token string) (inlineBot, error) {
		bot, err := tgbotapi.NewBotAPI(token)
		if err != nil {
			return nil, err
		}
		return telegramInlineBot{bot: bot}, nil
	}
	return im
}

func (im *InlineManager) newGeneration() *inlineGeneration {
	updateWorkers, updateCapacity := im.updateWorkers, im.updateQueueCapacity
	callbackWorkers, callbackCapacity := im.callbackWorkers, im.callbackQueueCapacity
	if updateWorkers <= 0 {
		updateWorkers = DefaultUpdateWorkers
	}
	if updateCapacity <= 0 {
		updateCapacity = DefaultUpdateQueueCapacity
	}
	if callbackWorkers <= 0 {
		callbackWorkers = DefaultCallbackWorkers
	}
	if callbackCapacity <= 0 {
		callbackCapacity = DefaultCallbackQueueCapacity
	}
	generation := newInlineGeneration(updateCapacity, callbackCapacity)
	generation.startExecutors(updateWorkers, callbackWorkers)
	return generation
}

func (im *InlineManager) BotUsernameStr() string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.BotUsername
}

func (im *InlineManager) BotIDVal() int64 {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.BotID
}

func (im *InlineManager) RegisterManager(afterBreak bool, ignoreTokenChecks bool) error {
	im.registerMu.Lock()
	defer im.registerMu.Unlock()

	im.mu.Lock()
	if im.closed {
		im.mu.Unlock()
		return errInlineClosed
	}
	if im.initComplete && im.bot != nil && im.generation != nil {
		im.mu.Unlock()
		return nil
	}
	previous := im.generation
	im.mu.Unlock()
	if previous != nil {
		previous.stop()
		<-previous.done
	}

	generation := im.newGeneration()
	// Registration itself belongs to the generation, so Close also waits for
	// bootstrap and any partially completed startup work.
	generation.mu.Lock()
	generation.wg.Add(1)
	generation.mu.Unlock()
	defer generation.wg.Done()
	im.mu.Lock()
	if im.closed {
		im.mu.Unlock()
		generation.stop()
		return errInlineClosed
	}
	im.generation = generation
	im.bot = nil
	im.initComplete = false
	im.BotUsername = ""
	im.BotID = 0
	im.mu.Unlock()

	failed := true
	defer func() {
		if failed {
			generation.stop()
			im.mu.Lock()
			if im.generation == generation {
				im.bot = nil
				im.initComplete = false
				im.BotUsername = ""
				im.BotID = 0
			}
			im.mu.Unlock()
		}
	}()

	for retryAfterBreak := afterBreak; ; retryAfterBreak = true {
		if err := generation.ctx.Err(); err != nil {
			return err
		}
		token := im.tokenValue()
		if token == "" {
			var err error
			token, err = im.getToken()
			if err != nil {
				return err
			}
		}
		if token == "" && !ignoreTokenChecks {
			ok, err := im.assertTokenContext(generation.ctx, true, false)
			if err != nil {
				return fmt.Errorf("failed to assert bot token: %w", err)
			}
			if !ok {
				return fmt.Errorf("failed to assert bot token")
			}
			token = im.tokenValue()
		}
		if token == "" {
			return fmt.Errorf("no inline bot token configured")
		}

		bot, err := im.newBot(token)
		if err != nil && !ignoreTokenChecks {
			ok, assertErr := im.assertTokenContext(generation.ctx, true, false)
			if assertErr != nil {
				return fmt.Errorf("failed to assert bot token after bot api error %v: %w", err, assertErr)
			}
			if !ok {
				return fmt.Errorf("failed to assert bot token after bot api error: %w", err)
			}
			bot, err = im.newBot(im.tokenValue())
		}
		if err != nil {
			return fmt.Errorf("failed to init inline bot: %w", err)
		}
		if !generation.attachBot(bot) {
			bot.StopPolling()
			return generation.ctx.Err()
		}
		self := bot.Self()

		if err := im.resetBootstrapForBot(self.ID); err != nil {
			return err
		}
		retry, err := im.bootstrapUserBotSideFor(generation.ctx, retryAfterBreak, self.UserName, self.ID)
		if err != nil {
			return err
		}
		if retry {
			generation.mu.Lock()
			if generation.bot == bot {
				generation.bot = nil
			}
			generation.mu.Unlock()
			bot.StopPolling()
			continue
		}

		if !generation.start(func(ctx context.Context) { im.startPolling(ctx, bot, generation) }) ||
			!generation.start(func(ctx context.Context) { im.ttlCleaner(ctx) }) {
			return generation.ctx.Err()
		}
		im.mu.Lock()
		if im.closed || im.generation != generation || generation.ctx.Err() != nil {
			closed := im.closed
			im.mu.Unlock()
			if err := generation.ctx.Err(); err != nil {
				return err
			}
			if closed {
				return context.Canceled
			}
			return errInlineClosed
		}
		im.initComplete = true
		im.bot = bot.API()
		im.BotUsername = self.UserName
		im.BotID = self.ID
		im.mu.Unlock()
		failed = false
		L().Info("InlineManager started: @", zap.Any("user_name", self.UserName))
		return nil
	}
}

func (im *InlineManager) resetBootstrapForBot(botID int64) error {
	if im.db == nil {
		return nil
	}
	val, err := im.db.Get("goroku.inline", "last_bot_id", nil)
	if err != nil {
		return fmt.Errorf("read goroku.inline.last_bot_id: %w", err)
	}
	var lastBotID int64
	if val != nil {
		switch v := val.(type) {
		case float64:
			lastBotID = int64(v)
		case int64:
			lastBotID = v
		case int:
			lastBotID = int64(v)
		}
	}
	if lastBotID == botID {
		return nil
	}

	L().Info("[Inline] Inline bot ID changed from to (or first run). Resetting bootstrap flags.", zap.Any("last_bot_id", lastBotID), zap.Any("bot_id", botID))
	if err := im.db.Set("goroku.inline", "folder_created", false); err != nil {
		return err
	}
	if err := im.db.Set("goroku.inline", "bootstrapped_group", nil); err != nil {
		return err
	}
	return im.db.Set("goroku.inline", "last_bot_id", botID)
}

func (im *InlineManager) setInitComplete(complete bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.initComplete = complete
}

func (im *InlineManager) runtimeState() (*tgbotapi.BotAPI, string, int64) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.bot, im.BotUsername, im.BotID
}

func (im *InlineManager) request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return im.requestUnleased(c)
}

func (im *InlineManager) requestUnleased(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	bot := im.GetBotAPI()
	if bot == nil {
		return nil, fmt.Errorf("inline bot is not initialized")
	}
	return bot.RequestWithContext(im.generationContext(), c)
}

func (im *InlineManager) generationContext() context.Context {
	im.mu.RLock()
	generation := im.generation
	im.mu.RUnlock()
	if generation != nil {
		return generation.ctx
	}
	return context.Background()
}

func (im *InlineManager) startGenerationWorker(worker func(context.Context)) bool {
	im.mu.RLock()
	generation := im.generation
	im.mu.RUnlock()
	return generation != nil && generation.start(worker)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type userBotInlineBootstrap interface {
	SendMessage(chat chatref.ChatRef, message string) (chatref.SentMessage, error)
	CreateGorokuFolder(botID int64) error
	InviteBotToChannel(channelPeer tg.InputPeerClass) error
	PromoteBotToAdmin(channelPeer tg.InputPeerClass) error
}

func (im *InlineManager) bootstrapUserBotSide(afterBreak bool) error {
	_, err := im.bootstrapUserBotSideContext(im.generationContext(), afterBreak)
	return err
}

func (im *InlineManager) bootstrapUserBotSideContext(ctx context.Context, afterBreak bool) (bool, error) {
	_, botUsername, botID := im.runtimeState()
	return im.bootstrapUserBotSideFor(ctx, afterBreak, botUsername, botID)
}

func (im *InlineManager) bootstrapUserBotSideFor(ctx context.Context, afterBreak bool, botUsername string, botID int64) (bool, error) {
	client, ok := im.client.(userBotInlineBootstrap)
	if !ok || client == nil || botUsername == "" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	var bootstrappedGroup int64
	var bootstrappedChannel int64
	var folderCreated bool

	db := im.db
	if db != nil {
		val, err := db.Get("goroku.inline", "bootstrapped_group", nil)
		if err != nil {
			return false, fmt.Errorf("read goroku.inline.bootstrapped_group: %w", err)
		}
		if val != nil {
			switch v := val.(type) {
			case float64:
				bootstrappedGroup = int64(v)
			case int64:
				bootstrappedGroup = v
			case int:
				bootstrappedGroup = int64(v)
			}
		}
		val, err = db.Get("goroku.inline", "folder_created", false)
		if err != nil {
			return false, fmt.Errorf("read goroku.inline.folder_created: %w", err)
		}
		if val != nil {
			if b, ok := val.(bool); ok {
				folderCreated = b
			}
		}
		val, err = db.Get("goroku.forums", "channel_id", nil)
		if err != nil {
			return false, fmt.Errorf("read goroku.forums.channel_id: %w", err)
		}
		if val != nil {
			switch v := val.(type) {
			case float64:
				bootstrappedChannel = int64(v)
			case int64:
				bootstrappedChannel = v
			case int:
				bootstrappedChannel = int64(v)
			}
		}
	}

	if !folderCreated {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		msg, err := client.SendMessage(chatref.Username(botUsername), "/start goroku init")
		if err != nil {
			if db != nil && !afterBreak {
				if dbErr := db.Set("goroku.inline", "bot_token", nil); dbErr != nil {
					return false, dbErr
				}
				im.setToken("")
				L().Info("[Inline] Failed to start inline bot, token reset", zap.Error(err))
				return true, nil
			}
			return false, fmt.Errorf("failed to start inline bot @%s: %w", botUsername, err)
		}
		L().Info("[Inline] Inline bot @ initialized via userbot side", zap.Any("bot_username", botUsername), zap.Any("msg", msg))

		if err := client.CreateGorokuFolder(botID); err != nil {
			L().Info("[Inline] Failed to add inline bot to Goroku folder", zap.Error(err))
		} else {
			if db != nil {
				if err := db.Set("goroku.inline", "folder_created", true); err != nil {
					return false, err
				}
			}
		}
	}

	if db != nil {
		if bootstrappedChannel != 0 && bootstrappedChannel != bootstrappedGroup {
			channelPeer := &tg.InputPeerChannel{ChannelID: bootstrappedChannel}
			if err := client.InviteBotToChannel(channelPeer); err != nil {
				L().Info("[Inline] Failed to invite inline bot to log group", zap.Error(err))
			} else {
				L().Info("Successfully invited inline bot to log group")
			}
			if err := client.PromoteBotToAdmin(channelPeer); err != nil {
				L().Info("Failed to promote inline bot to admin", zap.Error(err))
			} else {
				L().Info("Successfully promoted inline bot to admin")
				if err := db.Set("goroku.inline", "bootstrapped_group", bootstrappedChannel); err != nil {
					return false, err
				}
			}
		}
	}
	return false, nil
}

// Close permanently stops intake and waits for the current generation. A
// caller timeout does not abandon shutdown; later callers observe the same
// completion and result.
func (im *InlineManager) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	im.closeOnce.Do(func() {
		im.mu.Lock()
		im.closed = true
		generation := im.generation
		im.initComplete = false
		im.bot = nil
		im.BotUsername = ""
		im.BotID = 0
		unloads := make([]func(), 0, len(im.units))
		for id := range im.units {
			if unload := im.removeUnitLocked(id); unload != nil {
				unloads = append(unloads, unload)
			}
		}
		im.units = make(map[string]*Unit)
		im.activeInlineMessages = make(map[string]string)
		im.activeMessageIDs = make(map[string]MessageIDInfo)
		im.customMap = make(map[string]Button)
		im.buttonUnits = make(map[string]string)
		im.QueryGalleries = make(map[string]QueryGalleryItem)
		im.webAuthTokens = nil
		im.fsm = make(map[string]string)
		im.errorEvents = make(map[string]chan error)
		if im.lifecycleCancel != nil {
			im.lifecycleCancel()
		}
		im.mu.Unlock()
		// Registration publishes its generation before token discovery. Cancel it
		// before waiting on registerMu so a blocked startup call can release the
		// registration transaction.
		if generation != nil {
			generation.stop()
		}
		go func() {
			// Registration owns registerMu for its entire startup. Waiting for it
			// here closes the race where Close could otherwise miss a generation
			// that registration had begun but had not published yet.
			im.registerMu.Lock()
			im.mu.RLock()
			currentGeneration := im.generation
			im.mu.RUnlock()
			if currentGeneration != nil {
				currentGeneration.stop()
				<-currentGeneration.done
			}
			im.registerMu.Unlock()
			runUnloads(unloads, defaultUnloadWorkers)
			close(im.closeDone)
		}()
	})
	select {
	case <-im.closeDone:
		return im.closeErr
	default:
	}
	select {
	case <-im.closeDone:
		return im.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runUnloads(unloads []func(), workers int) {
	if len(unloads) == 0 {
		return
	}
	if workers > len(unloads) {
		workers = len(unloads)
	}
	jobs := make(chan func())
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for unload := range jobs {
				func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							L().Warn("[Inline] OnUnload panic", zap.Any("panic", recovered))
						}
					}()
					unload()
				}()
			}
		}()
	}
	for _, unload := range unloads {
		jobs <- unload
	}
	close(jobs)
	wg.Wait()
}

var errInlineClosed = errors.New("inline manager is closed")

// claimIntake pins the current generation for a synchronous public operation.
// External APIs without context support remain bounded by Close's retryable
// wait: Close may time out, but dependencies are not safe to tear down until
// this lease is eventually released.
func (im *InlineManager) claimIntake() (*inlineGeneration, context.Context, error) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.closed {
		return nil, nil, errInlineClosed
	}
	generation := im.generation
	if generation == nil {
		generation = im.newGeneration()
		im.generation = generation
	}
	if !generation.acquire() {
		return nil, nil, errInlineClosed
	}
	return generation, generation.ctx, nil
}

func (im *InlineManager) tokenValue() string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.token
}

func (im *InlineManager) setToken(token string) uint64 {
	im.mu.Lock()
	im.token = token
	im.tokenRevision++
	revision := im.tokenRevision
	im.mu.Unlock()
	return revision
}

func (im *InlineManager) tokenRevisionValue() uint64 {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.tokenRevision
}

func (im *InlineManager) restartAfter(generation *inlineGeneration, revision uint64) {
	go func() {
		im.restartMu.Lock()
		defer im.restartMu.Unlock()
		generation.stop()
		<-generation.done
		im.mu.Lock()
		if im.closed || im.generation != generation || im.tokenRevision != revision {
			im.mu.Unlock()
			return
		}
		im.initComplete = false
		im.bot = nil
		im.BotUsername = ""
		im.BotID = 0
		im.mu.Unlock()
		if err := im.RegisterManager(false, true); err != nil && !errors.Is(err, errInlineClosed) {
			L().Error("[Inline] controlled restart failed", zap.Error(err))
		}
	}()
}

func (im *InlineManager) stopGeneration(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	im.mu.RLock()
	generation := im.generation
	im.mu.RUnlock()
	if generation == nil {
		return nil
	}
	generation.stop()
	select {
	case <-generation.done:
		im.mu.Lock()
		if im.generation == generation {
			im.initComplete = false
			im.bot = nil
			im.BotUsername = ""
			im.BotID = 0
		}
		im.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (im *InlineManager) IsComplete() bool {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.initComplete
}

func (im *InlineManager) GetBotAPI() *tgbotapi.BotAPI {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.bot
}

func (im *InlineManager) PopWebAuthToken(token string) bool {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.closed {
		return false
	}

	for i, t := range im.webAuthTokens {
		if t == token {
			im.webAuthTokens = append(im.webAuthTokens[:i], im.webAuthTokens[i+1:]...)
			return true
		}
	}
	return false
}

func (im *InlineManager) getToken() (string, error) {
	if im.db == nil {
		return "", nil
	}
	raw, err := im.db.Get("goroku.inline", "bot_token", "")
	if err != nil {
		return "", fmt.Errorf("read goroku.inline.bot_token: %w", err)
	}
	if tok, ok := raw.(string); ok {
		return tok, nil
	}
	return "", nil
}

func (im *InlineManager) startPolling(ctx context.Context, bot inlineBot, generation *inlineGeneration) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	// Explicitly request chosen_inline_result — without this Telegram
	// does NOT deliver ChosenInlineResult updates, causing the 10-second
	// "timeout waiting for inline message selection" error.
	u.AllowedUpdates = []string{
		"message",
		"inline_query",
		"chosen_inline_result",
		"callback_query",
	}
	updates := bot.Updates(u)

	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			if !generation.submit(generation.updateJobs, func(ctx context.Context) { im.handleUpdate(ctx, update) }) {
				L().Warn("[Inline] update queue full; dropping update", zap.Int("update_id", update.UpdateID))
			}
		case <-ctx.Done():
			generation.stopPolling(bot)
			return
		}
	}
}

func (im *InlineManager) ttlCleaner(ctx context.Context) {
	ticker := time.NewTicker(im.cleanerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			im.mu.Lock()
			now := time.Now()
			var unloads []func()
			for id, unit := range im.units {
				if !unit.TTL.IsZero() && now.After(unit.TTL) {
					if unload := im.removeUnitLocked(id); unload != nil {
						unloads = append(unloads, unload)
					}
				}
			}
			im.mu.Unlock()
			for _, unload := range unloads {
				im.runUnitUnload(unload)
			}
		case <-ctx.Done():
			return
		}
	}
}

type TelegramClient interface {
	InlineQuery(botUsername string, query string, chatID int64) (*tg.MessagesBotResults, error)
	SendInlineBotResult(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error)
}

func getSentMessageID(resp any) int64 {
	switch v := resp.(type) {
	case *tg.Updates:
		for _, update := range v.Updates {
			if u, ok := update.(*tg.UpdateNewMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateEditMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateEditChannelMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range v.Updates {
			if u, ok := update.(*tg.UpdateNewMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return int64(v.ID)
	case *tg.UpdateShortMessage:
		return int64(v.ID)
	case *tg.UpdateShortChatMessage:
		return int64(v.ID)
	case *tg.UpdateShort:
		if u, ok := v.Update.(*tg.UpdateNewMessage); ok {
			if msg, ok := u.Message.(*tg.Message); ok {
				return int64(msg.ID)
			}
		} else if u, ok := v.Update.(*tg.UpdateNewChannelMessage); ok {
			if msg, ok := u.Message.(*tg.Message); ok {
				return int64(msg.ID)
			}
		}
	}
	return 0
}

func (im *InlineManager) InvokeUnit(unitID string, chatID int64, replyToMsgID int64) error {
	generation, ctx, claimErr := im.claimIntake()
	if claimErr != nil {
		return claimErr
	}
	defer generation.release()
	client, ok := im.client.(TelegramClient)
	if !ok {
		return fmt.Errorf("client does not implement TelegramClient interface")
	}

	im.mu.Lock()
	if im.errorEvents == nil {
		im.errorEvents = make(map[string]chan error)
	}
	errCh := make(chan error, 1)
	im.errorEvents[unitID] = errCh
	im.mu.Unlock()

	defer func() {
		im.mu.Lock()
		delete(im.errorEvents, unitID)
		im.mu.Unlock()
	}()

	results, err := client.InlineQuery(im.BotUsernameStr(), unitID, chatID)
	if err != nil {
		return err
	}

	var queryID int64
	var resultID string
	var found bool

	queryID = results.QueryID
	if len(results.Results) > 0 {
		resultID = results.Results[0].GetID()
		found = true
	}

	if !found {
		return fmt.Errorf("no query results returned")
	}

	updates, err := client.SendInlineBotResult(chatID, queryID, resultID, replyToMsgID)
	if err != nil {
		return err
	}

	msgID := getSentMessageID(updates)
	if msgID != 0 {
		im.mu.Lock()
		im.activeMessageIDs[unitID] = MessageIDInfo{
			ChatID:    chatID,
			MessageID: msgID,
		}
		im.mu.Unlock()
	}

	// Wait for ChosenInlineResult or timeout.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-timer.C:
		return fmt.Errorf("timeout waiting for inline message selection")
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (im *InlineManager) GetButton(data string) (Button, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	btn, ok := im.customMap[data]
	return btn, ok
}
