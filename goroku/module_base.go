package goroku

import "fmt"

// Base is an embeddable implementation of everything in Module that a simple
// module does not care about. Embedding it leaves Name and Commands as the only
// methods an author must write:
//
//	type HTML struct{ goroku.Base }
//
//	func (m *HTML) Name() string { return "HTML" }
//
//	func (m *HTML) Commands() map[string]goroku.CommandHandler {
//	    return map[string]goroku.CommandHandler{"html": m.HTMLCmd}
//	}
//
//	func (m *HTML) HTMLCmd(msg *goroku.Message) error {
//	    return msg.Answer(msg.ArgsOrReply())
//	}
//
// Client, DB and Translator are populated by the loader before Init runs, so
// they are usable from Init, ClientReady, commands and watchers alike. A module
// that needs extra setup just declares its own Init; it does not have to call
// back into Base, and the fields are already there when it runs.
//
// Embedding is optional. Modules that implement Module directly are unaffected.
type Base struct {
	Client     *CustomTelegramClient
	DB         *Database
	Translator *Translator

	// self is the embedding module, needed to resolve its Name for translation
	// lookups. Set by bindBase.
	self Module
}

// NewBase builds a Base for a module constructed by hand rather than registered
// through the loader — internal helper instances, and tests.
//
// The embedding module is not known here, so T falls back to its default until
// the module is registered. Registered modules never need this.
func NewBase(client *CustomTelegramClient, db *Database) Base {
	base := Base{Client: client, DB: db}
	if client != nil {
		base.Translator = NewTranslator(client, db)
		base.Translator.Init()
	}
	return base
}

// bindBase is called by the loader before Init. It is unexported so only the
// loader can drive it, and reaches embedding types in other packages through
// method promotion.
func (b *Base) bindBase(self Module, client *CustomTelegramClient, db *Database) {
	b.self = self
	b.Client = client
	b.DB = db
	if client != nil {
		b.Translator = NewTranslator(client, db)
		b.Translator.Init()
	}
}

// baseBinder is satisfied by any module embedding Base.
type baseBinder interface {
	bindBase(self Module, client *CustomTelegramClient, db *Database)
}

// bindModuleBase wires Base if mod embeds it, and reports whether it did.
func bindModuleBase(mod Module, client *CustomTelegramClient, db *Database) bool {
	binder, ok := mod.(baseBinder)
	if !ok {
		return false
	}
	binder.bindBase(mod, client, db)
	return true
}

// Init satisfies Module for modules that need no setup of their own.
//
// The loader populates the fields before calling this, so overriding Init is
// safe and needs no super-call. Init also wires the fields itself, so a module
// constructed by hand — in a test, or as an internal helper — is usable after a
// direct Init(client, db) without going through the loader.
func (b *Base) Init(client *CustomTelegramClient, db *Database) error {
	if b.Client == nil {
		b.Client = client
	}
	if b.DB == nil {
		b.DB = db
	}
	if b.Translator == nil && client != nil {
		b.Translator = NewTranslator(client, db)
		b.Translator.Init()
	}
	return nil
}

// ClientReady runs once the Telegram client is connected. Override to react.
func (b *Base) ClientReady() error { return nil }

// OnUnload runs when the module is being removed. Override to release things.
func (b *Base) OnUnload() error { return nil }

// OnDlmod runs after the module is installed via .dlmod. Override to greet.
func (b *Base) OnDlmod() error { return nil }

// Strings returns no translatable strings. Override to declare them.
func (b *Base) Strings() map[string]string { return nil }

// Watchers returns no watchers. Override to observe every message.
func (b *Base) Watchers() []WatcherHandler { return nil }

// ModuleWithTranslationAliases lets a module accept legacy translation-file
// names in addition to the ones derived from Name.
type ModuleWithTranslationAliases interface {
	TranslationAliases() []string
}

// T returns the translated string for key, or def when no translation exists.
// It replaces the getTrans helper that was previously copied into every module.
func (b *Base) T(key, def string) string {
	if b == nil || b.Translator == nil || b.self == nil {
		return def
	}
	return TranslateModuleKey(b.Translator, b.self, key, def)
}

// TranslateModuleKey resolves goroku.modules.<module>.<key> for a module,
// trying its name as written, lowercased, snake_cased, and any legacy aliases
// the module declares.
func TranslateModuleKey(t *Translator, mod Module, key, def string) string {
	if t == nil || mod == nil {
		return def
	}
	name := mod.Name()
	candidates := []string{name, lowerASCII(name), camelToSnake(name)}
	if aliased, ok := mod.(ModuleWithTranslationAliases); ok {
		candidates = append(candidates, aliased.TranslationAliases()...)
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		if val := t.GetKey(fmt.Sprintf("goroku.modules.%s.%s", candidate, key)); val != nil {
			return fmt.Sprintf("%v", val)
		}
	}
	return def
}

func lowerASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
