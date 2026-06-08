package modules

import (
	"fmt"
	"html"
	"regexp"
	"goroku/goroku"
	"goroku/goroku/utils"
	"sort"
	"strconv"
	"strings"
)

// Help implements the Module interface and provides the .help command
// for listing all registered modules and their commands.
type Help struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator

	// Configs
	coreEmoji    string
	plainEmoji   string
	emptyEmoji   string
	descIcon     string
	commandEmoji string
	bannerUrl    string
	mediaQuote   bool
	invertMedia  bool
}

func (m *Help) Name() string {
	return "Help"
}

func (m *Help) Strings() map[string]string {
	return map[string]string{
		"name": "Help",
		"_cfg_core_emoji": "Bullet emoji/tag for core modules",
		"_cfg_plain_emoji": "Bullet emoji/tag for plain modules",
		"_cfg_empty_emoji": "Bullet emoji/tag for empty modules",
		"_cfg_desc_icon": "Emoji/tag for module description icon",
		"_cfg_command_emoji": "Emoji/tag for command bullet",
		"_cfg_banner_url": "Banner image URL shown in help menu",
		"_cfg_media_quote": "Switch preview media to quote",
		"_cfg_invert_media": "Invert media position (above/below text)",
	}
}

func (m *Help) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *Help) ClientReady() error { return nil }
func (m *Help) OnUnload() error    { return nil }
func (m *Help) OnDlmod() error     { return nil }

func (m *Help) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"core_emoji":    "<tg-emoji emoji-id=4974681956907221809>▪️</tg-emoji>",
		"plain_emoji":   "<tg-emoji emoji-id=4974508259839836856>▪️</tg-emoji>",
		"empty_emoji":   "<tg-emoji emoji-id=5100652175172830068>🟠</tg-emoji>",
		"desc_icon":     "<tg-emoji emoji-id=5188377234380954537>🪐</tg-emoji>",
		"command_emoji": "<tg-emoji emoji-id=5197195523794157505>▫️</tg-emoji>",
		"banner_url":    "",
		"media_quote":   false,
		"invert_media":  false,
	}
}

func (m *Help) ConfigReady(config map[string]interface{}) error {
	if val, ok := config["core_emoji"].(string); ok {
		m.coreEmoji = val
	}
	if val, ok := config["plain_emoji"].(string); ok {
		m.plainEmoji = val
	}
	if val, ok := config["empty_emoji"].(string); ok {
		m.emptyEmoji = val
	}
	if val, ok := config["desc_icon"].(string); ok {
		m.descIcon = val
	}
	if val, ok := config["command_emoji"].(string); ok {
		m.commandEmoji = val
	}
	if val, ok := config["banner_url"].(string); ok {
		m.bannerUrl = val
	}
	if val, ok := config["media_quote"].(bool); ok {
		m.mediaQuote = val
	}
	if val, ok := config["invert_media"].(bool); ok {
		m.invertMedia = val
	}
	return nil
}

func (m *Help) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"help":     m.HelpCmd,
		"helphide": m.HelphideCmd,
	}
}

func (m *Help) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

// getPrefix fetches the command prefix for the sender
func (m *Help) getPrefix(senderID int64) string {
	mainPrefixVal := m.db.Get("goroku.main", "command_prefix", ".")
	mainPrefix := "."
	if val, ok := mainPrefixVal.(string); ok {
		mainPrefix = val
	}
	prefixesVal := m.db.Get("goroku.main", "prefixes", nil)
	if prefixes, ok := prefixesVal.(map[string]interface{}); ok {
		senderStr := strconv.FormatInt(senderID, 10)
		if customPrefix, exists := prefixes[senderStr].(string); exists {
			return customPrefix
		}
	}
	return mainPrefix
}

// findAliases finds all aliases pointing to command
func (m *Help) findAliases(loader *goroku.Modules, command string) []string {
	var aliases []string
	cmdLower := strings.ToLower(command)
	for alias, realCmd := range loader.GetAliases() {
		if strings.ToLower(realCmd) == cmdLower {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

// formatPositional formats string replacing {} with args
func formatPositional(format string, args ...interface{}) string {
	res := format
	for _, arg := range args {
		res = strings.Replace(res, "{}", fmt.Sprintf("%v", arg), 1)
	}
	return res
}

// isCoreModule determines if a module is considered built-in core
func isCoreModule(name string) bool {
	coreMods := map[string]bool{
		"Help":                 true,
		"Settings":             true,
		"Translations":         true,
		"Security":             true,
		"Loader":               true,
		"APILimiter":           true,
		"Updater":              true,
		"GorokuPluginSecurity": true,
		"GorokuSecurity":       true,
		"GorokuSettings":       true,
		"GorokuConfig":         true,
		"GorokuInfo":           true,
		"GorokuWeb":            true,
		"Eval":                 true,
		"GorokuBackup":         true,
		"InlineStuff":          true,
		"Presets":              true,
		"Quickstart":           true,
		"Terminal":             true,
		"Tester":               true,
		"Translate":            true,
	}
	return coreMods[name] || strings.HasPrefix(strings.ToLower(name), "core")
}

// stringSimilarity calculates a Levenshtein distance similarity ratio
func stringSimilarity(a, b string) float64 {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = minInt(
				d[i-1][j]+1,
				d[i][j-1]+1,
				d[i-1][j-1]+cost,
			)
		}
	}
	dist := d[len(a)][len(b)]
	maxLen := maxInt(len(a), len(b))
	return 1.0 - float64(dist)/float64(maxLen)
}

func minInt(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// HelphideCmd toggles visibility of modules
func (m *Help) HelphideCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) == 0 {
		return msg.Answer(getTrans(m.translator, m.Name(), "no_mod", "🚫 <b>Specify module to hide</b>"))
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return msg.Answer("❌ Error: Modules registry not found.")
	}

	hiddenVal := m.db.Get("Help", "hide", []interface{}{})
	var currentlyHidden []string
	if slice, ok := hiddenVal.([]interface{}); ok {
		for _, v := range slice {
			if s, ok := v.(string); ok {
				currentlyHidden = append(currentlyHidden, s)
			}
		}
	} else if stringSlice, ok := hiddenVal.([]string); ok {
		currentlyHidden = stringSlice
	}

	var hidden []string
	var shown []string

	for _, arg := range args {
		mod := loader.LookupByName(arg)
		if mod == nil {
			continue
		}
		modName := mod.Name()
		foundIdx := -1
		for i, h := range currentlyHidden {
			if strings.EqualFold(h, modName) {
				foundIdx = i
				break
			}
		}

		if foundIdx >= 0 {
			currentlyHidden = append(currentlyHidden[:foundIdx], currentlyHidden[foundIdx+1:]...)
			shown = append(shown, modName)
		} else {
			currentlyHidden = append(currentlyHidden, modName)
			hidden = append(hidden, modName)
		}
	}

	m.db.Set("Help", "hide", currentlyHidden)

	var hiddenList []string
	for _, h := range hidden {
		hiddenList = append(hiddenList, fmt.Sprintf("👁‍🗨 <i>%s</i>", h))
	}
	hiddenStr := strings.Join(hiddenList, "\n")

	var shownList []string
	for _, s := range shown {
		shownList = append(shownList, fmt.Sprintf("👁 <i>%s</i>", s))
	}
	shownStr := strings.Join(shownList, "\n")

	tpl := getTrans(m.translator, m.Name(), "hidden_shown", "<b>{} modules hidden, {} modules shown:</b>\n{}\n{}")
	resp := formatPositional(tpl, len(hidden), len(shown), hiddenStr, shownStr)
	return msg.Answer(resp)
}

// HelpCmd handles the .help [module] command.
func (m *Help) HelpCmd(msg *goroku.Message) error {
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return msg.Answer("❌ Error: Modules registry not found.")
	}

	dispatcher := loader.GetDispatcher()
	if dispatcher == nil {
		return msg.Answer("❌ Error: Command dispatcher not found.")
	}

	// Fetch dynamic configuration emojis matching Python
	coreEmoji := m.coreEmoji
	if coreEmoji == "" {
		coreEmoji = "<tg-emoji emoji-id=4974681956907221809>▪️</tg-emoji>"
	}
	plainEmoji := m.plainEmoji
	if plainEmoji == "" {
		plainEmoji = "<tg-emoji emoji-id=4974508259839836856>▪️</tg-emoji>"
	}
	emptyEmoji := m.emptyEmoji
	if emptyEmoji == "" {
		emptyEmoji = "<tg-emoji emoji-id=5100652175172830068>🟠</tg-emoji>"
	}
	descIcon := m.descIcon
	if descIcon == "" {
		descIcon = "<tg-emoji emoji-id=5188377234380954537>🪐</tg-emoji>"
	}
	commandEmoji := m.commandEmoji
	if commandEmoji == "" {
		commandEmoji = "<tg-emoji emoji-id=5197195523794157505>▫️</tg-emoji>"
	}

	argsRaw := utils.GetArgsRaw(msg.Text)
	force := false
	if strings.Contains(argsRaw, "-f") {
		argsRaw = strings.ReplaceAll(argsRaw, " -f", "")
		argsRaw = strings.ReplaceAll(argsRaw, "-f", "")
		force = true
	}

	onlyCore := false
	if strings.Contains(argsRaw, "-c") {
		argsRaw = strings.ReplaceAll(argsRaw, " -c", "")
		argsRaw = strings.ReplaceAll(argsRaw, "-c", "")
		onlyCore = true
		force = true
	}

	onlyLoaded := false
	if strings.Contains(argsRaw, "-l") {
		argsRaw = strings.ReplaceAll(argsRaw, " -l", "")
		argsRaw = strings.ReplaceAll(argsRaw, "-l", "")
		onlyLoaded = true
		force = true
	}

	args := strings.TrimSpace(argsRaw)
	modulesList := loader.GetModules()

	hiddenVal := m.db.Get("Help", "hide", []interface{}{})
	var currentlyHidden []string
	if slice, ok := hiddenVal.([]interface{}); ok {
		for _, v := range slice {
			if s, ok := v.(string); ok {
				currentlyHidden = append(currentlyHidden, s)
			}
		}
	} else if stringSlice, ok := hiddenVal.([]string); ok {
		currentlyHidden = stringSlice
	}

	prefix := m.getPrefix(msg.SenderID)

	var responseText string
	var opts []goroku.MsgOption

	if args == "" {
		// ── List ALL modules ───────────────────────────────────────────────
		hiddenCount := 0
		if !force {
			for _, mod := range modulesList {
				modName := mod.Name()
				isModHidden := false
				for _, h := range currentlyHidden {
					if strings.EqualFold(h, modName) {
						isModHidden = true
						break
					}
				}
				if isModHidden {
					hiddenCount++
				}
			}
		}

		replyTemplate := getTrans(m.translator, m.Name(), "all_header", "<b>{} mods available, {} hidden:</b>")
		replyHeader := formatPositional(replyTemplate, len(modulesList), hiddenCount)
		shownWarn := false

		modNames := make([]string, 0, len(modulesList))
		for name := range modulesList {
			modNames = append(modNames, name)
		}
		sort.Strings(modNames)

		var coreList []string
		var plainList []string
		var noCmdsList []string

		for _, name := range modNames {
			mod := modulesList[name]
			modName := mod.Name()

			isHidden := false
			for _, h := range currentlyHidden {
				if strings.EqualFold(h, modName) {
					isHidden = true
					break
				}
			}
			if isHidden && !force {
				continue
			}

			cmds := mod.Commands()
			hasCommands := len(cmds) > 0

			if !hasCommands {
				noCmdsList = append(noCmdsList, fmt.Sprintf("\n%s <code>%s</code>", emptyEmoji, modName))
				continue
			}

			core := isCoreModule(modName)
			bullet := plainEmoji
			if core {
				bullet = coreEmoji
			}

			tmp := fmt.Sprintf("\n%s <code>%s</code>", bullet, modName)

			var allowedCmds []string
			var modCmdNames []string
			for c := range cmds {
				modCmdNames = append(modCmdNames, c)
			}
			sort.Strings(modCmdNames)

			for _, c := range modCmdNames {
				if force || dispatcher.GetSecurityManager().Check(msg, c) {
					allowedCmds = append(allowedCmds, c)
				}
			}

			if len(allowedCmds) > 0 {
				tmp += ": ( " + strings.Join(allowedCmds, " | ") + " )"
				if core {
					coreList = append(coreList, tmp)
				} else {
					plainList = append(plainList, tmp)
				}
			} else {
				if !shownWarn {
					replyTemplate := getTrans(m.translator, m.Name(), "only_allowed_warn", "<i>You have permissions to execute only these commands</i>\n")
					replyHeader = replyTemplate + replyHeader
					shownWarn = true
				}
			}
		}

		sort.Strings(coreList)
		sort.Strings(plainList)
		sort.Strings(noCmdsList)

		var contentBuilder strings.Builder
		contentBuilder.WriteString(descIcon + " " + replyHeader + "\n")

		noCmdsStr := ""
		if force {
			noCmdsStr = strings.Join(noCmdsList, "")
		}

		if onlyCore {
			contentBuilder.WriteString(fmt.Sprintf(" <blockquote expandable>%s</blockquote>", strings.Join(coreList, "")))
		} else if onlyLoaded {
			contentBuilder.WriteString(fmt.Sprintf(" <blockquote expandable>%s</blockquote>", strings.Join(plainList, "")+noCmdsStr))
		} else {
			contentBuilder.WriteString(fmt.Sprintf(" <blockquote expandable>%s</blockquote><blockquote expandable>%s</blockquote>", strings.Join(coreList, ""), strings.Join(plainList, "")+noCmdsStr))
		}

		responseText = contentBuilder.String()
	} else {
		// ── Show help for specific module ─────────────────────────────────
		exact := true
		var found goroku.Module

		found = loader.LookupByName(args)

		if found == nil {
			argsLower := strings.ToLower(args)
			for _, mod := range modulesList {
				for cmdName := range mod.Commands() {
					if strings.ToLower(cmdName) == argsLower {
						found = mod
						break
					}
				}
				if found != nil {
					break
				}
			}
		}

		if found == nil {
			var bestMod goroku.Module
			bestRatio := -1.0
			for _, mod := range modulesList {
				ratio := stringSimilarity(args, mod.Name())
				if ratio > bestRatio {
					bestRatio = ratio
					bestMod = mod
				}
			}
			if bestRatio > 0.4 {
				found = bestMod
				exact = false
			}
		}

		if found == nil {
			return msg.Answer(fmt.Sprintf("❌ Module <b>%s</b> not found.\n\nUse <code>.help</code> to see all modules.", args))
		}

		name := found.Name()
		if val, exists := found.Strings()["name"]; exists {
			name = val
		}
		name = getTrans(m.translator, found.Name(), "name", name)
		nameEsc := escapeHTMLExceptAllowed(name)

		reply := fmt.Sprintf("%s <b>%s</b>:", descIcon, nameEsc)

		defClsDoc := ""
		if val, exists := found.Strings()["_cls_doc"]; exists {
			defClsDoc = val
		}
		clsDoc := getTrans(m.translator, found.Name(), "_cls_doc", defClsDoc)
		if clsDoc != "" {
			reply += fmt.Sprintf("\n<i>ℹ️ %s\n</i>", escapeHTMLExceptAllowed(clsDoc))
		}

		cmds := found.Commands()
		var allowedCmdNames []string
		for c := range cmds {
			if force || dispatcher.GetSecurityManager().Check(msg, c) {
				allowedCmdNames = append(allowedCmdNames, c)
			}
		}
		sort.Strings(allowedCmdNames)

		var lines []string
		for _, cmd := range allowedCmdNames {
			aliases := m.findAliases(loader, cmd)
			aliasStr := ""
			if len(aliases) > 0 {
				var aliasPieces []string
				for _, alias := range aliases {
					aliasPieces = append(aliasPieces, fmt.Sprintf("<code>%s%s</code>", html.EscapeString(prefix), alias))
				}
				aliasStr = fmt.Sprintf(" (%s)", strings.Join(aliasPieces, ", "))
			}

			defDoc := getTrans(m.translator, "Help", "undoc", "🦥 No docs")
			if val, exists := found.Strings()["_cmd_doc_"+cmd]; exists {
				defDoc = val
			}
			doc := getTrans(m.translator, found.Name(), "_cmd_doc_"+cmd, defDoc)
			escapedDoc := escapeHTMLExceptAllowed(doc)

			lines = append(lines, fmt.Sprintf("%s <code>%s%s</code>%s %s", commandEmoji, html.EscapeString(prefix), cmd, aliasStr, escapedDoc))
		}

		cmdsStr := strings.Join(lines, "\n")
		if len(lines) == 0 {
			cmdsStr = "<i>No commands</i>\n"
		}

		resp := fmt.Sprintf("%s<blockquote expandable>%s</blockquote>", reply, cmdsStr)
		if !exact {
			resp += "\n" + getTrans(m.translator, "Help", "not_exact", "<tg-emoji emoji-id=5355133243773435190>☝️</tg-emoji> <b>No exact match occured, so the closest result is shown instead</b>")
		}
		if isCoreModule(found.Name()) {
			resp += "\n" + getTrans(m.translator, "Help", "core_notice", "<tg-emoji emoji-id=5355133243773435190>☝️</tg-emoji> <b>This is a core module. You can't unload it nor replace</b>")
		}

		responseText = resp
	}

	if m.bannerUrl != "" {
		responseText += fmt.Sprintf("<a href=\"%s\">&#8203;</a>", m.bannerUrl)
		if m.invertMedia {
			opts = append(opts, goroku.WithInvertMedia(true))
		}
	}

	return msg.Answer(responseText, opts...)
}

var allowedTags = regexp.MustCompile(`(?i)</?(b|i|u|s|code|pre|tg-emoji|blockquote|a|tg-spoiler)(?:\s+[^>]*)?>`)

func escapeHTMLExceptAllowed(s string) string {
	type placeholder struct {
		id  string
		tag string
	}
	var phs []placeholder
	i := 0
	result := allowedTags.ReplaceAllStringFunc(s, func(tag string) string {
		ph := fmt.Sprintf("__TAG_PH_%d__", i)
		phs = append(phs, placeholder{id: ph, tag: tag})
		i++
		return ph
	})
	result = html.EscapeString(result)
	for _, ph := range phs {
		result = strings.Replace(result, ph.id, ph.tag, 1)
	}
	return result
}
