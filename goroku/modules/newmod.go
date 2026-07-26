package modules

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"unicode"

	"goroku/goroku"
)

// maxNewModuleNameLen keeps generated identifiers and file names sane.
const maxNewModuleNameLen = 40

// validateNewModuleName checks that name can be used both as a Go type name and
// as a command word. The returned error is written straight to the user, so it
// says what to do rather than what went wrong internally.
func validateNewModuleName(name string) error {
	if name == "" {
		return fmt.Errorf("give the module a name, for example <code>.newmod Weather</code>")
	}
	if len(name) > maxNewModuleNameLen {
		return fmt.Errorf("name is longer than %d characters", maxNewModuleNameLen)
	}
	for i, r := range name {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII:
		case r == '_':
		case unicode.IsDigit(r) && i > 0:
		default:
			return fmt.Errorf("name may only contain latin letters, digits and underscores, and may not start with a digit")
		}
	}
	if r := rune(name[0]); !unicode.IsUpper(r) {
		return fmt.Errorf("name must start with a capital letter, for example <code>Weather</code>")
	}
	if token.Lookup(name).IsKeyword() {
		return fmt.Errorf("%q is a Go keyword", name)
	}
	return nil
}

// newModuleSource renders a ready-to-load module skeleton. The result compiles
// and does something visible as-is, so a new author edits working code instead
// of filling in a blank.
func newModuleSource(name string) string {
	command := strings.ToLower(name)
	var b strings.Builder
	fmt.Fprintf(&b, `package modules

import "goroku/goroku"

// %[1]s is a Goroku module.
//
// Embedding goroku.Base supplies everything a module needs but rarely cares
// about: the Client, DB and Translator fields, the Init/ClientReady/OnUnload/
// OnDlmod lifecycle, and the T translation helper. Name and Commands below are
// the only methods that are actually required.
type %[1]s struct {
	goroku.Base
}

// Name is how the module appears in .help and .unloadmod.
func (m *%[1]s) Name() string { return %[1]q }

// Strings are optional. Keys starting with _cls_doc and _cmd_doc_ document the
// module and its commands; the rest are user-facing text that m.T can
// translate.
func (m *%[1]s) Strings() map[string]string {
	return map[string]string{
		"name":              %[1]q,
		"_cls_doc":          "What %[1]s does",
		"_cmd_doc_%[2]s": "What .%[2]s does",
		"no_args":           "❌ <b>Send some text or reply to a message</b>",
	}
}

// Commands maps command words to handlers. Users invoke them with the bot
// prefix, so this registers .%[2]s
func (m *%[1]s) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		%[2]q: m.%[1]sCmd,
	}
}

// %[1]sCmd handles .%[2]s
//
// Inside a handler you can use:
//   msg.Args()        - text after the command
//   msg.ArgsOrReply() - that text, or the replied-to message when empty
//   msg.Answer(text)  - edit your own message, or reply to someone else's
//   m.Client, m.DB    - Telegram client and persistent storage
//   m.T(key, default) - translated string from Strings above
func (m *%[1]s) %[1]sCmd(msg *goroku.Message) error {
	input := msg.ArgsOrReply()
	if input == "" {
		return msg.Answer(m.T("no_args", "❌ <b>Send some text or reply to a message</b>"))
	}
	return msg.Answer("<b>%[1]s got:</b> <code>" + input + "</code>")
}
`, name, command)
	return b.String()
}

// NewmodCmd generates a module skeleton and sends it back as a .go file the
// user can edit and install with .loadmod.
func (m *LoaderModule) NewmodCmd(msg *goroku.Message) error {
	name := strings.TrimSpace(msg.Args())
	if fields := strings.Fields(name); len(fields) > 0 {
		name = fields[0]
	}

	if err := validateNewModuleName(name); err != nil {
		return msg.Answer(formatTrans(
			m.T("newmod_bad_name", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>{}</b>"),
			err.Error()))
	}

	if m.Client != nil && m.Client.Loader != nil {
		if m.Client.Loader.WithModule(name, func(goroku.Module) {}) {
			return msg.Answer(formatTrans(
				m.T("newmod_exists", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>A module named {} is already loaded</b>"),
				name))
		}
	}

	source := newModuleSource(name)
	// Guard against shipping a skeleton that will not parse; a template typo
	// would otherwise only surface when the user tries to load it.
	if _, err := parser.ParseFile(token.NewFileSet(), name+".go", source, parser.SkipObjectResolution); err != nil {
		return fmt.Errorf("generated skeleton for %s does not parse: %w", name, err)
	}

	caption := formatTrans(
		m.T("newmod_ready", "<tg-emoji emoji-id=5873204392429096339>📄</tg-emoji> <b>Skeleton for {} is ready.</b>\n\nEdit it, then reply to the file with <code>.loadmod</code> to install."),
		name)

	nr := &namedReader{r: bytes.NewReader([]byte(source)), name: name + ".go"}
	if m.Client == nil {
		return fmt.Errorf("client is not initialized")
	}
	if _, err := m.Client.SendFile(goroku.ChatRefID(msg.ChatID), nr, caption); err != nil {
		return fmt.Errorf("send module skeleton: %w", err)
	}
	return nil
}
