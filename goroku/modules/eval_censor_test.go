package modules

import (
	"strings"
	"testing"

	"goroku/goroku"
)

// .terminal masked credentials in its output; .eval shipped them verbatim, so
// an owner running .eval in a public chat published whatever it printed.
func TestEvalOutputIsCensored(t *testing.T) {
	m := newEvalSecretsTestModule(t)

	output := "token=" + testBotToken + " redis=" + testRedisURI
	got := m.censor(output)

	for _, secret := range evalSecrets() {
		if strings.Contains(got, secret) {
			t.Fatalf("eval output still carries a secret: %q", got)
		}
	}
}

// The two commands must not disagree about what counts as a secret.
func TestEvalAndTerminalCensorAgree(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	if err := db.Set("goroku.inline", "bot_token", testBotToken); err != nil {
		t.Fatal(err)
	}
	client := goroku.NewCustomTelegramClient(99)
	client.GorokuDB = db

	eval := &Eval{Base: goroku.Base{Client: client, DB: db}}
	terminal := &TerminalMod{Base: goroku.Base{Client: client, DB: db}}

	input := "printed " + testBotToken
	if eval.censor(input) != terminal.censor(input) {
		t.Fatalf("eval censor = %q, terminal censor = %q", eval.censor(input), terminal.censor(input))
	}
}
