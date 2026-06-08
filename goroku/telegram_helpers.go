package goroku

import (
	"encoding/json"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// SendMessageWithTopic sends a message to a specific topic thread in a forum chat.
func SendMessageWithTopic(bot *tgbotapi.BotAPI, chatID int64, text string, topicID int) (tgbotapi.Message, error) {
	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"text":       text,
		"parse_mode": tgbotapi.ModeHTML,
	}
	if topicID != 0 {
		params["message_thread_id"] = strconv.Itoa(topicID)
	}
	resp, err := bot.MakeRequest("sendMessage", params)
	if err != nil {
		return tgbotapi.Message{}, err
	}
	var msg tgbotapi.Message
	err = json.Unmarshal(resp.Result, &msg)
	return msg, err
}

// SendPhotoWithTopic sends a photo to a specific topic thread in a forum chat.
func SendPhotoWithTopic(bot *tgbotapi.BotAPI, chatID int64, file interface{}, caption string, topicID int) (tgbotapi.Message, error) {
	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"caption":    caption,
		"parse_mode": tgbotapi.ModeHTML,
	}
	if topicID != 0 {
		params["message_thread_id"] = strconv.Itoa(topicID)
	}

	var resp *tgbotapi.APIResponse
	var err error

	switch f := file.(type) {
	case tgbotapi.FileURL:
		params["photo"] = string(f)
		resp, err = bot.MakeRequest("sendPhoto", params)
	case tgbotapi.FileBytes:
		files := []tgbotapi.RequestFile{{Name: "photo", Data: f}}
		resp, err = bot.UploadFiles("sendPhoto", params, files)
	default:
		return tgbotapi.Message{}, fmt.Errorf("unsupported photo file type: %T", file)
	}

	if err != nil {
		return tgbotapi.Message{}, err
	}
	var msg tgbotapi.Message
	err = json.Unmarshal(resp.Result, &msg)
	return msg, err
}

// SendDocumentWithTopic sends a document to a specific topic thread in a forum chat.
func SendDocumentWithTopic(bot *tgbotapi.BotAPI, chatID int64, file interface{}, caption string, topicID int) (tgbotapi.Message, error) {
	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"caption":    caption,
		"parse_mode": tgbotapi.ModeHTML,
	}
	if topicID != 0 {
		params["message_thread_id"] = strconv.Itoa(topicID)
	}

	var resp *tgbotapi.APIResponse
	var err error

	switch f := file.(type) {
	case tgbotapi.FileURL:
		params["document"] = string(f)
		resp, err = bot.MakeRequest("sendDocument", params)
	case tgbotapi.FileBytes:
		files := []tgbotapi.RequestFile{{Name: "document", Data: f}}
		resp, err = bot.UploadFiles("sendDocument", params, files)
	default:
		return tgbotapi.Message{}, fmt.Errorf("unsupported document file type: %T", file)
	}

	if err != nil {
		return tgbotapi.Message{}, err
	}
	var msg tgbotapi.Message
	err = json.Unmarshal(resp.Result, &msg)
	return msg, err
}
