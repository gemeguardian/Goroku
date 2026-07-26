package inline

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"go.uber.org/zap"

	"goroku/goroku/logger"
)

func L() *zap.Logger { return logger.L() }

func (im *InlineManager) HandleUpdate(update tgbotapi.Update) {
	generation, ctx, err := im.claimIntake()
	if err != nil {
		return
	}
	defer generation.release()
	im.handleUpdate(ctx, update)
}

func (im *InlineManager) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	if ctx.Err() != nil {
		return
	}
	L().Debug("HandleUpdate", zap.Int("ID", update.UpdateID), zap.Bool("InlineQuery", update.InlineQuery != nil), zap.Bool("CallbackQuery", update.CallbackQuery != nil), zap.Bool("ChosenInlineResult", update.ChosenInlineResult != nil))
	if update.InlineQuery != nil {
		im.handleInlineQuery(update.InlineQuery)
	} else if update.CallbackQuery != nil {
		im.handleCallbackQuery(ctx, update.CallbackQuery)
	} else if update.Message != nil {
		im.handleBotMessage(update.Message)
	} else if update.ChosenInlineResult != nil {
		im.handleChosenInlineResult(ctx, update.ChosenInlineResult)
	}
}

func (im *InlineManager) isUserAuthorizedForInline(userID int64) (bool, error) {
	if userID == im.ownerID() {
		return true, nil
	}
	allowInline := false
	if im.db != nil {
		raw, err := im.db.Get("goroku.security", "allow_inline_query", false)
		if err != nil {
			return false, fmt.Errorf("read goroku.security.allow_inline_query: %w", err)
		}
		if rawBool, ok := raw.(bool); ok {
			allowInline = rawBool
		}
	}
	if allowInline {
		return true, nil
	}
	if im.client != nil {
		if sm := im.client.GetSecurityManager(); sm != nil && sm.IsOwner(userID) {
			return true, nil
		}
	}
	return false, nil
}

func (im *InlineManager) handleInlineQuery(q *tgbotapi.InlineQuery) {
	authorized, err := im.isUserAuthorizedForInline(q.From.ID)
	if err != nil {
		L().Warn("[Inline] Failed to authorize inline query", zap.Int64("user_id", q.From.ID), zap.Error(err))
		return
	}
	if !authorized {
		return
	}
	if strings.TrimSpace(q.Query) == "" {
		im.answerInlineHelp(q)
		return
	}

	parts := strings.SplitN(q.Query, " ", 2)
	var switchQuery string
	if len(parts) > 0 {
		switchQuery = strings.ToLower(parts[0])
	}

	if im.handleModuleInlineQuery(q, switchQuery, parts) {
		return
	}

	im.mu.RLock()
	btn, isInputBtn := im.customMap[switchQuery]
	im.mu.RUnlock()

	if isInputBtn && btn.Input != "" {
		article := tgbotapi.NewInlineQueryResultArticle(localRandStr(20), btn.Input, "🔄 Transferring value to userbot...")
		article.Description = "Press to submit input value"
		article.InputMessageContent = tgbotapi.InputTextMessageContent{
			Text:      "🔄 <b>Transferring value to userbot...</b>\n<i>This message will be deleted automatically</i>",
			ParseMode: tgbotapi.ModeHTML,
		}

		inlineConf := tgbotapi.InlineConfig{
			InlineQueryID: q.ID,
			Results:       []any{article},
			CacheTime:     0,
			IsPersonal:    true,
		}
		_, err := im.request(inlineConf)
		if err != nil {
			L().Info("[Inline] Failed to answer input inline query", zap.Error(err))
		}
		return
	}

	unitID := q.Query
	im.mu.Lock()
	unit, exists := im.units[unitID]
	im.mu.Unlock()

	if !exists {
		L().Info("[Inline] Unit not found for query", zap.Any("unit_id", unitID))
		return
	}

	var result any
	markup := im.generateMarkup(unit.Buttons)

	switch {
	case unit.Photo != "" && unit.Type == "form":
		photo := tgbotapi.NewInlineQueryResultPhoto(unitID, unit.Photo)
		photo.Caption = unit.Text
		photo.ParseMode = tgbotapi.ModeHTML
		photo.ReplyMarkup = &markup
		photo.ThumbURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/moon-satellite.png"
		result = photo
	case unit.GifURL != "":
		gif := tgbotapi.NewInlineQueryResultGIF(unitID, unit.GifURL)
		gif.Caption = unit.Text
		gif.ParseMode = tgbotapi.ModeHTML
		gif.ReplyMarkup = &markup
		gif.ThumbURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/moon-satellite.png"
		result = gif
	case unit.Video != "":
		video := tgbotapi.NewInlineQueryResultVideo(unitID, unit.Video)
		video.Caption = unit.Text
		video.ReplyMarkup = &markup
		video.ThumbURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/moon-satellite.png"
		video.MimeType = "video/mp4"
		result = video
	case unit.File != "":
		doc := tgbotapi.NewInlineQueryResultDocument(unitID, unit.File, "Document", unit.MimeType)
		doc.Caption = unit.Text
		doc.ReplyMarkup = &markup
		doc.ThumbURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/moon-satellite.png"
		result = doc
	case len(unit.Location) == 2:
		loc := tgbotapi.NewInlineQueryResultLocation(unitID, "Location", unit.Location[0], unit.Location[1])
		loc.ReplyMarkup = &markup
		result = loc
	case unit.Audio != nil:
		var audioURL string
		var title string = "Audio"
		var performer string
		var duration int

		if m, ok := unit.Audio.(map[string]any); ok {
			if u, ok := m["url"].(string); ok {
				audioURL = u
			}
			if t, ok := m["title"].(string); ok {
				title = t
			}
			if p, ok := m["performer"].(string); ok {
				performer = p
			}
			if d, ok := m["duration"].(int); ok {
				duration = d
			}
		} else if s, ok := unit.Audio.(string); ok {
			audioURL = s
		}

		audio := tgbotapi.NewInlineQueryResultAudio(unitID, audioURL, title)
		audio.Caption = unit.Text
		audio.ParseMode = tgbotapi.ModeHTML
		audio.Performer = performer
		audio.Duration = duration
		audio.ReplyMarkup = &markup
		result = audio
	case unit.Type == "gallery":
		// Check for gif/video first
		isGif := strings.HasSuffix(strings.ToLower(unit.Photo), ".gif") || strings.HasSuffix(strings.ToLower(unit.Photo), ".mp4") || unit.Gif
		if isGif {
			gif := tgbotapi.NewInlineQueryResultGIF(unitID, unit.Photo)
			gif.Caption = unit.Text
			gif.ParseMode = tgbotapi.ModeHTML
			gif.ReplyMarkup = &markup
			gif.ThumbURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/moon-satellite.png"
			result = gif
		} else {
			photo := tgbotapi.NewInlineQueryResultPhoto(unitID, unit.Photo)
			photo.Caption = unit.Text
			photo.ParseMode = tgbotapi.ModeHTML
			photo.ReplyMarkup = &markup
			photo.ThumbURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/moon-satellite.png"
			result = photo
		}
	default:
		text := unit.Text
		if unit.StartText != "" {
			text = unit.StartText
		}
		article := tgbotapi.NewInlineQueryResultArticle(unitID, unit.Type, text)
		article.Description = "Goroku Userbot inline result"
		article.ReplyMarkup = &markup
		article.InputMessageContent = tgbotapi.InputTextMessageContent{
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
		result = article
	}

	inlineConf := tgbotapi.InlineConfig{
		InlineQueryID: q.ID,
		Results:       []any{result},
		CacheTime:     0,
		IsPersonal:    true,
	}

	_, err = im.request(inlineConf)
	if err != nil {
		L().Info("[Inline] Failed to answer inline query", zap.Error(err))
	}
}

func (im *InlineManager) handleCallbackQuery(ctx context.Context, c *tgbotapi.CallbackQuery) {
	if strings.HasPrefix(c.Data, "authorize_web_") {
		token := strings.TrimPrefix(c.Data, "authorize_web_")
		im.mu.Lock()
		im.webAuthTokens = append(im.webAuthTokens, token)
		im.mu.Unlock()
		_, _ = im.request(tgbotapi.CallbackConfig{
			CallbackQueryID: c.ID,
			Text:            "Web authorization approved",
		})
		return
	}

	cb := CallbackQuery{
		ID:      c.ID,
		FromID:  c.From.ID,
		Data:    c.Data,
		Manager: im,
		leased:  true,
	}

	if c.Message != nil {
		cb.ChatID = c.Message.Chat.ID
		cb.MessageID = int64(c.Message.MessageID)
		cb.BotMessage = NewBotInlineMessage(im, "", cb.ChatID, cb.MessageID)
	}

	if c.InlineMessageID != "" {
		cb.InlineMessage = NewInlineMessage(im, "", c.InlineMessageID)
	}

	// Resolve the unit and check security first, before running any callbacks or handlers
	im.mu.RLock()
	_, exists := im.customMap[c.Data]
	unitID := im.buttonUnits[c.Data]
	if unitID == "" {
		parts := strings.Split(c.Data, "_")
		if len(parts) >= 2 && (parts[0] == "gal" || parts[0] == "lst") {
			unitID = parts[1]
		}
	}
	unit := im.units[unitID]
	if unit == nil {
		unit = im.findUnitByButtonDataLocked(c.Data)
		if unit != nil {
			unitID = unit.ID
		}
	}
	im.mu.RUnlock()

	if cb.InlineMessage != nil {
		cb.InlineMessage.UnitID = unitID
	}
	if cb.BotMessage != nil {
		cb.BotMessage.UnitID = unitID
	}

	if unit != nil && !im.isCallbackAllowed(unit, c.From.ID) {
		_ = cb.Answer("You are not allowed to press this button", true)
		return
	}

	im.dispatchModuleCallbacks(ctx, cb)

	handledComponent := false
	if unit != nil && unit.Module != "" {
		if !im.withModule(unit.Module, func(any) {
			handledComponent = im.HandleGalleryCallback(cb) || im.HandleListCallback(cb)
		}) {
			_ = cb.Answer("This module is no longer available", true)
			return
		}
	} else {
		handledComponent = im.HandleGalleryCallback(cb) || im.HandleListCallback(cb)
	}
	if handledComponent {
		return
	}

	if !exists {
		callbackConfig := tgbotapi.CallbackConfig{
			CallbackQueryID: c.ID,
		}
		_, _ = im.request(callbackConfig)
		return
	}

	run := func() {
		im.mu.RLock()
		btn, stillExists := im.customMap[c.Data]
		im.mu.RUnlock()
		if !stillExists || btn.Handler == nil {
			_ = cb.Answer("This action is no longer available", true)
			return
		}
		defer func() {
			if r := recover(); r != nil {
				L().Error("[Inline] Callback panic", zap.Any("panic", r))
			}
		}()
		err := btn.Handler(cb)
		if err != nil {
			L().Info("[Inline] Callback handler error", zap.Error(err))
			_ = cb.Answer(fmt.Sprintf("Error: %v", err), true)
		}
	}
	if unit != nil && unit.Module != "" {
		moduleName := unit.Module
		im.startHandler(ctx, func(context.Context) {
			if !im.withModule(moduleName, func(any) { run() }) {
				_ = cb.Answer("This module is no longer available", true)
			}
		})
		return
	}
	im.startHandler(ctx, func(context.Context) { run() })
}

func (im *InlineManager) handleModuleInlineQuery(q *tgbotapi.InlineQuery, cmd string, parts []string) bool {
	for _, name := range im.moduleNames() {
		handled := false
		im.withModule(name, func(value any) {
			mod, ok := value.(ModuleInlineHandlers)
			if !ok {
				return
			}
			handlers := mod.InlineHandlers()
			handler, ok := handlers[cmd]
			if !ok {
				handler, ok = handlers[strings.ToLower(cmd)]
			}
			if !ok || handler == nil {
				return
			}
			handled = true
			args := ""
			if len(parts) > 1 {
				args = parts[1]
			}
			query := &InlineQuery{QueryID: q.ID, Query: q.Query, Args: args, FromID: q.From.ID, Manager: im, leased: true}
			results, err := handler(query)
			if err != nil {
				L().Info("[Inline] module inline handler failed", zap.Any("handler", cmd), zap.Error(err))
				_ = query.E500()
				return
			}
			if len(results) > 0 {
				if err := query.answerResults(results, 0); err != nil {
					L().Info("[Inline] failed to answer module inline query", zap.Any("query", cmd), zap.Error(err))
				}
			}
		})
		if handled {
			return true
		}
	}
	return false
}

func (im *InlineManager) answerInlineHelp(q *tgbotapi.InlineQuery) {
	var text strings.Builder
	for _, name := range im.moduleNames() {
		im.withModule(name, func(value any) {
			mod, ok := value.(ModuleInlineHandlers)
			if !ok {
				return
			}
			name := "Inline"
			if named, ok := mod.(interface{ Name() string }); ok {
				name = named.Name()
			}
			help := map[string]string{}
			if withHelp, ok := mod.(ModuleInlineHelp); ok {
				help = withHelp.InlineHelp()
			}
			for cmd := range mod.InlineHandlers() {
				desc := help[cmd]
				if desc == "" {
					desc = "No description"
				}
				text.WriteString(fmt.Sprintf("• <code>@%s %s</code> — <b>%s</b>\n", im.BotUsernameStr(), cmd, desc))
				_ = name
			}
		})
	}
	if text.Len() == 0 {
		return
	}
	article := tgbotapi.NewInlineQueryResultArticle(localRandStr(20), "Goroku inline commands", text.String())
	article.Description = "Available inline commands"
	article.InputMessageContent = tgbotapi.InputTextMessageContent{Text: text.String(), ParseMode: tgbotapi.ModeHTML}
	_, err := im.request(tgbotapi.InlineConfig{InlineQueryID: q.ID, Results: []any{article}, CacheTime: 0, IsPersonal: true})
	if err != nil {
		L().Info("[Inline] failed to answer inline help", zap.Error(err))
	}
}

func (im *InlineManager) dispatchModuleCallbacks(ctx context.Context, cb CallbackQuery) {
	for _, registryName := range im.moduleNames() {
		registryName := registryName
		im.startHandler(ctx, func(context.Context) {
			if !im.withModule(registryName, func(value any) {
				mod, ok := value.(ModuleCallbackHandlers)
				if !ok {
					return
				}
				modName := ""
				if named, ok := mod.(interface{ Name() string }); ok {
					modName = named.Name()
				}
				// Security check: only allow owners or those who have trust on this module
				if sm := im.getSecurityManager(); sm != nil {
					if !im.isUserOwnerOrTrustedForModule(sm, cb.FromID, modName) {
						return
					}
				} else {
					// fallback if security manager is not available: only owner
					if cb.FromID != im.ownerID() {
						return
					}
				}

				for _, handler := range mod.CallbackHandlers() {
					if handler == nil {
						continue
					}
					defer func() {
						if r := recover(); r != nil {
							L().Error("[Inline] module callback panic", zap.Any("panic", r))
						}
					}()
					if err := handler(cb); err != nil {
						L().Info("[Inline] module callback handler failed", zap.Error(err))
					}
				}
			}) {
				return
			}
		})
	}
}

func (im *InlineManager) startHandler(ctx context.Context, handler func(context.Context)) {
	im.mu.RLock()
	generation := im.generation
	im.mu.RUnlock()
	if generation != nil && generation.ctx == ctx {
		if !generation.submit(generation.callbackJobs, handler) {
			L().Warn("[Inline] callback queue full; dropping callback")
		}
		return
	}
	// Direct HandleUpdate calls are retained for embedders and unit tests. They
	// are synchronous when no managed generation exists, so no worker can leak.
	handler(ctx)
}

func (im *InlineManager) inlineModules() []ModuleInlineHandlers {
	modules := make([]ModuleInlineHandlers, 0)
	for _, name := range im.moduleNames() {
		name := name
		isInline := false
		im.withModule(name, func(mod any) { _, isInline = mod.(ModuleInlineHandlers) })
		if isInline {
			modules = append(modules, leasedInlineModule{manager: im, name: name})
		}
	}
	return modules
}

type leasedInlineModule struct {
	manager *InlineManager
	name    string
}

func (m leasedInlineModule) InlineHandlers() map[string]InlineHandler {
	handlers := make(map[string]InlineHandler)
	m.manager.withModule(m.name, func(value any) {
		module, ok := value.(ModuleInlineHandlers)
		if !ok {
			return
		}
		for command := range module.InlineHandlers() {
			command := command
			handlers[command] = func(query *InlineQuery) ([]InlineResult, error) {
				var results []InlineResult
				var handlerErr error
				if !m.manager.withModule(m.name, func(current any) {
					currentModule, ok := current.(ModuleInlineHandlers)
					if !ok {
						handlerErr = fmt.Errorf("inline module %s is no longer available", m.name)
						return
					}
					handler := currentModule.InlineHandlers()[command]
					if handler == nil {
						handlerErr = fmt.Errorf("inline handler %s is no longer available", command)
						return
					}
					results, handlerErr = handler(query)
				}) {
					return nil, fmt.Errorf("inline module %s is no longer available", m.name)
				}
				return results, handlerErr
			}
		}
	})
	return handlers
}

func (im *InlineManager) InlineModules() []ModuleInlineHandlers {
	return im.inlineModules()
}

func (im *InlineManager) moduleNames() []string {
	if im.allModules == nil {
		return nil
	}
	return im.allModules.ModuleNames()
}

func (im *InlineManager) withModule(name string, fn func(any)) bool {
	return im.allModules != nil && im.allModules.WithModule(name, fn)
}

func (im *InlineManager) findUnitByButtonDataLocked(data string) *Unit {
	for _, unit := range im.units {
		for _, row := range unit.Buttons {
			for _, button := range row {
				if button.Data == data {
					return unit
				}
			}
		}
	}
	return nil
}

func (im *InlineManager) isCallbackAllowed(unit *Unit, userID int64) bool {
	L().Debug("isCallbackAllowed", zap.Int64("user_id", userID), zap.String("module", unit.Module), zap.Bool("disable_security", unit.DisableSecurity), zap.Bool("force_me", unit.ForceMe))

	if unit.DisableSecurity {
		L().Info("[SecurityDebug] Allow click: security is disabled for this unit.")
		return true
	}
	for _, allowed := range unit.AlwaysAllow {
		if allowed == userID {
			L().Info("[SecurityDebug] Allow click: userID is in AlwaysAllow list.", zap.Any("user_id", userID))
			return true
		}
	}
	if unit.ForceMe {
		res := userID == im.ownerID()
		L().Info("[SecurityDebug] ForceMe check: allowed (userID, ownerID)", zap.Any("allowed", res), zap.Any("user_id", userID), zap.Any("owner_id", im.ownerID()))
		return res
	}

	// Default security check
	if userID == im.ownerID() {
		L().Info("[SecurityDebug] Allow click: userID matches ownerID.", zap.Any("user_id", userID), zap.Any("owner_id", im.ownerID()))
		return true
	}

	if sm := im.getSecurityManager(); sm != nil {
		// Check owner first using SecurityManager
		if sm.IsOwner(userID) {
			L().Info("[SecurityDebug] Allow click: userID is verified owner by SecurityManager.", zap.Any("user_id", userID))
			return true
		}

		// Check module trust
		if unit.Module != "" {
			res := im.isUserOwnerOrTrustedForModule(sm, userID, unit.Module)
			L().Info("[SecurityDebug] Module trust check: userID, module, allowed", zap.Any("user_id", userID), zap.Any("module", unit.Module), zap.Any("allowed", res))
			return res
		}
		L().Info("[SecurityDebug] unit.Module is empty!")
	} else {
		L().Info("[SecurityDebug] SecurityManager is not available!")
	}

	L().Info("[SecurityDebug] Deny click: userID has no permission for module.", zap.Any("user_id", userID), zap.Any("module", unit.Module))
	return false
}

func (im *InlineManager) getSecurityManager() SecurityChecker {
	if im.client == nil {
		return nil
	}
	return im.client.GetSecurityManager()
}

func (im *InlineManager) isUserOwnerOrTrustedForModule(sm SecurityChecker, userID int64, moduleName string) bool {
	if userID == im.ownerID() {
		return true
	}
	if sm == nil {
		return false
	}
	if sm.IsOwner(userID) {
		return true
	}
	// A module-scoped grant on a privileged module would let a delegate press
	// the confirmation buttons of the very commands that hand out owner rights,
	// so those buttons stay owner-only no matter what CheckModuleAccess says.
	if sm.IsPrivilegedModule(moduleName) {
		return false
	}
	if sm.CheckModuleAccess(userID, moduleName) {
		return true
	}
	if modTrim := strings.TrimPrefix(moduleName, "Goroku"); modTrim != moduleName && sm.CheckModuleAccess(userID, modTrim) {
		return true
	}
	if modTrimPlugin := strings.TrimPrefix(moduleName, "GorokuPlugin"); modTrimPlugin != moduleName && sm.CheckModuleAccess(userID, modTrimPlugin) {
		return true
	}
	return false
}

func (im *InlineManager) ownerID() int64 {
	if im.client == nil {
		return 0
	}
	return im.client.TGIDValue()
}

func (im *InlineManager) handleChosenInlineResult(ctx context.Context, r *tgbotapi.ChosenInlineResult) {
	parts := strings.SplitN(r.Query, " ", 2)
	var switchQuery string
	if len(parts) > 0 {
		switchQuery = parts[0]
	}

	im.mu.RLock()
	btn, existsInput := im.customMap[switchQuery]
	im.mu.RUnlock()

	if existsInput && btn.Input != "" && btn.InputHandler != nil {
		inputVal := ""
		if len(parts) > 1 {
			inputVal = strings.TrimSpace(parts[1])
		}

		im.mu.RLock()
		unitID := im.buttonUnits[switchQuery]
		im.mu.RUnlock()

		cb := CallbackQuery{
			ID:      r.ResultID,
			FromID:  r.From.ID,
			Data:    r.Query,
			Manager: im,
			leased:  true,
		}

		if unitID != "" {
			im.mu.Lock()
			msgInfo, hasMsgInfo := im.activeMessageIDs[unitID]
			inlineMsgID, hasInlineMsgID := im.activeInlineMessages[unitID]
			im.mu.Unlock()

			if hasInlineMsgID && inlineMsgID != "" {
				cb.InlineMessage = NewInlineMessage(im, unitID, inlineMsgID)
			} else if hasMsgInfo && msgInfo.ChatID != 0 && msgInfo.MessageID != 0 {
				cb.BotMessage = NewBotInlineMessage(im, unitID, msgInfo.ChatID, int64(msgInfo.MessageID))
			}
		}

		if cb.InlineMessage == nil && cb.BotMessage == nil && r.InlineMessageID != "" {
			cb.InlineMessage = NewInlineMessage(im, unitID, r.InlineMessageID)
		}

		run := func() {
			defer func() {
				if r := recover(); r != nil {
					L().Error("[Inline] Input handler panic", zap.Any("panic", r))
				}
			}()
			im.mu.RLock()
			current, exists := im.customMap[switchQuery]
			im.mu.RUnlock()
			if exists && current.InputHandler != nil {
				_ = current.InputHandler(cb, inputVal)
			}
		}
		im.mu.RLock()
		unit := im.units[unitID]
		im.mu.RUnlock()
		if unit != nil && unit.Module != "" {
			im.startHandler(ctx, func(context.Context) {
				if !im.withModule(unit.Module, func(any) { run() }) {
					_ = cb.Answer("This module is no longer available", true)
				}
			})
		} else {
			im.startHandler(ctx, func(context.Context) { run() })
		}
		return
	}

	unitID := r.Query
	if unitID == "" {
		unitID = r.ResultID
	}
	im.mu.Lock()
	unit, exists := im.units[unitID]
	msgInfo := im.activeMessageIDs[unitID]
	if exists {
		im.activeInlineMessages[unitID] = r.InlineMessageID
	}
	ch, hasCh := im.errorEvents[unitID]
	im.mu.Unlock()

	if exists && unit != nil && unit.StartText != "" {
		im.startHandler(ctx, func(ctx context.Context) {
			markup := im.generateMarkup(unit.Buttons)
			for attempt := 1; attempt <= 5; attempt++ {
				if attempt > 1 {
					if sleepContext(ctx, time.Duration(attempt*250)*time.Millisecond) != nil {
						return
					}
				}
				var err error
				if r.InlineMessageID != "" {
					err = NewInlineMessage(im, unitID, r.InlineMessageID).Edit(unit.Text, markup)
				} else if msgInfo.ChatID != 0 && msgInfo.MessageID != 0 {
					err = NewBotInlineMessage(im, unitID, msgInfo.ChatID, int64(msgInfo.MessageID)).Edit(unit.Text, markup)
				} else {
					err = fmt.Errorf("no inline_message_id and no active message id")
				}
				if err != nil {
					L().Warn("Failed to edit start text", zap.String("unit", unitID), zap.String("inline", r.InlineMessageID), zap.Int64("chat", msgInfo.ChatID), zap.Int64("message", msgInfo.MessageID), zap.Int("attempt", attempt), zap.Error(err))
					continue
				}
				L().Debug("Edited start text", zap.String("unit", unitID), zap.String("inline", r.InlineMessageID), zap.Int64("chat", msgInfo.ChatID), zap.Int64("message", msgInfo.MessageID), zap.Int("attempt", attempt))
				break
			}
		})
	}

	if hasCh {
		ch <- nil
	}
}

func (im *InlineManager) handleBotMessage(m *tgbotapi.Message) {
	im.handleBotPM(m)
}
