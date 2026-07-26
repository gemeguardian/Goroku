package modules

import (
	"testing"

	"goroku/goroku"
)

// newEscalationTestClient wires a client with a real dispatcher so
// GorokuSecurity.getSecurityManager() resolves to a live SecurityManager.
func newEscalationTestClient(t *testing.T) (*goroku.CustomTelegramClient, *goroku.Database) {
	t.Helper()
	db := newSecurityModuleTestDatabase(t)
	if err := db.Set("goroku.security", "owner", []any{int64(99)}); err != nil {
		t.Fatal(err)
	}
	client := goroku.NewCustomTelegramClient(99)
	client.GorokuDB = db
	loader := goroku.NewModules(client, db)
	client.Loader = loader
	dispatcher, err := goroku.NewCommandDispatcherChecked(loader, client, db)
	if err != nil {
		t.Fatal(err)
	}
	loader.SetDispatcher(dispatcher)
	t.Cleanup(dispatcher.GetSecurityManager().Stop)
	return client, db
}

// Every command that rewrites security policy must be owner-only, otherwise a
// module-scoped delegation of GorokuSecurity hands the delegate the means to
// rewrite the owner list. Read-only commands must stay delegatable.
func TestSecurityCommandMetasMarkPolicyChangesOwnerOnly(t *testing.T) {
	m := &GorokuSecurity{}
	metas := m.CommandMetas()

	mutating := []string{
		"owneradd", "ownerrm", "sudoadd", "sudorm",
		"tsec", "tsecrm", "tsecclr",
		"newsgroup", "delsgroup", "sgroupadd", "sgroupdel",
	}
	readOnly := []string{"ownerlist", "sudolist", "sgroups", "sgroup", "security", "inlinesec", "querysec"}

	for _, cmd := range mutating {
		if !metas[cmd].OnlyOwner {
			t.Errorf("command %q changes security policy but is not OnlyOwner", cmd)
		}
	}
	for _, cmd := range readOnly {
		if metas[cmd].OnlyOwner {
			t.Errorf("read-only command %q became OnlyOwner; delegation of read access broke", cmd)
		}
	}

	classified := make(map[string]bool, len(mutating)+len(readOnly))
	for _, cmd := range append(append([]string{}, mutating...), readOnly...) {
		classified[cmd] = true
	}
	for cmd := range m.Commands() {
		if !classified[cmd] {
			t.Errorf("command %q is new and unclassified: decide whether it changes security policy", cmd)
		}
	}
}

// Defence in depth: the handler proves ownership itself, so one missing
// OnlyOwner tag or one over-broad rule is not enough to promote a user.
func TestAddownerCmdRejectsNonOwner(t *testing.T) {
	client, db := newEscalationTestClient(t)
	m := &GorokuSecurity{}
	if err := m.Init(client, db); err != nil {
		t.Fatal(err)
	}

	msg := &goroku.Message{
		SenderID:  100,
		ChatID:    100,
		IsPrivate: true,
		Client:    client,
		Text:      ".owneradd 555",
		RawText:   ".owneradd 555",
	}
	// The answer itself cannot be delivered without a connection; only the
	// effect on the owner list matters here.
	_ = m.AddownerCmd(msg)

	sm := client.Loader.GetDispatcher().GetSecurityManager()
	if sm.IsOwner(555) {
		t.Fatal("a non-owner promoted a user to owner through .owneradd")
	}
}

func TestAddownerCmdAcceptsOwner(t *testing.T) {
	client, db := newEscalationTestClient(t)
	m := &GorokuSecurity{}
	if err := m.Init(client, db); err != nil {
		t.Fatal(err)
	}

	msg := &goroku.Message{
		SenderID:  99,
		ChatID:    99,
		IsPrivate: true,
		Client:    client,
		Text:      ".owneradd 555",
		RawText:   ".owneradd 555",
	}
	_ = m.AddownerCmd(msg)

	sm := client.Loader.GetDispatcher().GetSecurityManager()
	if !sm.IsOwner(555) {
		t.Fatal("owner could no longer add an owner")
	}
}
