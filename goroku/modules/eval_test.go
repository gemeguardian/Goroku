package modules

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"goroku/goroku"

	"github.com/gotd/td/tg"
)

func TestMain(m *testing.M) {
	// Re-exec path: ProcessExecutor launches this test binary as the Yaegi worker.
	if IsYaegiWorkerProcess() {
		os.Exit(RunYaegiWorker())
	}
	os.Exit(m.Run())
}

func TestEvalCommandAvailableByDefaultAndSafeguarded(t *testing.T) {
	m := &Eval{client: &goroku.CustomTelegramClient{TGID: 123}}
	meta := m.CommandMetas()["eval"]
	if !meta.OnlyOwner {
		t.Fatal("eval command must remain owner-only")
	}
	if got := cap(yaegiSlots); got != 1 {
		t.Fatalf("Yaegi concurrency slots = %d, want 1", got)
	}

	// Worker has no shared memory: expressions return via result, not parent msg mutation.
	res, _, _, err := m.runYaegiEval(&goroku.Message{}, `"executed"`)
	if err != nil {
		t.Fatalf("worker eval: %v", err)
	}
	if res != "executed" {
		t.Fatalf("result = %q, want executed", res)
	}

	msg := &goroku.Message{
		RawText: `.eval "executed"`,
		Client:  &goroku.CustomTelegramClient{},
	}
	err = m.Commands()["eval"](msg)
	if !errors.Is(err, goroku.ErrClientNotInitialized) {
		t.Fatalf("EvalCmd() error = %v, want ErrClientNotInitialized from response delivery", err)
	}
	if !msg.Answered {
		t.Fatal("eval command did not reach response delivery")
	}
}

func TestEvalYaegiExpression(t *testing.T) {
	m := &Eval{
		client: &goroku.CustomTelegramClient{
			TGID: 123,
		},
	}
	res, stdout, stderr, err := m.runYaegiEval(&goroku.Message{}, "client.TGID")
	if err != nil {
		t.Fatalf("unexpected error: %v (stdout=%q stderr=%q)", err, stdout, stderr)
	}
	if res != "123" {
		t.Fatalf("expected 123, got %q", res)
	}
}

func TestEvalYaegiFormatsLiveClient(t *testing.T) {
	m := &Eval{client: &goroku.CustomTelegramClient{TGID: 123, Username: "matvey"}}
	res, _, _, err := m.runYaegiEval(&goroku.Message{}, "client")
	if err != nil {
		t.Fatalf("client expression: %v", err)
	}
	if !strings.Contains(res, "TGID:123") || !strings.Contains(res, "Username:matvey") {
		t.Fatalf("client output is not informative: %s", res)
	}
	if strings.Contains(res, "CustomTelegramClient:") {
		t.Fatalf("client output still contains a wrapper: %s", res)
	}
}

func TestEvalYaegiExposesLiveClient(t *testing.T) {
	m := &Eval{
		client: &goroku.CustomTelegramClient{
			TGID: 123,
			GorokuMe: &tg.User{
				ID:         123,
				FirstName:  "Matvey",
				Username:   "example",
				Phone:      "+15551234567",
				AccessHash: 987654321,
				Premium:    true,
			},
		},
	}

	res, _, _, err := m.runYaegiEval(&goroku.Message{}, "gorokuctx.Client.GorokuMe.ID")
	if err != nil {
		t.Fatalf("GorokuMe.ID: %v", err)
	}
	if res != "123" {
		t.Fatalf("GorokuMe.ID = %q, want 123", res)
	}

	res, _, _, err = m.runYaegiEval(&goroku.Message{}, "client.GorokuMe.Phone")
	if err != nil {
		t.Fatalf("GorokuMe.Phone: %v", err)
	}
	if res != "+15551234567" {
		t.Fatalf("GorokuMe.Phone = %q, want live client value", res)
	}

	res, _, _, err = m.runYaegiEval(&goroku.Message{}, "client.GorokuMe.AccessHash")
	if err != nil {
		t.Fatalf("GorokuMe.AccessHash: %v", err)
	}
	if res != "987654321" {
		t.Fatalf("GorokuMe.AccessHash = %q, want live client value", res)
	}
}

func TestEvalYaegiFallbackUsesCleanInterpreter(t *testing.T) {
	m := &Eval{client: &goroku.CustomTelegramClient{TGID: 123}}

	res, _, _, err := m.runYaegiEval(&goroku.Message{}, "value := client.TGID\nreturn value")
	if err != nil {
		t.Fatalf("statement fallback: %v", err)
	}
	if res != "123" {
		t.Fatalf("statement fallback result = %q, want 123", res)
	}

	_, _, _, err = m.runYaegiEval(&goroku.Message{}, "client.DoesNotExist")
	if err == nil {
		t.Fatal("unknown field unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "redeclared in this block") {
		t.Fatalf("fallback masked the useful compile error: %v", err)
	}
}

func TestEvalAndTerminalCensorSuppressOutputWhenDatabaseUnavailable(t *testing.T) {
	db := goroku.NewDatabase(2001)
	secret := "persisted-secret-value"

	for name, censor := range map[string]func(string) string{
		"terminal": (&TerminalMod{db: db}).censor,
	} {
		t.Run(name+" uninitialized", func(t *testing.T) {
			got := censor("visible " + secret)
			if got != censoredOutputUnavailable || strings.Contains(got, secret) {
				t.Fatalf("censor() = %q, want fail-closed suppression", got)
			}
		})
	}

	db = newSecurityModuleTestDatabase(t)
	if err := db.Set("main", "db_uri", secret); err != nil {
		t.Fatal(err)
	}
	terminal := &TerminalMod{db: db}
	if got := terminal.censor("visible " + secret); strings.Contains(got, secret) {
		t.Fatalf("terminal censor exposed active database secret: %q", got)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, censor := range map[string]func(string) string{
		"terminal": terminal.censor,
	} {
		t.Run(name+" closed", func(t *testing.T) {
			got := censor("visible " + secret)
			if got != censoredOutputUnavailable || strings.Contains(got, secret) {
				t.Fatalf("censor() = %q, want fail-closed suppression", got)
			}
		})
	}
}

func TestCensorMissingKeysOnActiveDatabaseKeepsOutput(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	const output = "ordinary output"
	if got := (&Eval{db: db}).censor(output); got != output {
		t.Fatalf("eval censor() = %q, want %q", got, output)
	}
	if got := (&TerminalMod{db: db}).censor(output); got != output {
		t.Fatalf("terminal censor() = %q, want %q", got, output)
	}
}

func TestEvalAndTerminalCensorAccountAndDatabaseSecretsConsistently(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	dbSecrets := map[[2]string]string{
		{"main", "redis_uri"}:          "redis-secret-value",
		{"main", "db_uri"}:             "database-secret-value",
		{"goroku.inline", "bot_token"}: "inline-secret-value",
		{"loader", "token"}:            "loader-secret-value",
		{"goroku.loader", "token"}:     "namespaced-loader-secret-value",
	}
	for key, secret := range dbSecrets {
		if err := db.Set(key[0], key[1], secret); err != nil {
			t.Fatal(err)
		}
	}

	client := &goroku.CustomTelegramClient{
		APIHash:  "telegram-api-hash-value",
		GorokuMe: &tg.User{Phone: "+15551234567"},
	}
	input := "hash telegram-api-hash-value phone +1 (555) 123-4567 number 123456789"
	for _, secret := range dbSecrets {
		input += " secret " + secret
	}

	for name, censor := range map[string]func(string) string{
		"terminal": (&TerminalMod{client: client, db: db}).censor,
	} {
		t.Run(name, func(t *testing.T) {
			got := censor(input)
			if strings.Contains(got, client.APIHash) || strings.Contains(got, "555") {
				t.Fatalf("censor exposed account/API credential: %q", got)
			}
			for _, secret := range dbSecrets {
				if strings.Contains(got, secret) {
					t.Fatalf("censor exposed database secret %q: %q", secret, got)
				}
			}
			if !strings.Contains(got, "number 123456789") {
				t.Fatalf("censor over-redacted unrelated number: %q", got)
			}
		})
	}
}

func TestBoundedBufferLimitsOutput(t *testing.T) {
	buf := newBoundedBuffer(8)
	input := strings.Repeat("x", 32)
	n, err := buf.Write([]byte(input))
	if err != nil || n != len(input) {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len(input))
	}
	if got := buf.String(); got != "xxxxxxxx\n[output truncated]" {
		t.Fatalf("unexpected bounded output %q", got)
	}
}

func TestProcessExecutorKillsProcessGroupOnTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := defaultProcessExecutor.Run(ctx, ProcessSpec{
		Name:          "bash",
		Args:          []string{"-c", "sleep 30 & wait"},
		CaptureOutput: true,
	})
	if res.Err == nil {
		t.Fatal("expected timeout error")
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut result, got %+v", res)
	}
	if res.Duration > 3*time.Second {
		t.Fatalf("timed out process took too long to stop: %v", res.Duration)
	}
}

func TestProcessExecutorOutputIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := defaultProcessExecutor.Run(ctx, ProcessSpec{
		Name:          "bash",
		Args:          []string{"-c", "for ((i=0;i<10000;i++)); do printf 0123456789; done"},
		OutputLimit:   1024,
		CaptureOutput: true,
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Truncated {
		t.Fatal("expected output to be truncated")
	}
	if got := len(res.Stdout); got != 1024 {
		t.Fatalf("stored output size = %d, want 1024", got)
	}
}

func TestProcessExecutorDistinguishesTimeoutAndExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	exitRes := defaultProcessExecutor.Run(ctx, ProcessSpec{
		Name: "bash",
		Args: []string{"-c", "exit 7"},
	})
	if exitRes.TimedOut || exitRes.ExitCode != 7 {
		t.Fatalf("exit result = %+v, want exit 7 without timeout", exitRes)
	}

	tctx, tcancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer tcancel()
	timeoutRes := defaultProcessExecutor.Run(tctx, ProcessSpec{
		Name: "bash",
		Args: []string{"-c", "sleep 30"},
	})
	if !timeoutRes.TimedOut || timeoutRes.Err == nil {
		t.Fatalf("timeout result = %+v", timeoutRes)
	}
}

func TestProcessExecutorSemaphoreBoundsConcurrency(t *testing.T) {
	exec := NewProcessExecutor(1, 1024)
	block := make(chan struct{})
	started := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd, err := exec.Command(ctx, ProcessSpec{
			Name: "bash",
			Args: []string{"-c", "sleep 30"},
		})
		if err != nil {
			t.Errorf("first acquire: %v", err)
			return
		}
		close(started)
		_ = cmd.Start()
		<-block
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		exec.Release()
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := exec.Command(ctx, ProcessSpec{Name: "true"}); err == nil {
		t.Fatal("expected second acquire to fail while slot held")
	}
	close(block)
}

func TestExecutionAuditContainsDigestNotBody(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/audit.log"
	setExecutionAuditLogPathForTest(logPath)
	t.Cleanup(func() { setExecutionAuditLogPathForTest("") })

	secretBody := "super-secret-eval-body-token-xyz"
	auditExecution(executionAuditEvent{
		ActorID:    42,
		ChatID:     7,
		Capability: "eval.go",
		Digest:     contentSHA256String(secretBody),
		Duration:   12 * time.Millisecond,
		ExitCode:   0,
		Status:     "ok",
	})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secretBody) {
		t.Fatalf("audit log leaked full body: %s", text)
	}
	digest := contentSHA256String(secretBody)
	if !strings.Contains(text, digest) {
		t.Fatalf("audit log missing digest: %s", text)
	}
	if !strings.Contains(text, "capability=eval.go") {
		t.Fatalf("audit log missing capability: %s", text)
	}
}
