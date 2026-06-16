package utils

import (
	"fmt"
	"reflect"
	"regexp"
)

func GetTopic(msgText string) int64 {
	// Placeholder logic mapping python's reply_to topic extracts
	return 0
}

func GetMimeType(media interface{}) string {
	if media == nil {
		return ""
	}
	// Try to get the MimeType via reflection (like TL document objects)
	v := reflect.ValueOf(media)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.Struct {
		f := v.FieldByName("MimeType")
		if f.IsValid() && f.Kind() == reflect.String {
			return f.String()
		}
	}
	return "application/octet-stream"
}

// SmartSplit splits text at word boundaries when possible, matching Python's smart_split behavior.
// It prefers to break at newlines, then spaces, then falls back to hard-cutting.
func SmartSplit(text string, length int) []string {
	runes := []rune(text)
	if len(runes) <= length {
		return []string{text}
	}

	var res []string
	for len(runes) > 0 {
		if len(runes) <= length {
			res = append(res, string(runes))
			break
		}

		chunk := runes[:length]
		// Try to split at last newline
		cut := -1
		for i := length - 1; i >= length/2; i-- {
			if chunk[i] == '\n' {
				cut = i + 1
				break
			}
		}
		// If no newline found, try last space
		if cut == -1 {
			for i := length - 1; i >= length/2; i-- {
				if chunk[i] == ' ' {
					cut = i + 1
					break
				}
			}
		}
		// Hard cut as last resort
		if cut == -1 {
			cut = length
		}

		res = append(res, string(runes[:cut]))
		runes = runes[cut:]
	}
	return res
}

func ArraySum(arrays [][]string) []string {
	var res []string
	for _, arr := range arrays {
		res = append(res, arr...)
	}
	return res
}

func Answer(msgText string, response string) string {
	// Emulates sending/editing response logic
	return fmt.Sprintf("Response: %s -> Output: %s", msgText, response)
}

func GetMessageLink(chatID, msgID int64, username string) string {
	if username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", username, msgID)
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", chatID, msgID)
}

// Censor censors sensitive tokens (API keys, passwords) from text.
func Censor(text string) string {
	return CensorSensitive(text)
}

// ExtractURLs extracts all http(s) URLs from text
func ExtractURLs(text string) []string {
	re := regexp.MustCompile(`https?://[^\s]+`)
	return re.FindAllString(text, -1)
}

// HasMedia returns true if message has media content.
// Uses reflection to check if msg has a non-nil Media field.
func HasMedia(msg interface{}) bool {
	if msg == nil {
		return false
	}
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		f := v.FieldByName("Media")
		if f.IsValid() && !f.IsNil() {
			return true
		}
	}
	return false
}
