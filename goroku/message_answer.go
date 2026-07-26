package goroku

import (
	stdhtml "html"
	"os"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

const telegramMessageLimit = 4096

type answerMode int

const (
	answerModeDirect answerMode = iota
	answerModeInlineList
	answerModeFile
)

type answerPlan struct {
	mode     answerMode
	pages    []string
	fileText string
}

func telegramTextLen(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func splitPlainTextForTelegram(text string, limit int) []string {
	if telegramTextLen(text) <= limit {
		return []string{text}
	}

	var chunks []string
	remaining := text
	for remaining != "" {
		if telegramTextLen(remaining) <= limit {
			chunks = append(chunks, remaining)
			break
		}

		cut := 0
		units := 0
		for idx, r := range remaining {
			rUnits := telegramTextLen(string(r))
			if units+rUnits > limit {
				break
			}
			units += rUnits
			cut = idx + len(string(r))
		}
		if cut <= 0 {
			cut = len([]rune(remaining[:1]))
		}

		splitAt := cut
		for _, sep := range []string{"\n", " "} {
			if idx := strings.LastIndex(remaining[:cut], sep); idx >= cut/2 {
				splitAt = idx
				break
			}
		}

		chunk := strings.TrimRight(remaining[:splitAt], "\n ")
		if chunk == "" {
			chunk = remaining[:cut]
			splitAt = cut
		}
		chunks = append(chunks, chunk)
		remaining = strings.TrimLeft(remaining[splitAt:], "\n ")
	}
	return chunks
}

func sliceAnswerEntities(entities []tg.MessageEntityClass, start, length int) []tg.MessageEntityClass {
	end := start + length
	var sliced []tg.MessageEntityClass
	for _, entity := range entities {
		from := max(entity.GetOffset(), start)
		to := min(entity.GetOffset()+entity.GetLength(), end)
		if from >= to {
			continue
		}
		clone := reflect.New(reflect.TypeOf(entity).Elem())
		clone.Elem().Set(reflect.ValueOf(entity).Elem())
		clone.Elem().FieldByName("Offset").SetInt(int64(from - start))
		clone.Elem().FieldByName("Length").SetInt(int64(to - from))
		sliced = append(sliced, clone.Interface().(tg.MessageEntityClass))
	}
	return sliced
}

func planLongAnswer(rawText string, canUseInline bool) answerPlan {
	plainText, entities := parseHTML(rawText)
	if telegramTextLen(plainText) < telegramMessageLimit {
		return answerPlan{mode: answerModeDirect}
	}

	plainPages := splitPlainTextForTelegram(plainText, telegramMessageLimit)
	if canUseInline {
		pages := make([]string, len(plainPages))
		byteOffset := 0
		utf16Offset := 0
		for i, page := range plainPages {
			skipped := strings.Index(plainText[byteOffset:], page)
			utf16Offset += telegramTextLen(plainText[byteOffset : byteOffset+skipped])
			pageLen := telegramTextLen(page)
			pages[i] = EntitiesToHTML(page, sliceAnswerEntities(entities, utf16Offset, pageLen))
			byteOffset += skipped + len(page)
			utf16Offset += pageLen
		}
		return answerPlan{mode: answerModeInlineList, pages: pages}
	}

	return answerPlan{mode: answerModeFile, fileText: plainText}
}

func (m *Message) Answer(text string, opts ...MsgOption) error {
	m.Answered = true
	ctx := m.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.GrepQuery != "" {
		lines := strings.Split(text, "\n")
		var matchingLines []string
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(m.GrepQuery))

		for _, line := range lines {
			matched := false
			if err == nil {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(m.GrepQuery))
			}

			if m.GrepInvert {
				if !matched {
					matchingLines = append(matchingLines, line)
				}
			} else {
				if matched {
					if err == nil {
						line = re.ReplaceAllString(line, "<u>$0</u>")
					} else {
						line = strings.ReplaceAll(line, m.GrepQuery, "<u>"+m.GrepQuery+"</u>")
					}
					matchingLines = append(matchingLines, line)
				}
			}
		}

		if len(matchingLines) == 0 {
			text = "<i>(grep output empty)</i>"
		} else {
			text = strings.Join(matchingLines, "\n")
		}
	}

	// Apply cut (keep first N lines)
	if m.CutLines > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > m.CutLines {
			lines = lines[:m.CutLines]
		}
		text = strings.Join(lines, "\n")
	}

	plainText, _ := parseHTML(text)

	// Apply split (send as multiple messages instead of file)
	if m.SplitOutput && telegramTextLen(plainText) >= telegramMessageLimit {
		chunks := splitPlainTextForTelegram(plainText, telegramMessageLimit)
		for i, chunk := range chunks {
			chunk = stdhtml.EscapeString(chunk)
			var err error
			if i == 0 {
				if m.Out {
					_, err = m.Client.EditMessageContext(ctx, ChatRefID(m.ChatID), m.ID, chunk, opts...)
				} else {
					_, err = m.Client.SendMessageWithOptionsContext(ctx, ChatRefID(m.ChatID), chunk, opts...)
				}
			} else {
				_, err = m.Client.SendMessageWithOptionsContext(ctx, ChatRefID(m.ChatID), chunk, opts...)
			}
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
		return nil
	}

	plan := planLongAnswer(text, m.GrepQuery == "")
	switch plan.mode {
	case answerModeInlineList:
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.Client != nil {
			if im := m.Client.InlineManager(); im != nil && im.IsComplete() {
				if _, err := im.List(m, plan.pages); err == nil {
					return nil
				}
			}
		}
		fallthrough
	case answerModeFile:
		fileText := plan.fileText
		if fileText == "" {
			fileText = plainText
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		tmpFile, err := os.CreateTemp("", "command_result_*.txt")
		if err == nil {
			defer func() { _ = os.Remove(tmpFile.Name()) }()
			_, _ = tmpFile.WriteString(fileText)
			_ = tmpFile.Close()
			if m.Out {
				_, err = m.Client.EditMessageFileContext(ctx, ChatRefID(m.ChatID), m.ID, tmpFile.Name(), "💾 Output too long")
			} else {
				_, err = m.Client.SendFileContext(ctx, ChatRefID(m.ChatID), tmpFile.Name(), "💾 Output too long")
			}
		} else {
			_, err = m.Client.SendFileContext(ctx, ChatRefID(m.ChatID), []byte(fileText), "💾 Output too long")
		}
		if err != nil || !m.Out {
			return err
		}
		if tmpFile != nil {
			return nil
		}
		return m.Client.DeleteMessageContext(ctx, ChatRefID(m.ChatID), m.ID)
	}
	if m.Out {
		_, err := m.Client.EditMessageContext(ctx, ChatRefID(m.ChatID), m.ID, text, opts...)
		return err
	}
	_, err := m.Client.SendMessageWithOptionsContext(ctx, ChatRefID(m.ChatID), text, opts...)
	return err
}
