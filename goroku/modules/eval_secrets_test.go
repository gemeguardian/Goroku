package modules

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"goroku/goroku"
)

const (
	testBotToken = "1234567:AA-not-a-real-bot-token-value"
	testRedisURI = "redis://user:not-a-real-password@127.0.0.1:6379/0"
	testDBURI    = "postgres://user:not-a-real-password@127.0.0.1:5432/goroku"
)

// newEvalSecretsTestModule returns an Eval wired to a database holding the
// values censorExecutionOutput masks, so a leak is detectable by substring.
func newEvalSecretsTestModule(t *testing.T) *Eval {
	t.Helper()
	db := newSecurityModuleTestDatabase(t)
	for _, kv := range [][3]string{
		{"goroku.inline", "bot_token", testBotToken},
		{"main", "redis_uri", testRedisURI},
		{"main", "db_uri", testDBURI},
	} {
		if err := db.Set(kv[0], kv[1], kv[2]); err != nil {
			t.Fatal(err)
		}
	}
	client := goroku.NewCustomTelegramClient(99)
	client.GorokuDB = db
	return &Eval{Base: goroku.Base{Client: client, DB: db}}
}

func evalSecrets() []string {
	return []string{testBotToken, testRedisURI, testDBURI}
}

// Process arguments are world-readable on Linux via /proc/<pid>/cmdline, so the
// eval context — which carries the whole database dump — must never be an
// argument.
func TestPythonEvalSpecKeepsSecretsOutOfArgs(t *testing.T) {
	m := newEvalSecretsTestModule(t)
	msg := &goroku.Message{ID: 1, ChatID: 2, SenderID: 3, Text: ".evalpy 1", RawText: ".evalpy 1", Client: m.Client}

	spec, err := m.buildPythonEvalSpec(msg, "1")
	if err != nil {
		t.Fatal(err)
	}

	for i, arg := range spec.Args {
		for _, secret := range evalSecrets() {
			if strings.Contains(arg, secret) {
				t.Fatalf("Args[%d] carries a secret from the database", i)
			}
		}
	}
	if len(spec.Args) != 1 || spec.Args[0] != "-" {
		t.Fatalf("Args = %q, want the script to be read from stdin", spec.Args)
	}
	if spec.Stdin == nil {
		t.Fatal("script is not passed on stdin")
	}
}

// Even out of argv, the secrets have no business inside the interpreter: the
// evaluated code could just print them back out of its own snapshot.
func TestPythonEvalContextRedactsSecrets(t *testing.T) {
	m := newEvalSecretsTestModule(t)
	msg := &goroku.Message{ID: 1, ChatID: 2, SenderID: 3, Text: ".evalpy 1", RawText: ".evalpy 1", Client: m.Client}

	spec, err := m.buildPythonEvalSpec(msg, "1")
	if err != nil {
		t.Fatal(err)
	}

	ctxJSON := ""
	for _, kv := range spec.ExtraEnv {
		if after, ok := strings.CutPrefix(kv, evalContextEnvVar+"="); ok {
			ctxJSON = after
		}
	}
	if ctxJSON == "" {
		t.Fatalf("eval context is not in the environment: %q", spec.ExtraEnv)
	}
	for _, secret := range evalSecrets() {
		if strings.Contains(ctxJSON, secret) {
			t.Fatal("a secret reached the eval context")
		}
	}

	var decoded struct {
		DB map[string]map[string]any `json:"db"`
	}
	if err := json.Unmarshal([]byte(ctxJSON), &decoded); err != nil {
		t.Fatalf("eval context is not valid JSON: %v", err)
	}
	if got := decoded.DB["goroku.inline"]["bot_token"]; got != evalRedactedPlaceholder {
		t.Fatalf("goroku.inline.bot_token = %v, want %q", got, evalRedactedPlaceholder)
	}
	if got := decoded.DB["main"]["redis_uri"]; got != evalRedactedPlaceholder {
		t.Fatalf("main.redis_uri = %v, want %q", got, evalRedactedPlaceholder)
	}
}

// The Yaegi worker gets its snapshot over a pipe to another process; the same
// values must be stripped there.
func TestYaegiRequestRedactsSecrets(t *testing.T) {
	m := newEvalSecretsTestModule(t)
	req := m.buildYaegiRequest(&goroku.Message{ID: 1}, "1")

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range evalSecrets() {
		if strings.Contains(string(payload), secret) {
			t.Fatal("a secret reached the Yaegi worker request")
		}
	}
}

// Redaction edits the dump in place, so Dump() has to hand out a copy or the
// bot would lose its own bot token on the first .evalpy.
func TestDatabaseDumpReturnsCopy(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	if err := db.Set("goroku.inline", "bot_token", testBotToken); err != nil {
		t.Fatal(err)
	}

	dump := db.Dump()
	dump["goroku.inline"]["bot_token"] = "mutated"

	raw, err := db.Get("goroku.inline", "bot_token", "")
	if err != nil {
		t.Fatal(err)
	}
	if raw != testBotToken {
		t.Fatalf("bot_token = %v after mutating Dump() result, want the original", raw)
	}
}

// redactedDBDump must not disarm the live database either.
func TestRedactedDBDumpLeavesDatabaseIntact(t *testing.T) {
	m := newEvalSecretsTestModule(t)

	if got := redactedDBDump(m.DB)["goroku.inline"]["bot_token"]; got != evalRedactedPlaceholder {
		t.Fatalf("redacted dump bot_token = %v, want %q", got, evalRedactedPlaceholder)
	}
	raw, err := m.DB.Get("goroku.inline", "bot_token", "")
	if err != nil {
		t.Fatal(err)
	}
	if raw != testBotToken {
		t.Fatalf("bot_token = %v in the live database, want the original", raw)
	}
}

// End-to-end check that the launch still works after moving the script to
// stdin and the context to the environment.
func TestPythonEvalRunsWithScriptOnStdin(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	m := newEvalSecretsTestModule(t)
	msg := &goroku.Message{ID: 1, ChatID: 2, SenderID: 3, Text: ".evalpy 1 + 1", RawText: ".evalpy 1 + 1", Client: m.Client}

	res, err := m.runPythonEval(msg, "1 + 1")
	if err != nil {
		t.Fatalf("runPythonEval: %v", err)
	}
	if res.Traceback != "" {
		t.Fatalf("python traceback: %s", res.Traceback)
	}
	if res.Result == nil || *res.Result != "2" {
		t.Fatalf("result = %v, want 2", res.Result)
	}

	// The context must still arrive: the redacted marker proves db reached it.
	res, err = m.runPythonEval(msg, `db["goroku.inline"]["bot_token"]`)
	if err != nil {
		t.Fatalf("runPythonEval with db access: %v", err)
	}
	if res.Result == nil || *res.Result != evalRedactedPlaceholder {
		t.Fatalf("db bot_token in eval = %v, want %q (traceback: %s)", res.Result, evalRedactedPlaceholder, res.Traceback)
	}
}
