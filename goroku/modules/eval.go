package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

type Eval struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *Eval) Name() string {
	return "Eval"
}

func (m *Eval) Strings() map[string]string {
	return map[string]string{
		"name": "Evaluator",
	}
}

func (m *Eval) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *Eval) ClientReady() error { return nil }
func (m *Eval) OnUnload() error    { return nil }
func (m *Eval) OnDlmod() error     { return nil }

func (m *Eval) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"eval":   m.EvalCmd,
		"e":      m.EvalCmd,
		"epy":    m.EvalPyCmd,
		"evalpy": m.EvalPyCmd,
		"py":     m.EvalPyCmd,
		"ec":     m.ECCmd,
		"ecpp":   m.ECPPCmd,
		"enode":  m.ENodeCmd,
	}
}

func (m *Eval) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *Eval) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *Eval) censor(text string) string {
	var extras []string
	if m.client != nil {
		extras = append(extras, m.client.APIHash)
		if m.client.GorokuMe != nil {
			if u, ok := m.client.GorokuMe.(*tg.User); ok && u.Phone != "" {
				extras = append(extras, u.Phone)
			}
		}
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

func (m *Eval) EvalCmd(msg *goroku.Message) error {
	code := utils.GetArgsRaw(msg.Text)
	if code == "" {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil && reply.RawText != "" {
			code = reply.RawText
		}
	}
	if code == "" {
		return msg.Answer("❌ No code to evaluate")
	}
	code = strings.ReplaceAll(code, "\u00a0", " ")

	start := time.Now()
	result, stdout, stderr, err := m.runYaegiEval(msg, code)
	execTime := time.Since(start).Seconds()
	if err != nil {
		errOut := strings.TrimSpace(err.Error())
		if stderr != "" {
			errOut += "\n" + stderr
		}

		errTrans := m.getTrans("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		return msg.Answer(formatTrans(
			errTrans,
			"4985626654563894116",
			"go",
			utils.EscapeHTML(code),
			"error",
			m.censor(errOut),
		))
	}

	evalPyTrans := m.getTrans("eval_py", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
	outStr := formatTrans(evalPyTrans, "4985626654563894116", "go", utils.EscapeHTML(code))

	if result != "" || stdout == "" {
		evalResultTrans := m.getTrans("eval_result", "\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(evalResultTrans, "go", utils.EscapeHTML(m.censor(result)))
	}
	if stdout != "" {
		printOutpTrans := m.getTrans("print_outp", "\n\n<tg-emoji emoji-id=5118861066981344121>✅</tg-emoji><b> Print Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(printOutpTrans, "go", utils.EscapeHTML(m.censor(stdout)))
	}
	timeExecTrans := m.getTrans("time_exec", "\n<tg-emoji emoji-id=5134202243486057363>💫</tg-emoji> <b>Execution time: {}s</b>")
	outStr += formatTrans(timeExecTrans, fmt.Sprintf("%.2f", execTime))

	return msg.Answer(outStr)
}

type PythonEvalResult struct {
	Result    *string `json:"result"`
	Stdout    string  `json:"stdout"`
	Error     *string `json:"error"`
	Traceback string  `json:"traceback"`
}

func (m *Eval) runPythonEval(msg *goroku.Message, code string) (*PythonEvalResult, error) {
	reply, _ := msg.GetReplyMessage()
	ctxData := map[string]interface{}{
		"message": messageToPythonMap(msg),
		"reply":   messageToPythonMap(reply),
		"client": map[string]interface{}{
			"tg_id":    m.client.TGID,
			"username": m.client.Username,
		},
		"db": m.db.Dump(),
	}
	ctxJSON, err := json.Marshal(ctxData)
	if err != nil {
		return nil, err
	}

	py := fmt.Sprintf(`
import contextlib
import io
import json
import traceback
from types import SimpleNamespace

_ctx = json.loads(%q)

def _ns(value):
    if isinstance(value, dict):
        return SimpleNamespace(**{k: _ns(v) for k, v in value.items()})
    if isinstance(value, list):
        return [_ns(v) for v in value]
    return value

class DBProxy:
    def __init__(self, data):
        self._data = data or {}
    def get(self, owner, key=None, default=None):
        if key is None:
            return self._data.get(owner, default)
        return self._data.get(owner, {}).get(key, default)
    def __getitem__(self, key):
        return self._data[key]

message = m = event = _ns(_ctx.get("message") or {})
reply = r = _ns(_ctx.get("reply") or {})
client = c = _ns(_ctx.get("client") or {})
db = DBProxy(_ctx.get("db") or {})

_code = %q
_out = io.StringIO()
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
`, string(ctxJSON), code)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", py)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("python execution error: %v, stderr: %s", err, stderr.String())
	}

	var res PythonEvalResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return nil, fmt.Errorf("failed to parse python output: %v, output: %s", err, stdout.String())
	}

	return &res, nil
}

func (m *Eval) EvalPyCmd(msg *goroku.Message) error {
	code := utils.GetArgsRaw(msg.Text)
	if code == "" {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil && reply.RawText != "" {
			code = reply.RawText
		}
	}
	if code == "" {
		return msg.Answer("❌ No Python code to evaluate")
	}
	code = strings.ReplaceAll(code, "\u00a0", " ")

	start := time.Now()
	resData, err := m.runPythonEval(msg, code)
	execTime := time.Since(start).Seconds()

	if err != nil || (resData != nil && resData.Traceback != "") {
		errOut := ""
		if resData != nil && resData.Traceback != "" {
			errOut = formatPythonTraceback(resData.Traceback)
		} else if err != nil {
			errOut = err.Error()
		}

		errTrans := m.getTrans("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		return msg.Answer(formatTrans(
			errTrans,
			"4985626654563894116",
			"python",
			utils.EscapeHTML(code),
			"error",
			m.censor(errOut),
		))
	}

	evalPyTrans := m.getTrans("eval_py", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
	outStr := formatTrans(evalPyTrans, "4985626654563894116", "python", utils.EscapeHTML(code))

	result := ""
	if resData.Result != nil {
		result = *resData.Result
	}
	stdout := resData.Stdout

	if result != "" || stdout == "" {
		evalResultTrans := m.getTrans("eval_result", "\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(evalResultTrans, "python", utils.EscapeHTML(m.censor(result)))
	}
	if stdout != "" {
		printOutpTrans := m.getTrans("print_outp", "\n\n<tg-emoji emoji-id=5118861066981344121>✅</tg-emoji><b> Print Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		outStr += formatTrans(printOutpTrans, "python", utils.EscapeHTML(m.censor(stdout)))
	}
	timeExecTrans := m.getTrans("time_exec", "\n<tg-emoji emoji-id=5134202243486057363>💫</tg-emoji> <b>Execution time: {}s</b>")
	outStr += formatTrans(timeExecTrans, fmt.Sprintf("%.2f", execTime))

	return msg.Answer(outStr)
}

func messageToPythonMap(msg *goroku.Message) map[string]interface{} {
	if msg == nil {
		return nil
	}
	return map[string]interface{}{
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

func (m *Eval) runYaegiEval(msg *goroku.Message, code string) (string, string, string, error) {
	var stdout, stderr bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, Stderr: &stderr})
	if err := i.Use(stdlib.Symbols); err != nil {
		return "", "", "", err
	}
	loader, _ := m.client.Loader.(*goroku.Modules)
	if err := i.Use(interp.Exports{
		"gorokuctx/gorokuctx": map[string]reflect.Value{
			"Msg":    reflect.ValueOf(msg),
			"Client": reflect.ValueOf(m.client),
			"DB":     reflect.ValueOf(m.db),
			"Loader": reflect.ValueOf(loader),
		},
	}); err != nil {
		return "", "", "", err
	}

	source := m.buildYaegiSource(code, true)
	value, err := m.evalYaegiWithTimeout(i, source)
	if err != nil {
		source = m.buildYaegiSource(code, false)
		value, err = m.evalYaegiWithTimeout(i, source)
	}
	if err != nil {
		return "", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
	}

	runner, ok := value.Interface().(func() interface{})
	if !ok {
		return "", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), fmt.Errorf("invalid yaegi runner signature")
	}
	done := make(chan struct{})
	var result interface{}
	var panicValue interface{}
	go func() {
		defer func() {
			panicValue = recover()
			close(done)
		}()
		result = runner()
	}()
	select {
	case <-done:
		if panicValue != nil {
			return "", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), fmt.Errorf("panic: %v", panicValue)
		}
	case <-time.After(15 * time.Second):
		return "", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), fmt.Errorf("eval timeout")
	}

	resultText := ""
	if result != nil {
		resultText = fmt.Sprintf("%v", result)
	}
	return resultText, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), nil
}

func (m *Eval) buildYaegiSource(code string, expression bool) string {
	body := code
	if expression {
		body = "return " + code
	}
	return fmt.Sprintf(`package main

import (
    "gorokuctx"
)

func __run__() interface{} {
    msg := gorokuctx.Msg
    client := gorokuctx.Client
    db := gorokuctx.DB
    loader := gorokuctx.Loader
    _ = msg
    _ = client
    _ = db
    _ = loader
    %s
    return nil
}
`, body)
}

func (m *Eval) evalYaegiWithTimeout(i *interp.Interpreter, source string) (reflect.Value, error) {
	done := make(chan struct{})
	var value reflect.Value
	var err error
	go func() {
		value, err = i.Eval(source)
		if err == nil {
			value, err = i.Eval("__run__")
		}
		close(done)
	}()
	select {
	case <-done:
		return value, err
	case <-time.After(15 * time.Second):
		return reflect.Value{}, fmt.Errorf("eval compile timeout")
	}
}

func (m *Eval) ECCmd(msg *goroku.Message) error {
	return m.runCCompiler(msg, true)
}

func (m *Eval) ECPPCmd(msg *goroku.Message) error {
	return m.runCCompiler(msg, false)
}

func (m *Eval) runCCompiler(msg *goroku.Message, isC bool) error {
	code := utils.GetArgsRaw(msg.Text)
	if code == "" {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil && reply.RawText != "" {
			code = reply.RawText
		}
	}
	if code == "" {
		msg.Text = "❌ No code to compile/execute"
		return nil
	}
	code = strings.ReplaceAll(code, "\u00a0", " ")

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
		noCompilerTrans := m.getTrans("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} compiler is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, emojiID, compilerName)
		if msg.Client != nil {
			msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text) //nolint:errcheck
		}
		return nil
	}

	compilingTrans := m.getTrans("compiling", "<tg-emoji emoji-id=5325787248363314644>🫥</tg-emoji> <b>Compiling code...</b>")
	_ = msg.Answer(compilingTrans)

	tmpDir, err := os.MkdirTemp("", "eval_compile_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "code."+lang)
	err = os.WriteFile(srcFile, []byte(code), 0644)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	binFile := filepath.Join(tmpDir, "code")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmdCompile := exec.CommandContext(ctx, compiler, "-o", binFile, srcFile)
	var compileOut bytes.Buffer
	cmdCompile.Stdout = &compileOut
	cmdCompile.Stderr = &compileOut

	err = cmdCompile.Run()
	if err != nil {
		errMsg := compileOut.String()
		if errMsg == "" {
			errMsg = err.Error()
		}

		errTrans := m.getTrans("err", "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>")
		msg.Text = formatTrans(errTrans, emojiID, lang, utils.EscapeHTML(code), "error", m.censor(errMsg))
		if msg.Client != nil {
			msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text) //nolint:errcheck
		}
		return nil
	}

	ctxRun, cancelRun := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRun()

	cmdRun := exec.CommandContext(ctxRun, binFile)
	var runOut bytes.Buffer
	cmdRun.Stdout = &runOut
	cmdRun.Stderr = &runOut

	err = cmdRun.Run()
	output := runOut.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	evalOrErrTrans := "eval"
	if errorOccurred {
		evalOrErrTrans = "err"
	}

	transKey := m.getTrans(evalOrErrTrans, "")
	if transKey == "" {
		if errorOccurred {
			transKey = "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>"
		} else {
			transKey = "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>"
		}
	}

	errorOrOutputLabel := "output"
	if errorOccurred {
		errorOrOutputLabel = "error"
	}

	msg.Text = formatTrans(
		transKey,
		emojiID,
		lang,
		utils.EscapeHTML(code),
		errorOrOutputLabel,
		utils.EscapeHTML(m.censor(output)),
	)

	if msg.Client != nil {
		msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text) //nolint:errcheck
	}
	return nil
}

func (m *Eval) ENodeCmd(msg *goroku.Message) error {
	code := utils.GetArgsRaw(msg.Text)
	if code == "" {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil && reply.RawText != "" {
			code = reply.RawText
		}
	}
	if code == "" {
		msg.Text = "❌ No code to execute"
		return nil
	}
	code = strings.ReplaceAll(code, "\u00a0", " ")

	_, checkErr := exec.LookPath("node")
	if checkErr != nil {
		noCompilerTrans := m.getTrans("no_compiler", "<tg-emoji emoji-id={}>💻</tg-emoji> <b>{} compiler is not installed on the system.</b>")
		msg.Text = formatTrans(noCompilerTrans, "4985643941807260310", "Node.js")
		if msg.Client != nil {
			msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text) //nolint:errcheck
		}
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "eval_js_*")
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error creating temp dir: %s", err.Error())
		return nil
	}
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "code.js")
	err = os.WriteFile(srcFile, []byte(code), 0644)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Error writing code: %s", err.Error())
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", srcFile)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	output := out.String()
	errorOccurred := false
	if err != nil {
		errorOccurred = true
		if output == "" {
			output = err.Error()
		}
	}

	evalOrErrTrans := "eval"
	if errorOccurred {
		evalOrErrTrans = "err"
	}

	transKey := m.getTrans(evalOrErrTrans, "")
	if transKey == "" {
		if errorOccurred {
			transKey = "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Error:</b>\n<pre><code class=\"language-{}\">{}</code></pre>"
		} else {
			transKey = "<tg-emoji emoji-id={}>💻</tg-emoji><b> Code:</b>\n<pre><code class=\"language-{}\">{}</code></pre>\n\n<tg-emoji emoji-id=5197688912457245639>✅</tg-emoji><b> Result:</b>\n<pre><code class=\"language-{}\">{}</code></pre>"
		}
	}

	errorOrOutputLabel := "output"
	if errorOccurred {
		errorOrOutputLabel = "error"
	}

	msg.Text = formatTrans(
		transKey,
		"4985643941807260310",
		"javascript",
		utils.EscapeHTML(code),
		errorOrOutputLabel,
		utils.EscapeHTML(m.censor(output)),
	)

	if msg.Client != nil {
		msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text) //nolint:errcheck
	}
	return nil
}
