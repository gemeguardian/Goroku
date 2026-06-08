package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// DANGEROUS_COMMANDS is a list of regex patterns that match destructive shell commands.
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
	stdin         io.WriteCloser
	mu            sync.Mutex
	stdout        strings.Builder
	stderr        strings.Builder
	done          bool
	startTime     time.Time
	cmdStr        string
	authMsgID     int64
	authMsgChatID int64
	authNeeded    bool
	authOngoing   bool
	user          string
}

type TerminalMod struct {
	client           *goroku.CustomTelegramClient
	db               *goroku.Database
	translator       *goroku.Translator
	sessions         sync.Map // map[string]*terminalSession keyed by "chatID/msgID"
	floodWaitProtect int
}

func (m *TerminalMod) Name() string {
	return "Terminal"
}

func (m *TerminalMod) Strings() map[string]string {
	return map[string]string{
		"name":                    "Terminal",
		"_cfg_FLOOD_WAIT_PROTECT": "Delay (in seconds) between terminal output updates to avoid floods",
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
func (m *TerminalMod) OnUnload() error    { return nil }
func (m *TerminalMod) OnDlmod() error     { return nil }

func (m *TerminalMod) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"FLOOD_WAIT_PROTECT": 2,
	}
}

func (m *TerminalMod) ConfigReady(config map[string]interface{}) error {
	if val, ok := config["FLOOD_WAIT_PROTECT"].(float64); ok {
		m.floodWaitProtect = int(val)
	} else if val, ok := config["FLOOD_WAIT_PROTECT"].(int); ok {
		m.floodWaitProtect = val
	}
	return nil
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
			Aliases: []string{"term", "sh", "cmd"},
		},
	}
}

func (m *TerminalMod) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *TerminalMod) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{
		func(msg *goroku.Message) error {
			m.sessions.Range(func(k, v interface{}) bool {
				sess := v.(*terminalSession)
				sess.mu.Lock()
				defer sess.mu.Unlock()

				if sess.done || sess.authMsgID == 0 {
					return true
				}

				if msg.ChatID == sess.authMsgChatID && msg.ID == sess.authMsgID {
					password := strings.TrimSpace(msg.Text)
					if password == "" {
						return true
					}

					authOngoingText := m.getTrans("auth_ongoing", "⏳ <b>Authenticating...</b>")
					go func(chat int64, msgID int64, text string) {
						_, _ = m.client.EditMessage(chat, msgID, text)
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
			m.sessions.Range(func(k, v interface{}) bool {
				key := k.(string)
				sess := v.(*terminalSession)
				prefix := fmt.Sprintf("%d/", msg.ChatID)
				if !strings.HasPrefix(key, prefix) {
					return true
				}
				sess.mu.Lock()
				if sess.done {
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
	if p, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && p != "" {
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

	runningText := formatTrans(m.getTrans("running", "⏳ <b>Running:</b> <code>{}</code>"), escapeHTML(cmdStr))
	_ = msg.Answer(runningText)

	cmd := exec.Command("bash", "-c", cmdStr)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		errMsg := formatTrans(m.getTrans("exec_error", "❌ <b>Failed to start command:</b> <code>{}</code>"), escapeHTML(err.Error()))
		return msg.Answer(errMsg)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		errMsg := formatTrans(m.getTrans("exec_error", "❌ <b>Failed to start command:</b> <code>{}</code>"), escapeHTML(err.Error()))
		return msg.Answer(errMsg)
	}

	stdinPipe, _ := cmd.StdinPipe()

	sess := &terminalSession{
		cmd:       cmd,
		stdin:     stdinPipe,
		startTime: time.Now(),
		cmdStr:    cmdStr,
	}

	key := msgKey(msg)
	m.sessions.Store(key, sess)
	defer m.sessions.Delete(key)

	if startErr := cmd.Start(); startErr != nil {
		errMsg := formatTrans(m.getTrans("exec_error", "❌ <b>Failed to start command:</b> <code>{}</code>"), escapeHTML(startErr.Error()))
		return msg.Answer(errMsg)
	}

	go func() {
		defer func() { recover() }()
		buf := make([]byte, 4096)
		for {
			n, readErr := stdoutPipe.Read(buf)
			if n > 0 {
				sess.mu.Lock()
				sess.stdout.Write(buf[:n])
				sess.mu.Unlock()
			}
			if readErr != nil {
				break
			}
		}
	}()

	go func() {
		defer func() { recover() }()
		buf := make([]byte, 4096)
		for {
			n, readErr := stderrPipe.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				sess.mu.Lock()
				sess.stderr.WriteString(chunk)
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
						_, _ = m.client.EditMessage(msg.ChatID, msg.ID, authNeededText)

						escapedCmd := "<code>" + escapeHTML(s.cmdStr) + "</code>"
						escapedUser := escapeHTML(s.user)
						authMsg := formatTrans(m.getTrans("auth_msg", ""), escapedCmd, escapedUser)

						sentMsg, err := m.client.SendMessage(m.client.TGID, authMsg)
						if err == nil {
							var sentID int64
							if hasID, ok := sentMsg.(interface{ GetID() int64 }); ok {
								sentID = hasID.GetID()
							} else if hasID, ok := sentMsg.(interface{ GetID() int }); ok {
								sentID = int64(hasID.GetID())
							}
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
						_, _ = m.client.EditMessage(s.authMsgChatID, s.authMsgID, failText)
						time.Sleep(2 * time.Second)
						deleteMessage(m.client, s.authMsgChatID, s.authMsgID)

						escapedCmd := "<code>" + escapeHTML(s.cmdStr) + "</code>"
						escapedUser := escapeHTML(s.user)
						authMsg := formatTrans(m.getTrans("auth_msg", ""), escapedCmd, escapedUser)

						sentMsg, err := m.client.SendMessage(m.client.TGID, authMsg)
						if err == nil {
							var sentID int64
							if hasID, ok := sentMsg.(interface{ GetID() int64 }); ok {
								sentID = hasID.GetID()
							} else if hasID, ok := sentMsg.(interface{ GetID() int }); ok {
								sentID = int64(hasID.GetID())
							}
							s.mu.Lock()
							s.authMsgID = sentID
							s.mu.Unlock()
						}
					}(sess)
				}

				if detectedLocked && sess.authNeeded && !sess.done {
					go func(s *terminalSession) {
						lockedText := m.getTrans("auth_locked", "")
						_, _ = m.client.EditMessage(s.authMsgChatID, s.authMsgID, lockedText)
						time.Sleep(3 * time.Second)
						deleteMessage(m.client, s.authMsgChatID, s.authMsgID)
						s.mu.Lock()
						s.authMsgID = 0
						s.mu.Unlock()
					}(sess)
				}
				sess.mu.Unlock()
			}
			if readErr != nil {
				break
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		tickerDuration := time.Duration(m.floodWaitProtect) * time.Second
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
				text := m.buildTerminalText(cmdStr, stdout, stderr, nil, elapsed)
				if msg.Client != nil {
					msg.Client.EditMessage(msg.ChatID, msg.ID, text) //nolint:errcheck
				}
			}
		}
	}()

	cmdErr := cmd.Wait()
	close(done)

	sess.mu.Lock()
	sess.done = true
	authMsgID := sess.authMsgID
	authChatID := sess.authMsgChatID
	stdout := m.censor(sess.stdout.String())
	stderr := m.censor(sess.stderr.String())
	sess.mu.Unlock()

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

	fullOutput := stdout
	if stderr != "" {
		if fullOutput != "" {
			fullOutput += "\n--- stderr ---\n" + stderr
		} else {
			fullOutput = stderr
		}
	}

	finalText := m.buildTerminalText(cmdStr, stdout, stderr, &rc, elapsed)

	if len(fullOutput) > 4000 {
		_ = msg.Answer("💾 <i>Output is too long. Sending as file...</i>")
		if msg.Client != nil {
			_, _ = msg.Client.SendFile(msg.ChatID, []byte(fullOutput), "terminal_output.txt")
		}
		return nil
	}

	_ = msg.Answer(finalText)
	return nil
}

func (m *TerminalMod) censor(text string) string {
	var extras []string
	if m.client != nil {
		extras = append(extras, m.client.APIHash)
	}
	if m.db != nil {
		for _, item := range [][3]string{
			{"main", "redis_uri", ""},
			{"main", "db_uri", ""},
			{"goroku.inline", "bot_token", ""},
			{"loader", "token", ""},
			{"goroku.loader", "token", ""},
		} {
			if val, ok := m.db.Get(item[0], item[1], item[2]).(string); ok {
				extras = append(extras, val)
			}
		}
	}
	return utils.CensorSensitive(text, extras...)
}

func (m *TerminalMod) buildTerminalText(cmdStr, stdout, stderr string, rc *int, elapsed time.Duration) string {
	runningText := formatTrans(m.getTrans("running", ""), escapeHTML(m.censor(cmdStr)))
	var finishedText string
	if rc != nil {
		finishedText = formatTrans(m.getTrans("finished", ""), strconv.Itoa(*rc))
	}

	stdoutHeader := m.getTrans("stdout", "")

	stdoutLen := len(stdout)
	stdoutStart := stdoutLen - 2048
	if stdoutStart < 0 {
		stdoutStart = 0
	}
	stdoutContent := escapeHTML(stdout[stdoutStart:])

	var stderrPart string
	if stderr != "" {
		stderrLen := len(stderr)
		stderrStart := stderrLen - 1024
		if stderrStart < 0 {
			stderrStart = 0
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
		killErr = sess.cmd.Process.Signal(syscall.SIGKILL)
	} else {
		killErr = sess.cmd.Process.Signal(syscall.SIGTERM)
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
