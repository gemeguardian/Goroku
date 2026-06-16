package inline

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

func (im *InlineManager) HandleUpdate(update tgbotapi.Update) {
	log.Printf("[Inline] HandleUpdate: ID=%d, InlineQuery=%t, CallbackQuery=%t, ChosenInlineResult=%t\n",
		update.UpdateID, update.InlineQuery != nil, update.CallbackQuery != nil, update.ChosenInlineResult != nil)
	if update.InlineQuery != nil {
		im.handleInlineQuery(update.InlineQuery)
	} else if update.CallbackQuery != nil {
		im.handleCallbackQuery(update.CallbackQuery)
	} else if update.Message != nil {
		im.handleBotMessage(update.Message)
	} else if update.ChosenInlineResult != nil {
		im.handleChosenInlineResult(update.ChosenInlineResult)
	}
}

func unpackInterface(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	return v
}

func (im *InlineManager) isUserAuthorizedForInline(userID int64) bool {
	if userID == im.ownerID() {
		return true
	}
	allowInline := false
	if dbTyped, ok := im.db.(interface {
		Get(string, string, interface{}) interface{}
	}); ok {
		raw := dbTyped.Get("goroku.security", "allow_inline_query", false)
		if val, ok := raw.(bool); ok {
			allowInline = val
		}
	}
	if allowInline {
		return true
	}
	if im.client != nil {
		vClient := reflect.ValueOf(im.client)
		if vClient.Kind() == reflect.Ptr {
			vClient = vClient.Elem()
		}
		if vClient.Kind() == reflect.Struct {
			fLoader := vClient.FieldByName("Loader")
			if fLoader.IsValid() && !fLoader.IsNil() {
				vLoader := unpackInterface(fLoader)
				mDispatcher := vLoader.MethodByName("GetDispatcher")
				if mDispatcher.IsValid() {
					resDisp := mDispatcher.Call(nil)
					if len(resDisp) > 0 && !resDisp[0].IsNil() {
						vDisp := unpackInterface(resDisp[0])
						mSec := vDisp.MethodByName("GetSecurityManager")
						if mSec.IsValid() {
							resSec := mSec.Call(nil)
							if len(resSec) > 0 && !resSec[0].IsNil() {
								vSec := unpackInterface(resSec[0])

								// First try the new IsUserInAllUsers method
								mCheck := vSec.MethodByName("IsUserInAllUsers")
								if mCheck.IsValid() {
									res := mCheck.Call([]reflect.Value{reflect.ValueOf(userID)})
									if len(res) > 0 && res[0].Kind() == reflect.Bool && res[0].Bool() {
										return true
									}
								}

								// Fallback: reflection on allUsers if IsUserInAllUsers is not found
								vSecStruct := vSec
								if vSecStruct.Kind() == reflect.Ptr {
									vSecStruct = vSecStruct.Elem()
								}
								if vSecStruct.Kind() == reflect.Struct {
									fAllUsers := vSecStruct.FieldByName("allUsers")
									if fAllUsers.IsValid() && !fAllUsers.IsNil() {
										vAllUsers := unpackInterface(fAllUsers)
										mToSlice := vAllUsers.MethodByName("ToSlice")
										if mToSlice.IsValid() {
											resSlice := mToSlice.Call(nil)
											if len(resSlice) > 0 && resSlice[0].Kind() == reflect.Slice {
												slice := resSlice[0]
												for i := 0; i < slice.Len(); i++ {
													idVal := slice.Index(i).Interface()
													var id int64
													switch v := idVal.(type) {
													case int64:
														id = v
													case float64:
														id = int64(v)
													case int:
														id = int64(v)
													}
													if id == userID {
														return true
													}
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return false
}

func (im *InlineManager) handleInlineQuery(q *tgbotapi.InlineQuery) {
	if !im.isUserAuthorizedForInline(q.From.ID) {
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
			Results:       []interface{}{article},
			CacheTime:     0,
			IsPersonal:    true,
		}
		_, err := im.bot.Request(inlineConf)
		if err != nil {
			log.Printf("[Inline] Failed to answer input inline query: %v\n", err)
		}
		return
	}

	unitID := q.Query
	im.mu.Lock()
	unit, exists := im.units[unitID]
	im.mu.Unlock()

	if !exists {
		log.Printf("[Inline] Unit not found for query: %s\n", unitID)
		return
	}

	var result interface{}
	markup := im.GenerateMarkup(unit.Buttons)

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

		if m, ok := unit.Audio.(map[string]interface{}); ok {
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
		Results:       []interface{}{result},
		CacheTime:     0,
		IsPersonal:    true,
	}

	_, err := im.bot.Request(inlineConf)
	if err != nil {
		log.Printf("[Inline] Failed to answer inline query: %v\n", err)
	}
}

func (im *InlineManager) handleCallbackQuery(c *tgbotapi.CallbackQuery) {
	if strings.HasPrefix(c.Data, "authorize_web_") {
		token := strings.TrimPrefix(c.Data, "authorize_web_")
		im.mu.Lock()
		im.webAuthTokens = append(im.webAuthTokens, token)
		im.mu.Unlock()
		_, _ = im.bot.Request(tgbotapi.CallbackConfig{
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
	btn, exists := im.customMap[c.Data]
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

	im.dispatchModuleCallbacks(cb)

	if im.HandleGalleryCallback(cb) {
		return
	}
	if im.HandleListCallback(cb) {
		return
	}

	if !exists {
		callbackConfig := tgbotapi.CallbackConfig{
			CallbackQueryID: c.ID,
		}
		_, _ = im.bot.Request(callbackConfig)
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[Inline] Callback panic: %v\n", r)
			}
		}()
		err := btn.Handler(cb)
		if err != nil {
			log.Printf("[Inline] Callback handler error: %v\n", err)
			_ = cb.Answer(fmt.Sprintf("Error: %v", err), true)
		}
	}()
}

func (im *InlineManager) handleModuleInlineQuery(q *tgbotapi.InlineQuery, cmd string, parts []string) bool {
	for _, mod := range im.inlineModules() {
		handlers := mod.InlineHandlers()
		handler, ok := handlers[cmd]
		if !ok {
			handler, ok = handlers[strings.ToLower(cmd)]
		}
		if !ok || handler == nil {
			continue
		}
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		query := &InlineQuery{QueryID: q.ID, Query: q.Query, Args: args, FromID: q.From.ID, Manager: im}
		results, err := handler(query)
		if err != nil {
			log.Printf("[Inline] module inline handler %s failed: %v\n", cmd, err)
			_ = query.E500()
			return true
		}
		if len(results) == 0 {
			return true
		}
		if err := query.AnswerResults(results, 0); err != nil {
			log.Printf("[Inline] failed to answer module inline query %s: %v\n", cmd, err)
		}
		return true
	}
	return false
}

func (im *InlineManager) answerInlineHelp(q *tgbotapi.InlineQuery) {
	var text strings.Builder
	for _, mod := range im.inlineModules() {
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
			text.WriteString(fmt.Sprintf("• <code>@%s %s</code> — <b>%s</b>\n", im.BotUsername, cmd, desc))
			_ = name
		}
	}
	if text.Len() == 0 {
		return
	}
	article := tgbotapi.NewInlineQueryResultArticle(localRandStr(20), "Goroku inline commands", text.String())
	article.Description = "Available inline commands"
	article.InputMessageContent = tgbotapi.InputTextMessageContent{Text: text.String(), ParseMode: tgbotapi.ModeHTML}
	_, err := im.bot.Request(tgbotapi.InlineConfig{InlineQueryID: q.ID, Results: []interface{}{article}, CacheTime: 0, IsPersonal: true})
	if err != nil {
		log.Printf("[Inline] failed to answer inline help: %v\n", err)
	}
}

func (im *InlineManager) dispatchModuleCallbacks(cb CallbackQuery) {
	for _, mod := range im.callbackModules() {
		modName := ""
		if named, ok := mod.(interface{ Name() string }); ok {
			modName = named.Name()
		}
		// Security check: only allow owners or those who have trust on this module
		if sm := im.getSecurityManager(); sm != nil {
			if !im.isUserOwnerOrTrustedForModule(sm, cb.FromID, modName) {
				continue
			}
		} else {
			// fallback if security manager is not available: only owner
			if cb.FromID != im.ownerID() {
				continue
			}
		}

		for _, handler := range mod.CallbackHandlers() {
			if handler == nil {
				continue
			}
			go func(h func(CallbackQuery) error) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[Inline] module callback panic: %v\n", r)
					}
				}()
				if err := h(cb); err != nil {
					log.Printf("[Inline] module callback handler failed: %v\n", err)
				}
			}(handler)
		}
	}
}

func (im *InlineManager) inlineModules() []ModuleInlineHandlers {
	var modules []ModuleInlineHandlers
	for _, mod := range im.allModuleValues() {
		if h, ok := mod.(ModuleInlineHandlers); ok {
			modules = append(modules, h)
		}
	}
	return modules
}

func (im *InlineManager) callbackModules() []ModuleCallbackHandlers {
	var modules []ModuleCallbackHandlers
	for _, mod := range im.allModuleValues() {
		if h, ok := mod.(ModuleCallbackHandlers); ok {
			modules = append(modules, h)
		}
	}
	return modules
}

func (im *InlineManager) allModuleValues() []interface{} {
	if im.allModules == nil {
		return nil
	}
	val := reflect.ValueOf(im.allModules)
	method := val.MethodByName("GetModules")
	if !method.IsValid() {
		return nil
	}
	res := method.Call(nil)
	if len(res) == 0 || res[0].Kind() != reflect.Map {
		return nil
	}
	var out []interface{}
	iter := res[0].MapRange()
	for iter.Next() {
		out = append(out, iter.Value().Interface())
	}
	return out
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
	log.Printf("[SecurityDebug] isCallbackAllowed called: userID=%d, ownerID=%d, unit.Module=%q, DisableSecurity=%t, ForceMe=%t\n",
		userID, im.ownerID(), unit.Module, unit.DisableSecurity, unit.ForceMe)

	if unit.DisableSecurity {
		log.Printf("[SecurityDebug] Allow click: security is disabled for this unit.\n")
		return true
	}
	for _, allowed := range unit.AlwaysAllow {
		if allowed == userID {
			log.Printf("[SecurityDebug] Allow click: userID=%d is in AlwaysAllow list.\n", userID)
			return true
		}
	}
	if unit.ForceMe {
		res := userID == im.ownerID()
		log.Printf("[SecurityDebug] ForceMe check: allowed=%t (userID=%d, ownerID=%d)\n", res, userID, im.ownerID())
		return res
	}

	// Default security check
	if userID == im.ownerID() {
		log.Printf("[SecurityDebug] Allow click: userID=%d matches ownerID=%d.\n", userID, im.ownerID())
		return true
	}

	if sm := im.getSecurityManager(); sm != nil {
		// Check owner first using SecurityManager
		vSec := unpackInterface(reflect.ValueOf(sm))
		mIsOwner := vSec.MethodByName("IsOwner")
		if mIsOwner.IsValid() {
			resVals := mIsOwner.Call([]reflect.Value{reflect.ValueOf(userID)})
			if len(resVals) > 0 && resVals[0].Kind() == reflect.Bool && resVals[0].Bool() {
				log.Printf("[SecurityDebug] Allow click: userID=%d is verified owner by SecurityManager.\n", userID)
				return true
			}
		}

		// Check module trust
		if unit.Module != "" {
			res := im.isUserOwnerOrTrustedForModule(sm, userID, unit.Module)
			log.Printf("[SecurityDebug] Module trust check: userID=%d, module=%q, allowed=%t\n", userID, unit.Module, res)
			return res
		} else {
			log.Printf("[SecurityDebug] unit.Module is empty!\n")
		}
	} else {
		log.Printf("[SecurityDebug] SecurityManager is not available!\n")
	}

	log.Printf("[SecurityDebug] Deny click: userID=%d has no permission for module=%q.\n", userID, unit.Module)
	return false
}

func (im *InlineManager) getSecurityManager() interface{} {
	if im.client == nil {
		return nil
	}
	vClient := reflect.ValueOf(im.client)
	if vClient.Kind() == reflect.Ptr {
		vClient = vClient.Elem()
	}
	if vClient.Kind() == reflect.Struct {
		fLoader := vClient.FieldByName("Loader")
		if fLoader.IsValid() && !fLoader.IsNil() {
			vLoader := unpackInterface(fLoader)
			mDispatcher := vLoader.MethodByName("GetDispatcher")
			if mDispatcher.IsValid() {
				resDisp := mDispatcher.Call(nil)
				if len(resDisp) > 0 && !resDisp[0].IsNil() {
					vDisp := unpackInterface(resDisp[0])
					mSec := vDisp.MethodByName("GetSecurityManager")
					if mSec.IsValid() {
						resSec := mSec.Call(nil)
						if len(resSec) > 0 && !resSec[0].IsNil() {
							return resSec[0].Interface()
						}
					}
				}
			}
		}
	}
	return nil
}

func (im *InlineManager) isUserOwnerOrTrustedForModule(sm interface{}, userID int64, moduleName string) bool {
	if userID == im.ownerID() {
		return true
	}

	vSec := unpackInterface(reflect.ValueOf(sm))

	// Try calling IsOwner
	mIsOwner := vSec.MethodByName("IsOwner")
	if mIsOwner.IsValid() {
		res := mIsOwner.Call([]reflect.Value{reflect.ValueOf(userID)})
		if len(res) > 0 && res[0].Kind() == reflect.Bool && res[0].Bool() {
			return true
		}
	}

	// Try calling CheckModuleAccess
	mCheck := vSec.MethodByName("CheckModuleAccess")
	if mCheck.IsValid() {
		// Try exact module name
		res := mCheck.Call([]reflect.Value{
			reflect.ValueOf(userID),
			reflect.ValueOf(moduleName),
		})
		if len(res) > 0 && res[0].Kind() == reflect.Bool && res[0].Bool() {
			return true
		}

		// Try without "Goroku" prefix
		if modTrim := strings.TrimPrefix(moduleName, "Goroku"); modTrim != moduleName {
			res := mCheck.Call([]reflect.Value{
				reflect.ValueOf(userID),
				reflect.ValueOf(modTrim),
			})
			if len(res) > 0 && res[0].Kind() == reflect.Bool && res[0].Bool() {
				return true
			}
		}

		// Try without "GorokuPlugin" prefix
		if modTrimPlugin := strings.TrimPrefix(moduleName, "GorokuPlugin"); modTrimPlugin != moduleName {
			res := mCheck.Call([]reflect.Value{
				reflect.ValueOf(userID),
				reflect.ValueOf(modTrimPlugin),
			})
			if len(res) > 0 && res[0].Kind() == reflect.Bool && res[0].Bool() {
				return true
			}
		}
	}
	return false
}

func (im *InlineManager) ownerID() int64 {
	if im.client == nil {
		return 0
	}
	v := reflect.ValueOf(im.client)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName("TGID")
	if f.IsValid() && f.Kind() == reflect.Int64 {
		return f.Int()
	}
	return 0
}

func (im *InlineManager) handleChosenInlineResult(r *tgbotapi.ChosenInlineResult) {
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

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Inline] Input handler panic: %v\n", r)
				}
			}()
			_ = btn.InputHandler(cb, inputVal)
		}()
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
		go func() {
			markup := im.GenerateMarkup(unit.Buttons)
			for attempt := 1; attempt <= 5; attempt++ {
				if attempt > 1 {
					time.Sleep(time.Duration(attempt*250) * time.Millisecond)
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
					log.Printf("[Inline] failed to edit start text for unit %s inline=%s chat=%d message=%d attempt=%d: %v", unitID, r.InlineMessageID, msgInfo.ChatID, msgInfo.MessageID, attempt, err)
					continue
				}
				log.Printf("[Inline] edited start text for unit %s inline=%s chat=%d message=%d attempt=%d", unitID, r.InlineMessageID, msgInfo.ChatID, msgInfo.MessageID, attempt)
				break
			}
		}()
	}

	if hasCh {
		ch <- nil
	}
}

func (im *InlineManager) handleBotMessage(m *tgbotapi.Message) {
	im.HandleBotPM(m)
}
