package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"goroku/goroku"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// Worker isolation (M4.2): Go .eval runs in a child process so timeout/cancel can
// SIGKILL the process group. The worker has no shared memory with the bot:
// msg/client/db are JSON snapshots (read-only copies); Loader is unavailable;
// mutations do not affect the parent process.
const (
	yaegiWorkerArg = "--yaegi-worker"
	yaegiWorkerEnv = "GOROKU_YAEGI_WORKER"
)

// Overridable for tests; production default matches prior in-process budget.
var yaegiEvalTimeout = 15 * time.Second

// yaegiWorkerBinOverride is set via GOROKU_YAEGI_WORKER_BIN (tests / custom paths).
func yaegiWorkerBinOverride() string {
	return strings.TrimSpace(os.Getenv("GOROKU_YAEGI_WORKER_BIN"))
}

type yaegiMsgSnap struct {
	ID           int64  `json:"id"`
	ChatID       int64  `json:"chat_id"`
	SenderID     int64  `json:"sender_id"`
	Text         string `json:"text"`
	RawText      string `json:"raw_text"`
	Out          bool   `json:"out"`
	IsPrivate    bool   `json:"is_private"`
	IsChannel    bool   `json:"is_channel"`
	IsGroup      bool   `json:"is_group"`
	ReplyToMsgID int64  `json:"reply_to_msg_id"`
	ViaBotID     int64  `json:"via_bot_id"`
}

type yaegiClientSnap struct {
	TGID     int64  `json:"tg_id"`
	Username string `json:"username"`
}

type yaegiWorkerRequest struct {
	Code   string           `json:"code"`
	Msg    *yaegiMsgSnap    `json:"msg,omitempty"`
	Client *yaegiClientSnap `json:"client,omitempty"`
	DB     map[string]any   `json:"db,omitempty"`
}

type yaegiWorkerResponse struct {
	Result string `json:"result"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Error  string `json:"error,omitempty"`
}

// IsYaegiWorkerProcess reports whether this process should run the Yaegi worker
// entrypoint instead of the full application (or test suite).
func IsYaegiWorkerProcess() bool {
	if os.Getenv(yaegiWorkerEnv) == "1" {
		return true
	}
	for _, a := range os.Args[1:] {
		if a == yaegiWorkerArg {
			return true
		}
	}
	return false
}

// RunYaegiWorker is the child-process entrypoint. Reads one JSON request from
// stdin, evaluates with Yaegi, writes one JSON response to stdout.
func RunYaegiWorker() int {
	var req yaegiWorkerRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(yaegiWorkerResponse{Error: "invalid request: " + err.Error()})
		return 2
	}
	res := executeYaegiWorker(req)
	if err := json.NewEncoder(os.Stdout).Encode(res); err != nil {
		return 3
	}
	return 0
}

func resolveYaegiWorker() (name string, args []string, err error) {
	if bin := yaegiWorkerBinOverride(); bin != "" {
		return bin, nil, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("resolve yaegi worker executable: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return exe, []string{yaegiWorkerArg}, nil
}

func executeYaegiWorker(req yaegiWorkerRequest) yaegiWorkerResponse {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return yaegiWorkerResponse{Error: "no code to evaluate"}
	}

	stdout, stderr := newBoundedBuffer(externalOutputLimit), newBoundedBuffer(externalOutputLimit)
	i := interp.New(interp.Options{Stdout: stdout, Stderr: stderr})
	if err := i.Use(stdlib.Symbols); err != nil {
		return yaegiWorkerResponse{Error: err.Error()}
	}

	msg := req.Msg
	if msg == nil {
		msg = &yaegiMsgSnap{}
	}
	client := req.Client
	if client == nil {
		client = &yaegiClientSnap{}
	}
	db := req.DB
	if db == nil {
		db = map[string]any{}
	}

	if err := i.Use(interp.Exports{
		"gorokuctx/gorokuctx": map[string]reflect.Value{
			"Msg":    reflect.ValueOf(msg),
			"Client": reflect.ValueOf(client),
			"DB":     reflect.ValueOf(db),
			// Loader is intentionally unavailable out-of-process (no shared bot state).
			"Loader": reflect.ValueOf((*struct{})(nil)),
		},
	}); err != nil {
		return yaegiWorkerResponse{Error: err.Error()}
	}

	out := func(result string, err error) yaegiWorkerResponse {
		res := yaegiWorkerResponse{
			Result: result,
			Stdout: strings.TrimSpace(stdout.String()),
			Stderr: strings.TrimSpace(stderr.String()),
		}
		if err != nil {
			res.Error = err.Error()
		}
		return res
	}

	if isFullPackageGo(code) {
		_, err := evalYaegiSource(i, code, false)
		return out("", err)
	}

	source := buildYaegiSource(code, true)
	value, err := evalYaegiSource(i, source, true)
	if err != nil {
		source = buildYaegiSource(code, false)
		value, err = evalYaegiSource(i, source, true)
	}
	if err != nil {
		return out("", err)
	}

	resultText, runErr, multiValuePanic := invokeYaegiRunner(value)
	if multiValuePanic {
		source = buildYaegiSource(code, false)
		value, err = evalYaegiSource(i, source, true)
		if err != nil {
			return out("", err)
		}
		resultText, runErr, _ = invokeYaegiRunner(value)
	}
	if runErr != nil {
		return out("", runErr)
	}
	return out(resultText, nil)
}

func buildYaegiSource(code string, expression bool) string {
	body := code
	if expression {
		body = "return " + code
	}
	return fmt.Sprintf(`package main

import (
    "gorokuctx"
)

func __run__() any {
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

func evalYaegiSource(i *interp.Interpreter, source string, needRunFunc bool) (reflect.Value, error) {
	value, err := i.Eval(source)
	if err != nil {
		return reflect.Value{}, err
	}
	if needRunFunc {
		value, err = i.Eval("__run__")
	}
	return value, err
}

func invokeYaegiRunner(value reflect.Value) (string, error, bool) {
	if !value.IsValid() || value.Kind() != reflect.Func {
		return "", fmt.Errorf("invalid yaegi runner signature"), false
	}
	runner, ok := value.Interface().(func() any)
	if !ok {
		var result reflect.Value
		var panicValue any
		func() {
			defer func() { panicValue = recover() }()
			outs := value.Call(nil)
			if len(outs) > 0 {
				result = outs[0]
			}
		}()
		if panicValue != nil {
			return "", fmt.Errorf("panic: %v", panicValue), isMultiValuePanic(panicValue)
		}
		resultText := ""
		if result.IsValid() && result.Kind() == reflect.Interface && !result.IsNil() {
			resultText = formatEvalResult(result.Interface())
		} else if result.IsValid() && result.CanInterface() {
			v := result.Interface()
			if v != nil {
				resultText = formatEvalResult(v)
			}
		}
		return resultText, nil, false
	}
	var result any
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		result = runner()
	}()
	if panicValue != nil {
		return "", fmt.Errorf("panic: %v", panicValue), isMultiValuePanic(panicValue)
	}
	resultText := ""
	if result != nil {
		resultText = formatEvalResult(result)
	}
	return resultText, nil, false
}

func (m *Eval) buildYaegiRequest(msg *goroku.Message, code string) yaegiWorkerRequest {
	req := yaegiWorkerRequest{Code: code}
	if msg != nil {
		req.Msg = &yaegiMsgSnap{
			ID:           msg.ID,
			ChatID:       msg.ChatID,
			SenderID:     msg.SenderID,
			Text:         msg.Text,
			RawText:      msg.RawText,
			Out:          msg.Out,
			IsPrivate:    msg.IsPrivate,
			IsChannel:    msg.IsChannel,
			IsGroup:      msg.IsGroup,
			ReplyToMsgID: msg.ReplyToMsgID,
			ViaBotID:     msg.ViaBotID,
		}
	}
	if m.client != nil {
		req.Client = &yaegiClientSnap{
			TGID:     m.client.TGID,
			Username: m.client.Username,
		}
	}
	if m.db != nil {
		// Snapshot only — worker cannot mutate parent DB.
		dump := m.db.Dump()
		if dump != nil {
			req.DB = make(map[string]any, len(dump))
			for k, v := range dump {
				req.DB[k] = v
			}
		}
	}
	return req
}

func runYaegiWorkerProcess(ctx context.Context, req yaegiWorkerRequest) (yaegiWorkerResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return yaegiWorkerResponse{}, err
	}
	name, args, err := resolveYaegiWorker()
	if err != nil {
		return yaegiWorkerResponse{}, err
	}
	proc := defaultProcessExecutor.Run(ctx, ProcessSpec{
		Name:          name,
		Args:          args,
		Stdin:         bytes.NewReader(payload),
		CaptureOutput: true,
		ExtraEnv:      []string{yaegiWorkerEnv + "=1"},
	})
	if proc.TimedOut || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return yaegiWorkerResponse{}, errEvalTimeout
	}
	if proc.Canceled || errors.Is(ctx.Err(), context.Canceled) {
		return yaegiWorkerResponse{}, ctx.Err()
	}
	if len(proc.Stdout) == 0 {
		if proc.Err != nil {
			return yaegiWorkerResponse{}, fmt.Errorf("yaegi worker failed: %v; stderr=%s", proc.Err, strings.TrimSpace(string(proc.Stderr)))
		}
		return yaegiWorkerResponse{}, errors.New("yaegi worker produced empty output")
	}
	var res yaegiWorkerResponse
	if err := json.Unmarshal(proc.Stdout, &res); err != nil {
		return yaegiWorkerResponse{}, fmt.Errorf("yaegi worker response: %w; stdout=%s stderr=%s", err, strings.TrimSpace(string(proc.Stdout)), strings.TrimSpace(string(proc.Stderr)))
	}
	return res, nil
}
