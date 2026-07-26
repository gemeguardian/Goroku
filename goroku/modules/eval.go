package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const externalOutputLimit = 256 * 1024

const censoredOutputUnavailable = "[output suppressed: database unavailable]"

var (
	yaegiSlots     = make(chan struct{}, 1)
	errEvalTimeout = errors.New("eval timeout")
)

type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer { return &boundedBuffer{limit: limit} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return n, nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if b.truncated {
		s += "\n[output truncated]"
	}
	return s
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func acquireSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Eval struct {
	goroku.Base
}

func (m *Eval) Name() string {
	return "Eval"
}

func (m *Eval) Strings() map[string]string {
	return map[string]string{
		"name": "Evaluator",
	}
}

func (m *Eval) OnUnload() error { return nil }

func (m *Eval) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"eval":   m.EvalCmd,
		"evalpy": m.EvalPyCmd,
		"ec":     m.ECCmd,
		"ecpp":   m.ECPPCmd,
		"enode":  m.ENodeCmd,
		"ephp":   m.EPHPCmd,
		"eruby":  m.ERubyCmd,
		"ebf":    m.EBFCmd,
		"erust":  m.ERustCmd,
	}
}

func (m *Eval) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"eval": {
			Aliases:   []string{"e"},
			OnlyOwner: true,
		},
		"evalpy": {
			Aliases:   []string{"epy", "py"},
			OnlyOwner: true,
		},
		"ec": {
			OnlyOwner: true,
		},
		"ecpp": {
			OnlyOwner: true,
		},
		"enode": {
			OnlyOwner: true,
		},
		"ephp": {
			Aliases:   []string{"php"},
			OnlyOwner: true,
		},
		"eruby": {
			Aliases:   []string{"ruby"},
			OnlyOwner: true,
		},
		"ebf": {
			Aliases:   []string{"bf"},
			OnlyOwner: true,
		},
		"erust": {
			Aliases:   []string{"rust"},
			OnlyOwner: true,
		},
	}
}

func (m *Eval) censor(text string) string {
	return text
}

// evalContextEnvVar carries the eval context to the child interpreter. It is
// popped by the script before user code runs.
const evalContextEnvVar = "GOROKU_EVAL_CONTEXT"

// evalRedactedPlaceholder replaces a secret in an eval context.
const evalRedactedPlaceholder = "[REDACTED]"

// evalRedactedKeys never reach an eval context under any circumstances. These
// are the same values censorExecutionOutput masks in command output: handing
// them to the interpreter would make the masking pointless, since the code
// could simply print them from its own database snapshot.
var evalRedactedKeys = map[string][]string{
	"goroku.inline": {"bot_token"},
	"main":          {"redis_uri", "db_uri"},
	"loader":        {"token"},
	"goroku.loader": {"token"},
}

// redactedDBDump returns the database dump with evalRedactedKeys blanked.
// Dump() already returns a deep copy, so the live database is untouched.
func redactedDBDump(db *goroku.Database) map[string]map[string]any {
	if db == nil {
		return nil
	}
	dump := db.Dump()
	for owner, section := range dump {
		for redactedOwner, keys := range evalRedactedKeys {
			if !strings.EqualFold(owner, redactedOwner) {
				continue
			}
			for _, key := range keys {
				for existing := range section {
					if strings.EqualFold(existing, key) {
						section[existing] = evalRedactedPlaceholder
					}
				}
			}
		}
	}
	return dump
}

func censorExecutionOutput(text string, client *goroku.CustomTelegramClient, db *goroku.Database) string {
	var extras []string
	var phones []string
	if client != nil {
		extras = append(extras, client.APIHash)
		if u := client.GorokuMe; u != nil && u.Phone != "" {
			phones = append(phones, u.Phone)
		}
	}
	if db != nil {
		for _, item := range [][3]string{
			{"main", "redis_uri", ""},
			{"main", "db_uri", ""},
			{"goroku.inline", "bot_token", ""},
			{"loader", "token", ""},
			{"goroku.loader", "token", ""},
		} {
			raw, err := db.Get(item[0], item[1], item[2])
			if err != nil {
				return censoredOutputUnavailable
			}
			if val, ok := raw.(string); ok {
				extras = append(extras, val)
			}
		}
	}
	return utils.CensorSensitiveWithPhones(text, phones, extras...)
}

func formatPythonTraceback(tb string) string {
	tb = strings.Replace(tb, "Traceback (most recent call last):\n", "", 1)
	lines := strings.Split(tb, "\n")

	// Remove empty trailing lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	fileRegex := regexp.MustCompile(`^\s*File "([^"]+)", line ([0-9]+), in (.+)`)

	var formatted []string
	for _, line := range lines {
		matches := fileRegex.FindStringSubmatch(line)
		if len(matches) == 4 {
			filename := matches[1]
			lineno := matches[2]
			name := matches[3]
			formatted = append(formatted, fmt.Sprintf("👉 <code>%s:%s</code> <b>in</b> <code>%s</code>", utils.EscapeHTML(filename), lineno, utils.EscapeHTML(name)))
		} else {
			formatted = append(formatted, fmt.Sprintf("<code>%s</code>", utils.EscapeHTML(line)))
		}
	}

	if len(formatted) > 1 {
		mainLines := formatted[:len(formatted)-1]
		errLine := formatted[len(formatted)-1]
		return strings.Join(mainLines, "\n") + "\n\n🚫 " + errLine
	}

	return "🚫 " + formatted[0]
}

func evalCodeFromMessage(msg *goroku.Message, normalizeSpaces bool) string {
	code := utils.GetArgsRaw(msg.RawText)
	if code == "" {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil && reply.RawText != "" {
			code = reply.RawText
		}
	}
	if normalizeSpaces {
		code = strings.ReplaceAll(code, "\u00a0", " ")
	}
	return code
}

func (m *Eval) evalBlockText(errorOccurred bool, emojiID, lang, code, output string) string {
	transKey := m.T("eval", "")
	if errorOccurred {
		transKey = m.T("err", "")
	}
	if transKey == "" {
		if errorOccurred {
			transKey = "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>"
		} else {
			transKey = "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>"
		}
	}

	outputLabel := "output"
	if errorOccurred {
		outputLabel = "error"
	}

	return formatTrans(
		transKey,
		emojiID,
		lang,
		utils.EscapeHTML(code),
		outputLabel,
		utils.EscapeHTML(m.censor(output)),
	)
}

func (m *Eval) auditEval(msg *goroku.Message, capability, code string, started time.Time, err error, truncated bool) {
	actor, chat := int64(0), int64(0)
	if msg != nil {
		actor, chat = msg.SenderID, msg.ChatID
	}
	timedOut := errors.Is(err, errEvalTimeout) || (err != nil && strings.Contains(err.Error(), "timeout"))
	exit := 0
	if err != nil {
		exit = -1
	}
	auditExecution(executionAuditEvent{
		ActorID:    actor,
		ChatID:     chat,
		Capability: capability,
		Digest:     contentSHA256String(code),
		Duration:   time.Since(started),
		ExitCode:   exit,
		TimedOut:   timedOut,
		Truncated:  truncated,
		Status:     auditStatus(err, timedOut, false),
	})
}

func (m *Eval) EvalCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		return msg.Answer("❌ No code to evaluate")
	}

	start := time.Now()
	result, stdout, stderr, err := m.runYaegiEval(msg, code)
	m.auditEval(msg, "eval.go", code, start, err, false)
	execTime := time.Since(start).Seconds()
	if err != nil {
		errOut := strings.TrimSpace(err.Error())
		if stderr != "" {
			errOut += "\n" + stderr
		}

		errTrans := m.T("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		return msg.Answer(formatTrans(
			errTrans,
			"4994652309293105740",
			"go",
			utils.EscapeHTML(code),
			"error",
			m.censor(errOut),
		))
	}

	evalPyTrans := m.T("eval_py", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
	outStr := formatTrans(evalPyTrans, "4994652309293105740", "go", utils.EscapeHTML(code))

	if result != "" || stdout == "" {
		evalResultTrans := m.T("eval_result", "\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(evalResultTrans, "go", utils.EscapeHTML(m.censor(result)))
	}
	if stdout != "" {
		printOutpTrans := m.T("print_outp", "\n\n<tg-emoji emoji-id=5118861066981344121>✅</tg-emoji><b> Print Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(printOutpTrans, "go", utils.EscapeHTML(m.censor(stdout)))
	}
	timeExecTrans := m.T("time_exec", "\n<tg-emoji emoji-id=5134202243486057363>💫</tg-emoji> <b>Execution time: {}s</b>")
	outStr += formatTrans(timeExecTrans, fmt.Sprintf("%.2f", execTime))

	return msg.Answer(outStr)
}

type PythonEvalResult struct {
	Result    *string `json:"result"`
	Stdout    string  `json:"stdout"`
	Error     *string `json:"error"`
	Traceback string  `json:"traceback"`
}

// buildPythonEvalSpec assembles the child-process launch for .evalpy. It is
// separate from runPythonEval so the shape of the launch — what ends up in
// Args, what in the environment — can be asserted on without running python.
func (m *Eval) buildPythonEvalSpec(msg *goroku.Message, code string) (ProcessSpec, error) {
	reply, _ := msg.GetReplyMessage()
	ctxData := map[string]any{
		"message": messageToPythonMap(msg),
		"reply":   messageToPythonMap(reply),
		"client": map[string]any{
			"tg_id":    m.Client.TGID,
			"username": m.Client.Username,
		},
		"db": redactedDBDump(m.DB),
	}
	ctxJSON, err := json.Marshal(ctxData)
	if err != nil {
		return ProcessSpec{}, err
	}

	py := fmt.Sprintf(`
import contextlib
import io
import json
import os
import traceback
import datetime
import time
from types import SimpleNamespace

_ctx = json.loads(os.environ.pop(%q, "{}"))

def _ns(value):
    if isinstance(value, dict):
        return SimpleNamespace(**{k: _ns(v) for k, v in value.items()})
    if isinstance(value, list):
        return [_ns(v) for v in value]
    return value

class PeerUser:
    def __init__(self, user_id):
        self.user_id = user_id
    def __repr__(self):
        return f"PeerUser(\n  user_id={self.user_id}\n )"

class Message:
    def __init__(self, data):
        self._data = data or {}
        for k, v in self._data.items():
            setattr(self, k, v)
        self.peer_id = PeerUser(self._data.get("chat_id") or 0)
        self.date = datetime.datetime.now(datetime.timezone.utc)
        self.mentioned = False
        self.media_unread = False
        self.silent = False
        self.post = False
        self.from_scheduled = False
        self.legacy = False
        self.edit_hide = False
        self.pinned = False
        self.noforwards = False
        self.invert_media = False
        self.offline = False
        self.video_processing_pending = False
        self.paid_suggested_post_stars = False
        self.paid_suggested_post_ton = False
        self.from_id = PeerUser(self._data.get("sender_id") or 0)
        self.from_boosts_applied = None
        self.from_rank = None
        self.saved_peer_id = None
        self.fwd_from = None
        self.via_bot_id = self._data.get("via_bot_id")
        self.via_business_bot_id = None
        self.guestchat_via_from = None
        self.reply_to = None
        self.media = None
        self.reply_markup = None
        self.entities = []
        self.views = None
        self.forwards = None
        self.replies = None
        self.edit_date = None
        self.post_author = None
        self.grouped_id = None
        self.reactions = None
        self.restriction_reason = []
        self.ttl_period = None
        self.quick_reply_shortcut_id = None
        self.effect = None
        self.factcheck = None
        self.report_delivery_until_date = None
        self.paid_message_stars = None
        self.suggested_post = None
        self.schedule_repeat_period = None
        self.summary_from_language = None

    def __repr__(self):
        lines = [
            f" id={self.id}",
            f" peer_id={repr(self.peer_id)}",
            f" date={repr(self.date)}",
            f" message={repr(self.message)}",
            f" out={self.out}",
            f" mentioned={self.mentioned}",
            f" media_unread={self.media_unread}",
            f" silent={self.silent}",
            f" post={self.post}",
            f" from_scheduled={self.from_scheduled}",
            f" legacy={self.legacy}",
            f" edit_hide={self.edit_hide}",
            f" pinned={self.pinned}",
            f" noforwards={self.noforwards}",
            f" invert_media={self.invert_media}",
            f" offline={self.offline}",
            f" video_processing_pending={self.video_processing_pending}",
            f" paid_suggested_post_stars={self.paid_suggested_post_stars}",
            f" paid_suggested_post_ton={self.paid_suggested_post_ton}",
            f" from_id={repr(self.from_id)}",
            f" from_boosts_applied={self.from_boosts_applied}",
            f" from_rank={self.from_rank}",
            f" saved_peer_id={self.saved_peer_id}",
            f" fwd_from={self.fwd_from}",
            f" via_bot_id={self.via_bot_id}",
            f" via_business_bot_id={self.via_business_bot_id}",
            f" guestchat_via_from={self.guestchat_via_from}",
            f" reply_to={self.reply_to}",
            f" media={self.media}",
            f" reply_markup={self.reply_markup}",
            f" entities={repr(self.entities)}",
            f" views={self.views}",
            f" forwards={self.forwards}",
            f" replies={self.replies}",
            f" edit_date={self.edit_date}",
            f" post_author={self.post_author}",
            f" grouped_id={self.grouped_id}",
            f" reactions={self.reactions}",
            f" restriction_reason={repr(self.restriction_reason)}",
            f" ttl_period={self.ttl_period}",
            f" quick_reply_shortcut_id={self.quick_reply_shortcut_id}",
            f" effect={self.effect}",
            f" factcheck={self.factcheck}",
            f" report_delivery_until_date={self.report_delivery_until_date}",
            f" paid_message_stars={self.paid_message_stars}",
            f" suggested_post={self.suggested_post}",
            f" schedule_repeat_period={self.schedule_repeat_period}",
            f" summary_from_language={self.summary_from_language}"
        ]
        return "Message(\n" + ",\n".join(lines) + "\n)"

class DBProxy:
    def __init__(self, data):
        self._data = data or {}
    def get(self, owner, key=None, default=None):
        if key is None:
            return self._data.get(owner, default)
        return self._data.get(owner, {}).get(key, default)
    def __getitem__(self, key):
        return self._data[key]

message = m = event = Message(_ctx.get("message") or {})
reply = r = Message(_ctx.get("reply")) if _ctx.get("reply") else None
client = c = _ns(_ctx.get("client") or {})
db = DBProxy(_ctx.get("db") or {})

_code = %q
class _LimitedIO(io.StringIO):
    def __init__(self, limit):
        super().__init__()
        self.limit = limit
        self.truncated = False
    def write(self, value):
        remaining = self.limit - self.tell()
        if remaining > 0:
            super().write(value[:remaining])
        if len(value) > remaining:
            self.truncated = True
        return len(value)
    def getvalue(self):
        value = super().getvalue()
        if self.truncated:
            value += "\n[output truncated]"
        return value

_out = _LimitedIO(%d)
_res_data = {"result": None, "stdout": "", "error": None, "traceback": ""}

try:
    with contextlib.redirect_stdout(_out):
        try:
            _result = eval(_code, globals(), globals())
        except SyntaxError:
            exec(_code, globals(), globals())
            _result = None
    
    if _result is not None:
        if callable(getattr(_result, "stringify", None)):
            try:
                _result = str(_result.stringify())
            except Exception:
                _result = str(_result)
        else:
            _result = str(_result)
        _res_data["result"] = _result
        
    _res_data["stdout"] = _out.getvalue()
except Exception as e:
    _res_data["error"] = str(e)
    _res_data["traceback"] = traceback.format_exc()

print(json.dumps(_res_data))
`, evalContextEnvVar, code, externalOutputLimit)

	// The script goes in on stdin and the context in the environment: process
	// arguments are world-readable through /proc/<pid>/cmdline, and the context
	// carries the whole database dump. /proc/<pid>/environ is readable only by
	// the process owner.
	return ProcessSpec{
		Name:          "python3",
		Args:          []string{"-"},
		Stdin:         strings.NewReader(py),
		ExtraEnv:      []string{evalContextEnvVar + "=" + string(ctxJSON)},
		CaptureOutput: true,
	}, nil
}

func (m *Eval) runPythonEval(msg *goroku.Message, code string) (*PythonEvalResult, error) {
	spec, err := m.buildPythonEvalSpec(msg, code)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	proc := defaultProcessExecutor.Run(ctx, spec)
	if proc.Err != nil {
		return nil, fmt.Errorf("python execution error: %v, stderr: %s", proc.Err, string(proc.Stderr))
	}

	var res PythonEvalResult
	if proc.Truncated {
		return nil, fmt.Errorf("python output exceeded %d bytes", externalOutputLimit)
	}
	if err := json.Unmarshal(proc.Stdout, &res); err != nil {
		return nil, fmt.Errorf("failed to parse python output: %v, output: %s", err, string(proc.Stdout))
	}

	return &res, nil
}

func (m *Eval) EvalPyCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		return msg.Answer("❌ No Python code to evaluate")
	}

	start := time.Now()
	resData, err := m.runPythonEval(msg, code)
	auditErr := err
	if auditErr == nil && resData != nil && resData.Traceback != "" {
		auditErr = errors.New("python traceback")
	}
	m.auditEval(msg, "eval.python", code, start, auditErr, false)
	execTime := time.Since(start).Seconds()

	if err != nil || (resData != nil && resData.Traceback != "") {
		errOut := ""
		if resData != nil && resData.Traceback != "" {
			errOut = formatPythonTraceback(resData.Traceback)
		} else if err != nil {
			errOut = err.Error()
		}

		errTrans := m.T("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		return msg.Answer(formatTrans(
			errTrans,
			"4985626654563894116",
			"python",
			utils.EscapeHTML(code),
			"error",
			m.censor(errOut),
		))
	}

	evalPyTrans := m.T("eval_py", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
	outStr := formatTrans(evalPyTrans, "4985626654563894116", "python", utils.EscapeHTML(code))

	result := ""
	if resData.Result != nil {
		result = *resData.Result
	}
	stdout := resData.Stdout

	if result != "" || stdout == "" {
		evalResultTrans := m.T("eval_result", "\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(evalResultTrans, "python", utils.EscapeHTML(m.censor(result)))
	}
	if stdout != "" {
		printOutpTrans := m.T("print_outp", "\n\n<tg-emoji emoji-id=5118861066981344121>✅</tg-emoji><b> Print Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(printOutpTrans, "python", utils.EscapeHTML(m.censor(stdout)))
	}
	timeExecTrans := m.T("time_exec", "\n<tg-emoji emoji-id=5134202243486057363>💫</tg-emoji> <b>Execution time: {}s</b>")
	outStr += formatTrans(timeExecTrans, fmt.Sprintf("%.2f", execTime))

	return msg.Answer(outStr)
}

func messageToPythonMap(msg *goroku.Message) map[string]any {
	if msg == nil {
		return nil
	}
	return map[string]any{
		"id":              msg.ID,
		"ID":              msg.ID,
		"chat_id":         msg.ChatID,
		"ChatID":          msg.ChatID,
		"sender_id":       msg.SenderID,
		"SenderID":        msg.SenderID,
		"text":            msg.Text,
		"Text":            msg.Text,
		"message":         msg.RawText,
		"raw_text":        msg.RawText,
		"RawText":         msg.RawText,
		"out":             msg.Out,
		"Out":             msg.Out,
		"is_private":      msg.IsPrivate,
		"IsPrivate":       msg.IsPrivate,
		"is_channel":      msg.IsChannel,
		"IsChannel":       msg.IsChannel,
		"is_group":        msg.IsGroup,
		"IsGroup":         msg.IsGroup,
		"reply_to_msg_id": msg.ReplyToMsgID,
		"ReplyToMsgID":    msg.ReplyToMsgID,
	}
}

func isFullPackageGo(code string) bool {
	trimmed := strings.TrimSpace(code)
	for {
		if strings.HasPrefix(trimmed, "//") {
			idx := strings.Index(trimmed, "\n")
			if idx == -1 {
				return false
			}
			trimmed = strings.TrimSpace(trimmed[idx:])
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			idx := strings.Index(trimmed, "*/")
			if idx == -1 {
				return false
			}
			trimmed = strings.TrimSpace(trimmed[idx+2:])
			continue
		}
		break
	}
	return strings.HasPrefix(trimmed, "package ")
}

func (m *Eval) runYaegiEval(msg *goroku.Message, code string) (string, string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), yaegiEvalTimeout)
	defer cancel()
	if err := acquireSlot(ctx, yaegiSlots); err != nil {
		return "", "", "", errEvalTimeout
	}
	defer func() { <-yaegiSlots }()

	return runLiveYaegiEval(ctx, msg, m.Client, m.DB, code)
}

func formatEvalResult(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	if err, ok := v.(error); ok {
		return err.Error()
	}
	// Try indented JSON for structs/maps/slices.
	if data, err := json.MarshalIndent(v, "", "  "); err == nil {
		return string(data)
	}
	// Fallback to verbose Go representation.
	return fmt.Sprintf("%+v", v)
}

func isMultiValuePanic(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return strings.Contains(s, "not assignable to type") ||
		strings.Contains(s, "multiple-value") ||
		strings.Contains(s, "too many arguments to return")
}

func (m *Eval) ECCmd(msg *goroku.Message) error {
	return m.runCCompiler(msg, true)
}

func (m *Eval) ECPPCmd(msg *goroku.Message) error {
	return m.runCCompiler(msg, false)
}

func (m *Eval) runCCompiler(msg *goroku.Message, isC bool) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		msg.Text = "❌ No code to compile/execute"
		return nil
	}

	compiler := "g++"
	lang := "cpp"
	compilerName := "C++ (g++)"
	emojiID := "4985844035743646190" // c++ emoji
	if isC {
		compiler = "gcc"
		lang = "c"
		compilerName = "C (gcc)"
		emojiID = "4986046904228905931" // c emoji
	}

	_, checkErr := exec.LookPath(compiler)
	if checkErr != nil {
		noCompilerTrans := m.T("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} compiler is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, emojiID, compilerName)
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	compilingTrans := m.T("compiling", "<tg-emoji emoji-id=5325787248363314644>🫥</tg-emoji> <b>Compiling code...</b>")
	_ = msg.Answer(compilingTrans)

	tmpDir, err := os.MkdirTemp("", "eval_compile_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	srcFile := filepath.Join(tmpDir, "code."+lang)
	err = os.WriteFile(srcFile, []byte(code), 0600)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	binFile := filepath.Join(tmpDir, "code")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmdCompile, err := secureCommandContext(ctx, compiler, "-o", binFile, srcFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start compiler: %v", err)
		return nil
	}
	compileOut := newBoundedBuffer(externalOutputLimit)
	cmdCompile.Stdout = compileOut
	cmdCompile.Stderr = compileOut

	func() {
		defer releaseCommandSlot()
		err = cmdCompile.Run()
	}()
	if err != nil {
		errMsg := compileOut.String()
		if errMsg == "" {
			errMsg = err.Error()
		}

		errTrans := m.T("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		msg.Text = formatTrans(errTrans, emojiID, lang, utils.EscapeHTML(code), "error", m.censor(errMsg))
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	ctxRun, cancelRun := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRun()

	cmdRun, err := secureCommandContext(ctxRun, binFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start executable: %v", err)
		return nil
	}
	runOut := newBoundedBuffer(externalOutputLimit)
	cmdRun.Stdout = runOut
	cmdRun.Stderr = runOut

	func() {
		defer releaseCommandSlot()
		err = cmdRun.Run()
	}()
	output := runOut.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	msg.Text = m.evalBlockText(errorOccurred, emojiID, lang, code, output)

	if msg.Client != nil {
		msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
	}
	return nil
}

func (m *Eval) ENodeCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		msg.Text = "❌ No code to execute"
		return nil
	}

	_, checkErr := exec.LookPath("node")
	if checkErr != nil {
		noCompilerTrans := m.T("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} compiler is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, "4985643941807260310", "Node.js")
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "eval_js_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	srcFile := filepath.Join(tmpDir, "code.js")
	err = os.WriteFile(srcFile, []byte(code), 0600)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd, err := secureCommandContext(ctx, "node", srcFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start executable: %v", err)
		return nil
	}
	out := newBoundedBuffer(externalOutputLimit)
	cmd.Stdout = out
	cmd.Stderr = out

	func() {
		defer releaseCommandSlot()
		err = cmd.Run()
	}()
	output := out.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	msg.Text = m.evalBlockText(errorOccurred, "4985643941807260310", "javascript", code, output)

	if msg.Client != nil {
		msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
	}
	return nil
}

func runBrainfuck(code string) (string, error) {
	var instructions []rune
	for _, r := range code {
		if strings.ContainsRune("><+-.,[]", r) {
			instructions = append(instructions, r)
		}
	}

	jumps := make(map[int]int)
	var stack []int
	for i, r := range instructions {
		if r == '[' {
			stack = append(stack, i)
		} else if r == ']' {
			if len(stack) == 0 {
				return "", fmt.Errorf("unmatched ']' at instruction %d", i)
			}
			openIdx := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			jumps[openIdx] = i
			jumps[i] = openIdx
		}
	}
	if len(stack) > 0 {
		return "", fmt.Errorf("unmatched '[' at instruction %d", stack[len(stack)-1])
	}

	tape := make([]byte, 30000)
	ptr := 0
	pc := 0
	var out bytes.Buffer
	steps := 0
	maxSteps := 1000000

	for pc < len(instructions) {
		steps++
		if steps > maxSteps {
			return out.String(), fmt.Errorf("execution limit exceeded (potential infinite loop)")
		}

		switch instructions[pc] {
		case '>':
			ptr++
			if ptr >= len(tape) {
				ptr = 0
			}
		case '<':
			ptr--
			if ptr < 0 {
				ptr = len(tape) - 1
			}
		case '+':
			tape[ptr]++
		case '-':
			tape[ptr]--
		case '.':
			if out.Len() >= externalOutputLimit {
				return out.String(), fmt.Errorf("output limit exceeded")
			}
			out.WriteByte(tape[ptr])
		case ',':
			tape[ptr] = 0
		case '[':
			if tape[ptr] == 0 {
				pc = jumps[pc]
			}
		case ']':
			if tape[ptr] != 0 {
				pc = jumps[pc]
			}
		}
		pc++
	}
	return out.String(), nil
}

func (m *Eval) EBFCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, false)
	if code == "" {
		msg.Text = "❌ No code to execute"
		return nil
	}

	output, err := runBrainfuck(code)
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		} else {
			output = output + "\n\nError: " + err.Error()
		}
	}

	msg.Text = m.evalBlockText(errorOccurred, "4985930888572306287", "brainfuck", code, output)

	if msg.Client != nil {
		msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
	}
	return nil
}

func (m *Eval) EPHPCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		msg.Text = "❌ No code to execute"
		return nil
	}

	_, checkErr := exec.LookPath("php")
	if checkErr != nil {
		noCompilerTrans := m.T("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} interpreter is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, "4983593786413155017", "PHP")
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "eval_php_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	srcFile := filepath.Join(tmpDir, "code.php")
	err = os.WriteFile(srcFile, []byte(code), 0600)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd, err := secureCommandContext(ctx, "php", srcFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start executable: %v", err)
		return nil
	}
	out := newBoundedBuffer(externalOutputLimit)
	cmd.Stdout = out
	cmd.Stderr = out

	func() {
		defer releaseCommandSlot()
		err = cmd.Run()
	}()
	output := out.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	msg.Text = m.evalBlockText(errorOccurred, "4983593786413155017", "php", code, output)

	if msg.Client != nil {
		msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
	}
	return nil
}

func (m *Eval) ERubyCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		msg.Text = "❌ No code to execute"
		return nil
	}

	_, checkErr := exec.LookPath("ruby")
	if checkErr != nil {
		noCompilerTrans := m.T("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} interpreter is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, "4985760855112024628", "Ruby")
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "eval_rb_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	srcFile := filepath.Join(tmpDir, "code.rb")
	err = os.WriteFile(srcFile, []byte(code), 0600)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd, err := secureCommandContext(ctx, "ruby", srcFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start executable: %v", err)
		return nil
	}
	out := newBoundedBuffer(externalOutputLimit)
	cmd.Stdout = out
	cmd.Stderr = out

	func() {
		defer releaseCommandSlot()
		err = cmd.Run()
	}()
	output := out.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	msg.Text = m.evalBlockText(errorOccurred, "4985760855112024628", "ruby", code, output)

	if msg.Client != nil {
		msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
	}
	return nil
}

func (m *Eval) ERustCmd(msg *goroku.Message) error {
	code := evalCodeFromMessage(msg, true)
	if code == "" {
		msg.Text = "❌ No code to compile/execute"
		return nil
	}

	_, checkErr := exec.LookPath("rustc")
	if checkErr != nil {
		noCompilerTrans := m.T("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} compiler is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, "4994944646242108269", "Rust")
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	compilingTrans := m.T("compiling", "<tg-emoji emoji-id=5325787248363314644>🫥</tg-emoji> <b>Compiling code...</b>")
	_ = msg.Answer(compilingTrans)

	tmpDir, err := os.MkdirTemp("", "eval_rs_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	srcFile := filepath.Join(tmpDir, "code.rs")
	err = os.WriteFile(srcFile, []byte(code), 0600)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	binFile := filepath.Join(tmpDir, "code")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmdCompile, err := secureCommandContext(ctx, "rustc", "-o", binFile, srcFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start compiler: %v", err)
		return nil
	}
	compileOut := newBoundedBuffer(externalOutputLimit)
	cmdCompile.Stdout = compileOut
	cmdCompile.Stderr = compileOut

	func() {
		defer releaseCommandSlot()
		err = cmdCompile.Run()
	}()
	if err != nil {
		errMsg := compileOut.String()
		if errMsg == "" {
			errMsg = err.Error()
		}

		errTrans := m.T("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		msg.Text = formatTrans(errTrans, "4994944646242108269", "rust", utils.EscapeHTML(code), "error", m.censor(errMsg))
		if msg.Client != nil {
			msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
		}
		return nil
	}

	ctxRun, cancelRun := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRun()

	cmdRun, err := secureCommandContext(ctxRun, binFile)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to start executable: %v", err)
		return nil
	}
	runOut := newBoundedBuffer(externalOutputLimit)
	cmdRun.Stdout = runOut
	cmdRun.Stderr = runOut

	func() {
		defer releaseCommandSlot()
		err = cmdRun.Run()
	}()
	output := runOut.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	msg.Text = m.evalBlockText(errorOccurred, "4994944646242108269", "rust", code, output)

	if msg.Client != nil {
		msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text) //nolint:errcheck,gosec
	}
	return nil
}
