package modules

import (
	"context"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// DANGEROUS_COMMANDS is a list of regex patterns that match destructive shell
// commands.
//
// This is a typo guard, NOT a security control. Pattern matching on shell text
// is trivially bypassed (`X=/etc; rm -rf $X`, base64, `eval`, aliases), and it
// is not intended to contain a hostile operator. The actual boundary is that
// .terminal is owner-only; anyone who can reach it can already run arbitrary
// commands by construction. Do not add checks here expecting them to hold
// against someone who wants to get around them.
var DANGEROUS_COMMANDS = []string{
	`rm\s+.*\s+/\s*\*?`,
	`rm\s+.*\s+/etc/`,
	`rm\s+.*\s+/dev/`,
	`rm\s+.*\s+/boot/`,
	`rm\s+.*\s+/root/`,
	`rm\s+.*\s+/sys/`,
	`rm\s+.*\s+/proc/`,
	`dd\s+.*if=.*of=/dev/`,
	`mkfs\.`,
	`fdisk\s+/dev/`,
	`\\x72\\x6d\\x20\\x2d\\x72\\x66\\x20\\x2f`,
	`which\s+rm`,
	`chmod\s+.*000\s+.*/`,
	`:\(\)\s*\{\s*:\|:&\s*\}\s*;\s*:`,
	`cat\s+.*/dev/urandom\s+>\s+/dev/[hsv]d[a-z]`,
	`ln\s+.*-s\s+/\s+/dev/null`,
}

var compiledDangerousPatterns []*regexp.Regexp

func init() {
	for _, pattern := range DANGEROUS_COMMANDS {
		compiled := regexp.MustCompile(`(?i)` + pattern)
		compiledDangerousPatterns = append(compiledDangerousPatterns, compiled)
	}
}

func isDangerous(cmd string) bool {
	for _, re := range compiledDangerousPatterns {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

type terminalSession struct {
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	stdin         io.WriteCloser
	mu            sync.Mutex
	stdout        *boundedBuffer
	stderr        *boundedBuffer
	done          bool
	startTime     time.Time
	cmdStr        string
	ownerID       int64
	authMsgID     int64
	authMsgChatID int64
	authNeeded    bool
	authOngoing   bool
	user          string
}

type terminalWriter func([]byte) (int, error)

func (w terminalWriter) Write(p []byte) (int, error) {
	return w(p)
}

type TerminalMod struct {
	client           *goroku.CustomTelegramClient
	db               *goroku.Database
	translator       *goroku.Translator
	sessions         sync.Map // map[string]*terminalSession keyed by "chatID/msgID"
	configMu         sync.RWMutex
	floodWaitProtect int
	shell            string
}

func (m *TerminalMod) Name() string {
	return "Terminal"
}

func (m *TerminalMod) Strings() map[string]string {
	return map[string]string{
		"name":                    "Terminal",
		"_cfg_FLOOD_WAIT_PROTECT": "Delay (in seconds) between terminal output updates to avoid floods",
		"_cfg_SHELL":              "Shell executable for terminal commands (auto tries bash, zsh, then sh)",
	}
}

func (m *TerminalMod) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *TerminalMod) ClientReady() error { return nil }
func (m *TerminalMod) OnUnload() error {
	m.sessions.Range(func(_, value any) bool {
		if sess, ok := value.(*terminalSession); ok && sess.cancel != nil {
			sess.cancel()
		}
		return true
	})
	return nil
}
func (m *TerminalMod) OnDlmod() error { return nil }

var _ goroku.ModuleWithConfigSchema = (*TerminalMod)(nil)

// ConfigSchema is the M7 typed config surface for Terminal.
func (m *TerminalMod) ConfigSchema() []goroku.ConfigField {
	return []goroku.ConfigField{
		{Key: "FLOOD_WAIT_PROTECT", Type: "int", Default: 2, Validator: &goroku.IntegerValidator{}},
		{Key: "SHELL", Type: "string", Default: "auto", Validator: &goroku.StringValidator{MaxLen: 4096}},
	}
}

func (m *TerminalMod) ConfigReady(config map[string]any) error {
	floodWaitProtect := 2
	switch val := config["FLOOD_WAIT_PROTECT"].(type) {
	case float64:
		floodWaitProtect = int(val)
	case int:
		floodWaitProtect = val
	case int64:
		floodWaitProtect = int(val)
	}

	shell := "auto"
	if val, ok := config["SHELL"]; ok {
		configured, ok := val.(string)
		if !ok {
			return goroku.NewConfigError(m.Name(), "SHELL", fmt.Errorf("value must be a string"))
		}
		shell = strings.TrimSpace(configured)
		if shell == "" {
			shell = "auto"
		}
		if strings.ContainsRune(shell, '\x00') {
			return goroku.NewConfigError(m.Name(), "SHELL", fmt.Errorf("value contains a NUL byte"))
		}
	}

	m.configMu.Lock()
	m.floodWaitProtect = floodWaitProtect
	m.shell = shell
	m.configMu.Unlock()
	return nil
}

func (m *TerminalMod) terminalConfig() (int, string) {
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	shell := m.shell
	if shell == "" {
		shell = "auto"
	}
	return m.floodWaitProtect, shell
}

func resolveTerminalShell(preference string, lookPath func(string) (string, error)) (string, error) {
	preference = strings.TrimSpace(preference)
	if preference == "" || strings.EqualFold(preference, "auto") {
		for _, candidate := range []string{"bash", "zsh", "sh"} {
			if path, err := lookPath(candidate); err == nil {
				return path, nil
			}
		}
		return "", fmt.Errorf("no usable shell found (tried bash, zsh, and sh)")
	}

	if !filepath.IsAbs(preference) && strings.ContainsAny(preference, `/\\`) {
		return "", fmt.Errorf("terminal shell must be an executable name or absolute path: %q", preference)
	}
	path, err := lookPath(preference)
	if err != nil {
		return "", fmt.Errorf("terminal shell %q is unavailable: %w", preference, err)
	}
	return path, nil
}

func (m *TerminalMod) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"terminal":  m.TerminalCmd,
		"terminate": m.TerminateCmd,
	}
}

func (m *TerminalMod) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"terminal": {
			Aliases:   []string{"term", "sh", "cmd"},
			OnlyOwner: true,
		},
		"terminate": {
			OnlyOwner: true,
		},
	}
}

func (m *TerminalMod) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *TerminalMod) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{
		func(msg *goroku.Message) error {
			m.sessions.Range(func(k, v any) bool {
				sess := v.(*terminalSession)
				sess.mu.Lock()
				defer sess.mu.Unlock()

				if sess.done || sess.authMsgID == 0 {
					return true
				}

				if msg.ChatID == sess.authMsgChatID && msg.ID == sess.authMsgID {
					if msg.SenderID != sess.ownerID && msg.SenderID != m.client.TGID {
						return true
					}
					password := strings.TrimSpace(msg.Text)
					if password == "" {
						return true
					}

					authOngoingText := m.getTrans("auth_ongoing", "⏳ <b>Authenticating...</b>")
					go func(chat int64, msgID int64, text string) {
						_, _ = m.client.EditMessage(goroku.ChatRefID(chat), msgID, text)
					}(sess.authMsgChatID, sess.authMsgID, authOngoingText)

					if sess.stdin != nil {
						_, _ = fmt.Fprintln(sess.stdin, password)
					}
					return false
				}
				return true
			})

			prefix := m.getPrefix()
			if msg.Text == "" || strings.HasPrefix(msg.Text, prefix) {
				return nil
			}
			m.sessions.Range(func(k, v any) bool {
				key := k.(string)
				sess := v.(*terminalSession)
				prefix := fmt.Sprintf("%d/", msg.ChatID)
				if !strings.HasPrefix(key, prefix) {
					return true
				}
				sess.mu.Lock()
				if sess.done || (msg.SenderID != sess.ownerID && msg.SenderID != m.client.TGID) {
					sess.mu.Unlock()
					return true
				}
				sess.mu.Unlock()
				if sess.stdin != nil {
					_, _ = fmt.Fprintln(sess.stdin, msg.Text)
				}
				return false
			})
			return nil
		},
	}
}

func (m *TerminalMod) getPrefix() string {
	p := m.db.GetString("goroku.main", "command_prefix", ".")
	if p != "" {
		return p
	}
	return "."
}

func msgKey(msg *goroku.Message) string {
	return fmt.Sprintf("%d/%d", msg.ChatID, msg.ID)
}

func safeTruncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i]
		}
	}
	return ""
}

var SudoPassPrompts = []string{
	"[sudo] password for",
	"[sudo] пароль для",
}

var SudoWrongPass = []string{
	`Sorry, try again`,
	`Попробуйте еще раз`,
}

var SudoTooManyTries = []string{
	`incorrect password attempts`,
	`неверные попытки ввода пароля`,
}

func (m *TerminalMod) TerminalCmd(msg *goroku.Message) error {
	cmdStr := msg.Text
	parts := strings.SplitN(cmdStr, " ", 2)
	if len(parts) > 1 {
		cmdStr = strings.TrimSpace(parts[1])
	} else {
		cmdStr = ""
	}

	if cmdStr == "" {
		return msg.Answer("⚠️ Please provide a command to run.\nUsage: <code>.terminal &lt;command&gt;</code>")
	}

	if isDangerous(cmdStr) {
		text := formatTrans(m.getTrans("dangerous_command", ""), escapeHTML(cmdStr))
		return msg.Answer(text)
	}

	floodWaitProtect, shellPreference := m.terminalConfig()
	shell, err := resolveTerminalShell(shellPreference, exec.LookPath)
	if err != nil {
		return msg.Answer(formatTrans(m.getTrans("exec_error", "❌ <b>Failed to start command:</b> <code>{}</code>"), escapeHTML(err.Error())))
	}

	runningText := formatTrans(m.getTrans("running", "⏳ <b>Running:</b> <code>{}</code>"), escapeHTML(m.censor(cmdStr)))
	_ = msg.Answer(runningText)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	// Inherit ambient env for interactive shell compatibility; process group + slot still enforced.
	cmd, err := defaultProcessExecutor.Command(ctx, ProcessSpec{
		Name:       shell,
		Args:       []string{"-c", cmdStr},
		InheritEnv: true,
	})
	if err != nil {
		return msg.Answer(formatTrans(m.getTrans("exec_error", "❌ <b>Failed to start command:</b> <code>{}</code>"), escapeHTML(err.Error())))
	}
	defer defaultProcessExecutor.Release()

	stdinPipe, _ := cmd.StdinPipe()

	sess := &terminalSession{
		cmd:       cmd,
		cancel:    cancel,
		stdin:     stdinPipe,
		stdout:    newBoundedBuffer(externalOutputLimit),
		stderr:    newBoundedBuffer(externalOutputLimit),
		startTime: time.Now(),
		cmdStr:    cmdStr,
		ownerID:   msg.SenderID,
	}

	key := msgKey(msg)
	m.sessions.Store(key, sess)
	defer m.sessions.Delete(key)

	cmd.Stdout = terminalWriter(func(p []byte) (int, error) {
		sess.mu.Lock()
		_, _ = sess.stdout.Write(p)
		sess.mu.Unlock()
		return len(p), nil
	})
	cmd.Stderr = terminalWriter(func(p []byte) (int, error) {
		chunk := string(p)
		sess.mu.Lock()
		_, _ = sess.stderr.Write([]byte(chunk))
		currentStderr := sess.stderr.String()
		sess.mu.Unlock()

		detectedPrompt := false
		detectedWrong := false
		detectedLocked := false
		var sudoUser string

		for _, prompt := range SudoPassPrompts {
			if idx := strings.Index(currentStderr, prompt); idx != -1 {
				tail := currentStderr[idx+len(prompt):]
				if colonIdx := strings.Index(tail, ":"); colonIdx != -1 {
					sudoUser = strings.TrimSpace(tail[:colonIdx])
					detectedPrompt = true
					break
				}
			}
		}

		for _, wrong := range SudoWrongPass {
			if strings.Contains(currentStderr, wrong) {
				detectedWrong = true
				break
			}
		}

		for _, locked := range SudoTooManyTries {
			if strings.Contains(currentStderr, locked) {
				detectedLocked = true
				break
			}
		}

		sess.mu.Lock()
		if detectedPrompt && !sess.authNeeded && !sess.done {
			sess.authNeeded = true
			sess.user = sudoUser
			go func(s *terminalSession) {
				authNeededText := formatTrans(m.getTrans("auth_needed", ""), strconv.FormatInt(m.client.TGID, 10))
				_, _ = m.client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, authNeededText)

				escapedCmd := "<code>" + escapeHTML(s.cmdStr) + "</code>"
				escapedUser := escapeHTML(s.user)
				authMsg := formatTrans(m.getTrans("auth_msg", ""), escapedCmd, escapedUser)

				sentMsg, err := m.client.SendMessage(goroku.ChatRefID(m.client.TGID), authMsg)
				if err == nil {
					sentID := sentMsg.SentMessageID()
					s.mu.Lock()
					s.authMsgID = sentID
					s.authMsgChatID = m.client.TGID
					s.mu.Unlock()
				}
			}(sess)
		}

		if detectedWrong && sess.authNeeded && !sess.done {
			go func(s *terminalSession) {
				failText := m.getTrans("auth_fail", "")
				_, _ = m.client.EditMessage(goroku.ChatRefID(s.authMsgChatID), s.authMsgID, failText)
				time.Sleep(2 * time.Second)
				deleteMessage(m.client, s.authMsgChatID, s.authMsgID)

				escapedCmd := "<code>" + escapeHTML(s.cmdStr) + "</code>"
				escapedUser := escapeHTML(s.user)
				authMsg := formatTrans(m.getTrans("auth_msg", ""), escapedCmd, escapedUser)

				sentMsg, err := m.client.SendMessage(goroku.ChatRefID(m.client.TGID), authMsg)
				if err == nil {
					sentID := sentMsg.SentMessageID()
					s.mu.Lock()
					s.authMsgID = sentID
					s.mu.Unlock()
				}
			}(sess)
		}

		if detectedLocked && sess.authNeeded && !sess.done {
			go func(s *terminalSession) {
				lockedText := m.getTrans("auth_locked", "")
				_, _ = m.client.EditMessage(goroku.ChatRefID(s.authMsgChatID), s.authMsgID, lockedText)
				time.Sleep(3 * time.Second)
				deleteMessage(m.client, s.authMsgChatID, s.authMsgID)
				s.mu.Lock()
				s.authMsgID = 0
				s.mu.Unlock()
			}(sess)
		}
		sess.mu.Unlock()
		return len(p), nil
	})

	if startErr := cmd.Start(); startErr != nil {
		errMsg := formatTrans(m.getTrans("exec_error", "❌ <b>Failed to start command:</b> <code>{}</code>"), escapeHTML(startErr.Error()))
		return msg.Answer(errMsg)
	}

	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		tickerDuration := time.Duration(floodWaitProtect) * time.Second
		if tickerDuration < 1*time.Second {
			tickerDuration = 1 * time.Second
		}
		ticker := time.NewTicker(tickerDuration)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				sess.mu.Lock()
				stdout := m.censor(sess.stdout.String())
				stderr := m.censor(sess.stderr.String())
				sess.mu.Unlock()

				elapsed := time.Since(sess.startTime)
				text := m.buildTerminalText(cmdStr, stdout, stderr, nil, elapsed, true)
				if msg.Client != nil {
					msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, text) //nolint:errcheck,gosec
				}
			}
		}
	}()

	cmdErr := cmd.Wait()
	timedOut := ctx.Err() == context.DeadlineExceeded
	cancel()
	close(done)

	sess.mu.Lock()
	sess.done = true
	authMsgID := sess.authMsgID
	authChatID := sess.authMsgChatID
	stdout := m.censor(sess.stdout.String())
	stderr := m.censor(sess.stderr.String())
	truncated := sess.stdout.Truncated() || sess.stderr.Truncated()
	sess.mu.Unlock()
	if timedOut {
		if stderr != "" {
			stderr += "\n"
		}
		stderr += "command timed out after 15 minutes"
	}

	if authMsgID != 0 {
		go deleteMessage(m.client, authChatID, authMsgID)
	}

	elapsed := time.Since(sess.startTime)

	rc := 0
	if cmdErr != nil {
		if exitError, ok := cmdErr.(*exec.ExitError); ok {
			rc = exitError.ExitCode()
		} else {
			rc = -1
		}
	}
	auditExecution(executionAuditEvent{
		ActorID:    msg.SenderID,
		ChatID:     msg.ChatID,
		Capability: "terminal",
		Digest:     contentSHA256String(cmdStr),
		Duration:   elapsed,
		ExitCode:   rc,
		TimedOut:   timedOut,
		Truncated:  truncated,
		Status:     auditStatus(cmdErr, timedOut, false),
	})

	finalText := m.buildTerminalText(cmdStr, stdout, stderr, &rc, elapsed, false)
	_ = msg.Answer(finalText)
	return nil
}

func (m *TerminalMod) censor(text string) string {
	return censorExecutionOutput(text, m.client, m.db)
}

func (m *TerminalMod) buildTerminalText(cmdStr, stdout, stderr string, rc *int, elapsed time.Duration, truncateOutput bool) string {
	runningText := formatTrans(m.getTrans("running", ""), escapeHTML(m.censor(cmdStr)))
	var finishedText string
	if rc != nil {
		finishedText = formatTrans(m.getTrans("finished", ""), strconv.Itoa(*rc))
	}

	stdoutHeader := m.getTrans("stdout", "")

	stdoutStart := 0
	if truncateOutput && len(stdout) > 2048 {
		stdoutStart = len(stdout) - 2048
	}
	stdoutContent := escapeHTML(stdout[stdoutStart:])

	var stderrPart string
	if stderr != "" {
		stderrStart := 0
		if truncateOutput && len(stderr) > 1024 {
			stderrStart = len(stderr) - 1024
		}
		stderrContent := escapeHTML(stderr[stderrStart:])
		stderrPart = m.getTrans("stderr", "") + stderrContent
	}

	endText := m.getTrans("end", "")

	var timeExecText string
	if rc != nil {
		execSeconds := fmt.Sprintf("%.2f", elapsed.Seconds())
		timeExecText = formatTrans(m.getTrans("time_exec", ""), execSeconds)
	}

	return runningText + finishedText + stdoutHeader + stdoutContent + stderrPart + endText + timeExecText
}

func (m *TerminalMod) TerminateCmd(msg *goroku.Message) error {
	if msg.ReplyToMsgID == 0 {
		_ = msg.Answer(m.getTrans("what_to_kill", ""))
		return nil
	}

	replyMsg, err := msg.GetReplyMessage()
	if err != nil || replyMsg == nil {
		_ = msg.Answer(m.getTrans("what_to_kill", ""))
		return nil
	}

	key := fmt.Sprintf("%d/%d", replyMsg.ChatID, replyMsg.ID)
	val, exists := m.sessions.Load(key)
	if !exists {
		_ = msg.Answer(m.getTrans("no_cmd", ""))
		return nil
	}

	sess := val.(*terminalSession)
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.done || sess.cmd == nil || sess.cmd.Process == nil {
		_ = msg.Answer(m.getTrans("no_cmd", ""))
		return nil
	}

	var killErr error
	if strings.Contains(utils.GetArgsRaw(msg.Text), "-f") {
		killErr = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGKILL)
	} else {
		killErr = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGTERM)
	}

	if killErr != nil {
		_ = msg.Answer(m.getTrans("kill_fail", ""))
	} else {
		_ = msg.Answer(m.getTrans("killed", ""))
	}
	return nil
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func deleteMessage(client *goroku.CustomTelegramClient, chatID, msgID int64) {
	msg := &goroku.Message{
		ID:     msgID,
		ChatID: chatID,
		Client: client,
	}
	_ = msg.Delete()
}
