package inline

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// QueryGallery answers an inline query with articles that expand into full galleries when clicked.
func (im *InlineManager) QueryGallery(
	queryID string,
	items []QueryGalleryItem,
) error {
	var results []interface{}

	for _, item := range items {
		id := localRandStr(16)

		im.mu.Lock()
		im.QueryGalleries[id] = item
		im.mu.Unlock()

		var photoURL string
		if list, ok := item.NextHandler.([]string); ok && len(list) > 0 {
			photoURL = list[0]
		} else {
			photoURL = "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/loading.png"
		}

		article := tgbotapi.NewInlineQueryResultArticle(
			id,
			item.Title,
			fmt.Sprintf("🪐 <b>Opening gallery...</b>\n<i>#id: %s</i>", id),
		)
		article.Description = item.Description
		article.ThumbURL = photoURL
		article.ThumbWidth = 128
		article.ThumbHeight = 128

		// Set input message content format for the expansion watcher to match
		article.InputMessageContent = tgbotapi.InputTextMessageContent{
			Text:      fmt.Sprintf("🪐 <b>Opening gallery...</b>\n<i>#id: %s</i>", id),
			ParseMode: tgbotapi.ModeHTML,
		}

		results = append(results, article)
	}

	answer := tgbotapi.InlineConfig{
		InlineQueryID: queryID,
		Results:       results,
		CacheTime:     0,
		IsPersonal:    true,
	}

	_, err := im.bot.Request(answer)
	return err
}

func (im *InlineManager) PopQueryGallery(id string) (QueryGalleryItem, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	item, ok := im.QueryGalleries[id]
	if ok {
		delete(im.QueryGalleries, id)
	}
	return item, ok
}
