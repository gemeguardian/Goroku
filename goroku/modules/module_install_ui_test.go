package modules

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"goroku/goroku"
)

type installCardTestModule struct {
	name     string
	strings  map[string]string
	commands map[string]goroku.CommandHandler
	metas    map[string]goroku.CommandMeta
}

func (m *installCardTestModule) Name() string               { return m.name }
func (m *installCardTestModule) Strings() map[string]string { return m.strings }
func (m *installCardTestModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error {
	return nil
}
func (m *installCardTestModule) ClientReady() error                          { return nil }
func (m *installCardTestModule) OnUnload() error                             { return nil }
func (m *installCardTestModule) OnDlmod() error                              { return nil }
func (m *installCardTestModule) Commands() map[string]goroku.CommandHandler  { return m.commands }
func (m *installCardTestModule) Watchers() []goroku.WatcherHandler           { return nil }
func (m *installCardTestModule) CommandMetas() map[string]goroku.CommandMeta { return m.metas }

func installCardHandler(*goroku.Message) error { return nil }

func TestFormatModuleInstalledCardEscapesNamesDocsAndShowsRuntime(t *testing.T) {
	mod := &installCardTestModule{
		name: "Runtime<Core>",
		strings: map[string]string{
			"name":           "Display & <b>unsafe</b>",
			"_cmd_doc_alpha": "Run <all> & safely",
		},
		commands: map[string]goroku.CommandHandler{"zeta": installCardHandler, "alpha": installCardHandler},
		metas:    map[string]goroku.CommandMeta{"alpha": {Aliases: []string{"a2", "a1"}}},
	}
	card := formatModuleInstalledCard(mod, "!", "URL: https://example.test/a&b.go", nil, defaultLoadedTemplate, defaultCommandEmoji, "No docs")

	for _, want := range []string{
		"Display &amp; &lt;b&gt;unsafe&lt;/b&gt;",
		"URL: https://example.test/a&amp;b.go",
		"<tg-emoji emoji-id=5134452506935427991>🪐</tg-emoji>",
		defaultCommandEmoji,
		"<code>!alpha</code>",
		"Run &lt;all&gt; &amp; safely",
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q:\n%s", want, card)
		}
	}
	if strings.Index(card, "!alpha") > strings.Index(card, "!zeta") {
		t.Fatalf("commands are not sorted:\n%s", card)
	}
}

func TestFormatModuleInstalledCardNoCommandsAndWarning(t *testing.T) {
	mod := &installCardTestModule{name: "Quiet", strings: map[string]string{}, commands: map[string]goroku.CommandHandler{}}
	card := formatModuleInstalledCard(mod, ".", "Local file", errors.New("sync <uncertain>"), defaultLoadedTemplate, defaultCommandEmoji, "No docs")
	if !strings.Contains(card, "sync &lt;uncertain&gt;") {
		t.Fatalf("warning was not escaped:\n%s", card)
	}
	if strings.Contains(card, "Local file") || strings.Contains(card, ">🌐</tg-emoji>") {
		t.Fatalf("local installs must not show a source row:\n%s", card)
	}
}

func TestFormatModuleInstalledCardTruncatesCommandsAndDocs(t *testing.T) {
	commands := make(map[string]goroku.CommandHandler)
	stringsMap := map[string]string{"name": "Large"}
	for i := 0; i < moduleCardCommandLimit+3; i++ {
		name := fmt.Sprintf("cmd%02d", i)
		commands[name] = installCardHandler
		stringsMap["_cmd_doc_"+name] = strings.Repeat("x", moduleCardDocLimit+40)
	}
	mod := &installCardTestModule{name: "Large", strings: stringsMap, commands: commands}
	card := formatModuleInstalledCard(mod, ".", "Repository: example.test", nil, defaultLoadedTemplate, defaultCommandEmoji, "No docs")

	if strings.Count(card, "<code>.cmd") != moduleCardCommandLimit {
		t.Fatalf("rendered command count = %d, want %d", strings.Count(card, "<code>.cmd"), moduleCardCommandLimit)
	}
	if !strings.Contains(card, "+3 · <code>.help Large</code>") {
		t.Fatalf("missing remaining command count:\n%s", card)
	}
	if strings.Contains(card, strings.Repeat("x", moduleCardDocLimit+1)) || !strings.Contains(card, strings.Repeat("x", moduleCardDocLimit-3)+"...") {
		t.Fatalf("documentation was not truncated:\n%s", card)
	}
}

func TestSanitizedModuleSourceRemovesCredentialsAndQuery(t *testing.T) {
	got := sanitizedModuleSource(moduleSourceURL, "https://user:secret@example.test/mod.go?token=hidden#fragment")
	if got != "URL: https://example.test/mod.go" {
		t.Fatalf("sanitized source = %q", got)
	}
}
