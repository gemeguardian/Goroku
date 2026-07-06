package inline

import (
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
	mu                   sync.RWMutex
	registerMu           sync.Mutex
	bot                  *tgbotapi.BotAPI
	client               InlineUserBot
	db                   Database
	allModules           InlineModules
	units                map[string]*Unit
	activeInlineMessages map[string]string
	activeMessageIDs     map[string]MessageIDInfo
	customMap            map[string]Button
	buttonUnits          map[string]string
	QueryGalleries       map[string]QueryGalleryItem
	webAuthTokens        []string
	fsm                  map[string]string
	errorEvents          map[string]chan error
	initComplete         bool
	token                string
	BotUsername          string
	BotID                int64
	stopCh               chan struct{}
	markupTTL            time.Duration
}

func NewInlineManager(client InlineUserBot, db Database, allModules InlineModules) *InlineManager {
	im := &InlineManager{
		client:               client,
		db:                   db,
		allModules:           allModules,
		units:                make(map[string]*Unit),
		activeInlineMessages: make(map[string]string),
		activeMessageIDs:     make(map[string]MessageIDInfo),
		customMap:            make(map[string]Button),
		buttonUnits:          make(map[string]string),
		QueryGalleries:       make(map[string]QueryGalleryItem),
		webAuthTokens:        make([]string, 0),
		fsm:                  make(map[string]string),
		errorEvents:          make(map[string]chan error),
		stopCh:               make(chan struct{}),
		markupTTL:            24 * time.Hour,
	}
	im.token = im.getToken()
	return im
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

	im.mu.RLock()
	if im.initComplete && im.bot != nil {
		im.mu.RUnlock()
		return nil
	}
	im.mu.RUnlock()

	token := im.token
	if token == "" {
		token = im.getToken()
	}
	if token == "" {
		if !ignoreTokenChecks {
			ok, err := im.AssertToken(true, false)
			if err != nil || !ok {
				im.setInitComplete(false)
				return fmt.Errorf("failed to assert bot token: %v", err)
			}
			token = im.getToken()
		}
		if token == "" {
			return fmt.Errorf("no inline bot token configured")
		}
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		if ignoreTokenChecks {
			return fmt.Errorf("failed to init inline bot: %w", err)
		}
		ok, assertErr := im.AssertToken(true, false)
		if assertErr != nil || !ok {
			im.setInitComplete(false)
			return fmt.Errorf("failed to assert bot token after bot api error %v: %v", err, assertErr)
		}
		token = im.getToken()
		bot, err = tgbotapi.NewBotAPI(token)
		if err != nil {
			return fmt.Errorf("failed to init inline bot: %w", err)
		}
	}

	im.mu.Lock()
	im.bot = bot
	im.BotUsername = bot.Self.UserName
	im.BotID = bot.Self.ID
	im.stopCh = make(chan struct{})
	im.initComplete = true
	botUsername := im.BotUsername
	im.mu.Unlock()

	if dbTyped, ok := im.db.(interface {
		Get(string, string, any) (any, error)
		Set(string, string, any) bool
	}); ok {
		var lastBotID int64
		if val, _ := dbTyped.Get("goroku.inline", "last_bot_id", nil); val != nil {
			switch v := val.(type) {
			case float64:
				lastBotID = int64(v)
			case int64:
				lastBotID = v
			case int:
				lastBotID = int64(v)
			}
		}

		if lastBotID != bot.Self.ID {
			L().Info("[Inline] Inline bot ID changed from {0} to {1} (or first run). Resetting bootstrap flags.", zap.Any("arg0", lastBotID), zap.Any("arg1", bot.Self.ID))
			dbTyped.Set("goroku.inline", "folder_created", false)
			dbTyped.Set("goroku.inline", "bootstrapped_group", nil)
			dbTyped.Set("goroku.inline", "last_bot_id", bot.Self.ID)
		}
	}

	if err := im.bootstrapUserBotSide(afterBreak); err != nil {
		im.setInitComplete(false)
		return err
	}

	go im.startPolling()
	go im.ttlCleaner()

	L().Info("InlineManager started: @{0}", zap.Any("arg0", botUsername))
	return nil
}

func (im *InlineManager) setInitComplete(complete bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.initComplete = complete
}

func (im *InlineManager) runtimeState() (*tgbotapi.BotAPI, chan struct{}, string, int64) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.bot, im.stopCh, im.BotUsername, im.BotID
}

func (im *InlineManager) request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	bot := im.GetBotAPI()
	if bot == nil {
		return nil, fmt.Errorf("inline bot is not initialized")
	}
	return bot.Request(c)
}

type userBotInlineBootstrap interface {
	SendMessage(chat chatref.ChatRef, message string) (any, error)
	CreateGorokuFolder(botID int64) error
	InviteBotToChannel(channelPeer tg.InputPeerClass) error
	PromoteBotToAdmin(channelPeer tg.InputPeerClass) error
}

func (im *InlineManager) bootstrapUserBotSide(afterBreak bool) error {
	client, ok := im.client.(userBotInlineBootstrap)
	_, _, botUsername, botID := im.runtimeState()
	if !ok || client == nil || botUsername == "" {
		return nil
	}

	var bootstrappedGroup int64
	var folderCreated bool

	dbTyped, okDb := im.db.(interface {
		Get(string, string, any) (any, error)
		Set(string, string, any) bool
	})

	if okDb {
		if val, _ := dbTyped.Get("goroku.inline", "bootstrapped_group", nil); val != nil {
			switch v := val.(type) {
			case float64:
				bootstrappedGroup = int64(v)
			case int64:
				bootstrappedGroup = v
			case int:
				bootstrappedGroup = int64(v)
			}
		}
		if val, _ := dbTyped.Get("goroku.inline", "folder_created", false); val != nil {
			if b, ok := val.(bool); ok {
				folderCreated = b
			}
		}
	}

	if !folderCreated {
		msg, err := client.SendMessage(chatref.Username(botUsername), "/start goroku init")
		if err != nil {
			if okDb && !afterBreak {
				dbTyped.Set("goroku.inline", "bot_token", nil)
				im.token = ""
				L().Info("[Inline] Failed to start inline bot, token reset: {0}", zap.Any("arg0", err))
				return im.RegisterManager(true, false)
			}
			return fmt.Errorf("failed to start inline bot @%s: %w", botUsername, err)
		}
		L().Info("[Inline] Inline bot @{0} initialized via userbot side: {1}", zap.Any("arg0", botUsername), zap.Any("arg1", msg))

		if err := client.CreateGorokuFolder(botID); err != nil {
			L().Info("[Inline] Failed to add inline bot to Goroku folder: {0}", zap.Any("arg0", err))
		} else {
			if okDb {
				dbTyped.Set("goroku.inline", "folder_created", true)
			}
		}
	}

	if okDb {
		if val, _ := dbTyped.Get("goroku.forums", "channel_id", nil); val != nil {
			var cid int64
			switch v := val.(type) {
			case float64:
				cid = int64(v)
			case int64:
				cid = v
			case int:
				cid = int64(v)
			}

			if cid != 0 && cid != bootstrappedGroup {
				channelPeer := &tg.InputPeerChannel{ChannelID: cid}
				if err := client.InviteBotToChannel(channelPeer); err != nil {
					L().Info("[Inline] Failed to invite inline bot to log group: {0}", zap.Any("arg0", err))
				} else {
					L().Info("Successfully invited inline bot to log group")
				}
				if err := client.PromoteBotToAdmin(channelPeer); err != nil {
					L().Info("Failed to promote inline bot to admin", zap.Error(err))
				} else {
					L().Info("Successfully promoted inline bot to admin")
					dbTyped.Set("goroku.inline", "bootstrapped_group", cid)
				}
			}
		}
	}
	return nil
}

func (im *InlineManager) Stop() {
	defer func() { _ = recover() }()
	bot, stopCh, _, _ := im.runtimeState()
	if stopCh != nil {
		close(stopCh)
	}
	if bot != nil {
		bot.StopReceivingUpdates()
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

	for i, t := range im.webAuthTokens {
		if t == token {
			im.webAuthTokens = append(im.webAuthTokens[:i], im.webAuthTokens[i+1:]...)
			return true
		}
	}
	return false
}

func (im *InlineManager) getToken() string {
	// Read bot token from DB interface
	if dbTyped, ok := im.db.(interface {
		Get(string, string, any) (any, error)
	}); ok {
		if raw, _ := dbTyped.Get("goroku.inline", "bot_token", ""); raw != nil {
			if tok, ok := raw.(string); ok {
				return tok
			}
		}
	}
	return ""
}

func (im *InlineManager) startPolling() {
	bot, stopCh, _, _ := im.runtimeState()
	if bot == nil || stopCh == nil {
		return
	}
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
	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			go im.HandleUpdate(update)
		case <-stopCh:
			bot.StopReceivingUpdates()
			return
		}
	}
}

func (im *InlineManager) ttlCleaner() {
	_, stopCh, _, _ := im.runtimeState()
	if stopCh == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			im.mu.Lock()
			now := time.Now()
			for id, unit := range im.units {
				if !unit.TTL.IsZero() && now.After(unit.TTL) {
					im.removeUnitLocked(id)
				}
			}
			im.mu.Unlock()
		case <-stopCh:
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

	// Wait for ChosenInlineResult or timeout
	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for inline message selection")
	}

	return nil
}

func (im *InlineManager) GetButton(data string) (Button, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	btn, ok := im.customMap[data]
	return btn, ok
}
