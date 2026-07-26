package modules

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"goroku/goroku"
)

const (
	moduleCardCommandLimit = 8
	moduleCardDocLimit     = 120
	defaultCommandEmoji    = "<tg-emoji emoji-id=5197195523794157505>▫️</tg-emoji>"
	defaultLoadedTemplate  = "<tg-emoji emoji-id=5134452506935427991>🪐</tg-emoji> <b>Module</b> <code>{}</code>{} <b>loaded {}</b>{}{}{}{}{}{}{}{}"
)

type moduleSourceKind string

const (
	moduleSourceRepository moduleSourceKind = "Repository"
	moduleSourceURL        moduleSourceKind = "URL"
	moduleSourceLocal      moduleSourceKind = "Local file"
)

func moduleCommandPrefix(db *goroku.Database, senderID int64) string {
	if db == nil {
		return "."
	}
	prefixes := db.GetStringMap("goroku.main", "prefixes", nil)
	if prefix, ok := prefixes[strconv.FormatInt(senderID, 10)]; ok && prefix != "" {
		return prefix
	}
	return db.GetString("goroku.main", "command_prefix", ".")
}

func sanitizedModuleSource(kind moduleSourceKind, raw string) string {
	if kind == moduleSourceLocal {
		return string(moduleSourceLocal)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return string(kind)
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	if kind == moduleSourceRepository {
		return fmt.Sprintf("%s: %s", kind, u.Hostname())
	}
	return fmt.Sprintf("%s: %s", kind, u.String())
}

func formatModuleInstallError(err error) string {
	if err == nil {
		err = errors.New("unknown installation error")
	}
	return fmt.Sprintf("❌ <b>Module installation failed</b>\n<blockquote>%s</blockquote>\n<i>No restart is required; the previous runtime state was preserved where rollback was possible.</i>", html.EscapeString(truncateModuleText(err.Error(), 3000)))
}

func truncateModuleText(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-3])) + "..."

}

func truncateModuleDoc(doc string) string {
	return truncateModuleText(strings.Join(strings.Fields(doc), " "), moduleCardDocLimit)
}

func formatSequentialPlaceholders(template string, values ...string) string {
	for _, value := range values {
		template = strings.Replace(template, "{}", value, 1)
	}
	return template
}

func formatModuleInstalledCard(mod goroku.Module, prefix, source string, warning error, loadedTemplate, commandEmoji, undoc string) string {
	if prefix == "" {
		prefix = "."
	}
	if loadedTemplate == "" {
		loadedTemplate = defaultLoadedTemplate
	}
	if commandEmoji == "" {
		commandEmoji = defaultCommandEmoji
	}
	runtimeName := mod.Name()
	displayName := runtimeName
	stringsMap := mod.Strings()
	if name := strings.TrimSpace(stringsMap["name"]); name != "" {
		displayName = name
	}

	var docBlock string
	if doc := truncateModuleDoc(stringsMap["_cls_doc"]); doc != "" {
		docBlock = "\n\n<i><tg-emoji emoji-id=5879813604068298387>ℹ️</tg-emoji> " + html.EscapeString(doc) + "</i>"
	}

	commands := make([]string, 0, len(mod.Commands()))
	for command := range mod.Commands() {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	var commandBlock strings.Builder
	if len(commands) > 0 {
		commandBlock.WriteString("\n\n<blockquote expandable>")
		shown := commands
		if len(shown) > moduleCardCommandLimit {
			shown = shown[:moduleCardCommandLimit]
		}
		for i, command := range shown {
			if i > 0 {
				commandBlock.WriteByte('\n')
			}
			commandBlock.WriteString(commandEmoji)
			commandBlock.WriteString(" <code>")
			commandBlock.WriteString(html.EscapeString(truncateModuleText(prefix+command, 64)))
			commandBlock.WriteString("</code> ")
			doc := truncateModuleDoc(stringsMap["_cmd_doc_"+command])
			if doc == "" {
				doc = undoc
			}
			commandBlock.WriteString(html.EscapeString(doc))
		}
		if remaining := len(commands) - len(shown); remaining > 0 {
			commandBlock.WriteString(fmt.Sprintf("\n<i>+%d · <code>%shelp %s</code></i>", remaining, html.EscapeString(truncateModuleText(prefix, 16)), html.EscapeString(truncateModuleText(runtimeName, 100))))
		}
		commandBlock.WriteString("</blockquote>")
	}

	var sourceBlock string
	if source != string(moduleSourceLocal) {
		sourceBlock = "\n\n<tg-emoji emoji-id=6037284117505116849>🌐</tg-emoji> <code>" + html.EscapeString(truncateModuleText(source, 320)) + "</code>"
	}
	card := formatSequentialPlaceholders(
		loadedTemplate,
		html.EscapeString(truncateModuleText(displayName, 160)),
		"",
		"(•‿•)",
		docBlock,
		commandBlock.String(),
		"",
		"",
		"",
		sourceBlock,
		"",
		"",
	)
	if warning != nil {
		card += "\n\n<tg-emoji emoji-id=5312383351217201533>⚠️</tg-emoji> <i>" + html.EscapeString(truncateModuleText(warning.Error(), 500)) + "</i>"
	}
	return card
}
