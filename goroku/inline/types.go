package inline

import (
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Unit struct {
	ID              string
	Type            string // "form", "gallery", "list", "query_gallery"
	Text            string
	Message         interface{}
	Handler         interface{}
	TTL             time.Time
	GalleryFn       func(int) string
	Pages           []string
	Buttons         [][]Button
	CurrentPage     int
	TotalPages      int
	TotalItems      int
	Interval        time.Duration
	Photo           string
	Gif             bool
	GifURL          string
	Video           string
	File            string
	MimeType        string
	Location        []float64
	Audio           interface{}
	ForceMe         bool
	AlwaysAllow     []int64
	DisableSecurity bool
	OnUnload        func()
	Module          string
}

type Button struct {
	Text         string
	Data         string
	URL          string
	Handler      func(CallbackQuery) error
	Input        string
	SwitchQuery  string
	InputHandler func(CallbackQuery, string) error
}

type CallbackQuery struct {
	ID            string
	FromID        int64
	ChatID        int64
	MessageID     int64
	Data          string
	InlineMessage *InlineMessage
	BotMessage    *BotInlineMessage
	Manager       *InlineManager
}

func (c CallbackQuery) Answer(text string, showAlert bool) error {
	callbackConfig := tgbotapi.CallbackConfig{
		CallbackQueryID: c.ID,
		Text:            text,
		ShowAlert:       showAlert,
	}
	_, err := c.Manager.bot.Request(callbackConfig)
	return err
}

func (c CallbackQuery) Edit(text string, markup tgbotapi.InlineKeyboardMarkup) error {
	if c.InlineMessage != nil {
		return c.InlineMessage.Edit(text, markup)
	}
	if c.BotMessage != nil {
		return c.BotMessage.Edit(text, markup)
	}
	return fmt.Errorf("no message to edit")
}

type InlineMessage struct {
	InlineMessageID string
	UnitID          string
	InlineManager   *InlineManager
	Form            map[string]interface{}
}

func NewInlineMessage(im *InlineManager, unitID, inlineMessageID string) *InlineMessage {
	return &InlineMessage{
		InlineMessageID: inlineMessageID,
		UnitID:          unitID,
		InlineManager:   im,
		Form:            make(map[string]interface{}),
	}
}

func (m *InlineMessage) Edit(text string, markup tgbotapi.InlineKeyboardMarkup) error {
	if m.UnitID != "" && m.InlineManager != nil {
		m.InlineManager.mu.Lock()
		for _, row := range markup.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData != "" {
					m.InlineManager.buttonUnits[*btn.CallbackData] = m.UnitID
				}
			}
		}
		m.InlineManager.mu.Unlock()
	}

	var replyMarkup *tgbotapi.InlineKeyboardMarkup
	if len(markup.InlineKeyboard) > 0 {
		replyMarkup = &markup
	}
	editMsg := tgbotapi.EditMessageTextConfig{
		BaseEdit: tgbotapi.BaseEdit{
			InlineMessageID: m.InlineMessageID,
			ReplyMarkup:     replyMarkup,
		},
		Text:      text,
		ParseMode: tgbotapi.ModeHTML,
	}
	_, err := m.InlineManager.bot.Request(editMsg)
	return err
}

type deletableClient interface {
	DeleteMessage(chat interface{}, msgID int64) error
}

func (m *InlineMessage) Delete() (bool, error) {
	// First check if we have mapped ChatID and MessageID for this Unit ID
	m.InlineManager.mu.RLock()
	info, hasInfo := m.InlineManager.activeMessageIDs[m.UnitID]
	m.InlineManager.mu.RUnlock()

	if hasInfo && info.MessageID != 0 {
		if delClient, ok := m.InlineManager.client.(deletableClient); ok {
			err := delClient.DeleteMessage(info.ChatID, info.MessageID)
			if err == nil {
				m.InlineManager.mu.Lock()
				delete(m.InlineManager.activeMessageIDs, m.UnitID)
				m.InlineManager.mu.Unlock()
				return true, nil
			}
		}
	}

	// Inline query messages cannot be deleted by bot API, but we can wipe their text/markup
	editMsg := tgbotapi.EditMessageTextConfig{
		BaseEdit: tgbotapi.BaseEdit{
			InlineMessageID: m.InlineMessageID,
		},
		Text:      "🗑 <i>Message closed.</i>",
		ParseMode: tgbotapi.ModeHTML,
	}
	_, err := m.InlineManager.bot.Request(editMsg)
	return err == nil, err
}

func (m *InlineMessage) Unload() (bool, error) {
	return m.Delete()
}

type BotInlineMessage struct {
	ChatID        int64
	UnitID        string
	InlineManager *InlineManager
	MessageID     int64
	Form          map[string]interface{}
}

func NewBotInlineMessage(im *InlineManager, unitID string, chatID, messageID int64) *BotInlineMessage {
	return &BotInlineMessage{
		ChatID:        chatID,
		UnitID:        unitID,
		InlineManager: im,
		MessageID:     messageID,
		Form:          make(map[string]interface{}),
	}
}

func (m *BotInlineMessage) Edit(text string, markup tgbotapi.InlineKeyboardMarkup) error {
	if m.UnitID != "" && m.InlineManager != nil {
		m.InlineManager.mu.Lock()
		for _, row := range markup.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData != "" {
					m.InlineManager.buttonUnits[*btn.CallbackData] = m.UnitID
				}
			}
		}
		m.InlineManager.mu.Unlock()
	}

	var replyMarkup *tgbotapi.InlineKeyboardMarkup
	if len(markup.InlineKeyboard) > 0 {
		replyMarkup = &markup
	}
	editMsg := tgbotapi.EditMessageTextConfig{
		BaseEdit: tgbotapi.BaseEdit{
			ChatID:      m.ChatID,
			MessageID:   int(m.MessageID),
			ReplyMarkup: replyMarkup,
		},
		Text:      text,
		ParseMode: tgbotapi.ModeHTML,
	}
	_, err := m.InlineManager.bot.Request(editMsg)
	return err
}

func (m *BotInlineMessage) Delete() (bool, error) {
	delMsg := tgbotapi.DeleteMessageConfig{
		ChatID:    m.ChatID,
		MessageID: int(m.MessageID),
	}
	_, err := m.InlineManager.bot.Request(delMsg)
	return err == nil, err
}

func (m *BotInlineMessage) Unload() (bool, error) {
	return m.Delete()
}

type InlineCall struct {
	*InlineMessage
	Data string
}

type BotInlineCall struct {
	*BotInlineMessage
	Data string
}

type InlineUnit struct{}

type BotMessage struct {
	ID   int64
	Text string
}

type InlineQuery struct {
	QueryID string
	Query   string
	Args    string
	FromID  int64
	Manager *InlineManager
}

type InlineResult struct {
	Title       string
	Description string
	Message     string
	Photo       string
	Gif         string
	Video       string
	File        string
	MimeType    string
	Caption     string
	Thumb       string
	ReplyMarkup [][]Button
}

type InlineHandler func(*InlineQuery) ([]InlineResult, error)
type ModuleInlineHandlers interface {
	InlineHandlers() map[string]InlineHandler
}
type ModuleInlineHelp interface {
	InlineHelp() map[string]string
}
type ModuleCallbackHandlers interface {
	CallbackHandlers() []func(CallbackQuery) error
}

func (q *InlineQuery) Answer(results []tgbotapi.InlineQueryResultArticle, cacheTime int) error {
	// Convert slice of articles to slice of interface{} for tgbotapi
	var iResults []interface{}
	for _, res := range results {
		iResults = append(iResults, res)
	}

	answer := tgbotapi.InlineConfig{
		InlineQueryID: q.QueryID,
		Results:       iResults,
		CacheTime:     cacheTime,
		IsPersonal:    true,
	}
	_, err := q.Manager.bot.Request(answer)
	return err
}

func (q *InlineQuery) AnswerResults(results []InlineResult, cacheTime int) error {
	var iResults []interface{}
	for _, res := range results {
		id := localRandStr(20)
		markup := q.Manager.GenerateMarkup(res.ReplyMarkup)
		switch {
		case res.Message != "":
			article := tgbotapi.NewInlineQueryResultArticle(id, res.Title, res.Message)
			article.Description = res.Description
			article.ThumbURL = res.Thumb
			article.ReplyMarkup = &markup
			article.InputMessageContent = tgbotapi.InputTextMessageContent{Text: res.Message, ParseMode: tgbotapi.ModeHTML, DisableWebPagePreview: true}
			iResults = append(iResults, article)
		case res.Photo != "":
			photo := tgbotapi.NewInlineQueryResultPhoto(id, res.Photo)
			photo.Title = res.Title
			photo.Description = res.Description
			photo.Caption = res.Caption
			photo.ParseMode = tgbotapi.ModeHTML
			photo.ThumbURL = firstNonEmpty(res.Thumb, res.Photo)
			photo.ReplyMarkup = &markup
			iResults = append(iResults, photo)
		case res.Gif != "":
			gif := tgbotapi.NewInlineQueryResultGIF(id, res.Gif)
			gif.Title = res.Title
			gif.Caption = res.Caption
			gif.ParseMode = tgbotapi.ModeHTML
			gif.ThumbURL = firstNonEmpty(res.Thumb, res.Gif)
			gif.ReplyMarkup = &markup
			iResults = append(iResults, gif)
		case res.Video != "":
			video := tgbotapi.NewInlineQueryResultVideo(id, res.Video)
			video.Title = res.Title
			video.Description = res.Description
			video.Caption = res.Caption
			video.ThumbURL = firstNonEmpty(res.Thumb, res.Video)
			video.MimeType = "video/mp4"
			video.ReplyMarkup = &markup
			iResults = append(iResults, video)
		case res.File != "":
			doc := tgbotapi.NewInlineQueryResultDocument(id, res.File, firstNonEmpty(res.Title, "Document"), res.MimeType)
			doc.Description = res.Description
			doc.Caption = res.Caption
			doc.ThumbURL = firstNonEmpty(res.Thumb, res.File)
			doc.ReplyMarkup = &markup
			iResults = append(iResults, doc)
		}
	}
	answer := tgbotapi.InlineConfig{InlineQueryID: q.QueryID, Results: iResults, CacheTime: cacheTime, IsPersonal: true}
	_, err := q.Manager.bot.Request(answer)
	return err
}

func (q *InlineQuery) E400() error {
	return q.answerError("e400", "Bad Request")
}
func (q *InlineQuery) E403() error {
	return q.answerError("e403", "Forbidden")
}
func (q *InlineQuery) E404() error {
	return q.answerError("e404", "Not Found")
}
func (q *InlineQuery) E426() error {
	return q.answerError("e426", "Upgrade Required")
}
func (q *InlineQuery) E500() error {
	return q.answerError("e500", "Internal Server Error")
}

func (q *InlineQuery) answerError(code, text string) error {
	article := tgbotapi.NewInlineQueryResultArticle(q.QueryID, code, text)
	article.Description = fmt.Sprintf("Error %s: %s", code, text)
	article.InputMessageContent = tgbotapi.InputTextMessageContent{
		Text: fmt.Sprintf("🚫 <b>Error %s</b>\n%s", code, text),
	}
	return q.Answer([]tgbotapi.InlineQueryResultArticle{article}, 0)
}

type QueryGalleryItem struct {
	Title           string
	Description     string
	NextHandler     interface{} // []string or func(int) string
	Caption         string
	ForceMe         bool
	DisableSecurity bool
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
