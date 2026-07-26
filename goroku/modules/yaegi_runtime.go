package modules

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"goroku/goroku"
	"goroku/goroku/evalstats"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// Execution model for Go `.eval` — read this before changing anything here.
//
// The interpreter runs INSIDE the bot process, with the live *Message,
// *CustomTelegramClient, *Database and *Modules in its context. That is
// deliberate: `.eval` exists so the owner can reach into the running bot, and a
// snapshot in a child process cannot call client methods.
//
// The price is that evaluated code is not sandboxed and not interruptible.
// Yaegi offers no cancellation, so when an eval exceeds yaegiEvalTimeout the
// handler returns errEvalTimeout while the interpreter goroutine keeps running
// until the process restarts. Those abandoned goroutines are counted in
// evalstats and surfaced by /health, because the only cure is a restart and the
// operator needs to see them piling up.
//
// Consequences that must not be quietly dropped:
//   - `.eval` is owner-only and stays owner-only; it is equivalent to running
//     code as the process user.
//   - Output buffers are bounded and mutex-protected: an abandoned goroutine
//     goes on printing while the handler reads what it produced so far.

// Overridable for tests; production default is the historical eval budget.
var yaegiEvalTimeout = 15 * time.Second

func runLiveYaegiEval(ctx context.Context, msg *goroku.Message, client *goroku.CustomTelegramClient, db *goroku.Database, code string) (string, string, string, error) {
	// Bounded and mutex-protected: an eval abandoned at the deadline keeps
	// writing here while out() reads it. A plain bytes.Buffer both raced with
	// that read and let `for { println(...) }` grow until the process died.
	stdout := newBoundedBuffer(externalOutputLimit)
	stderr := newBoundedBuffer(externalOutputLimit)
	loader := client.Loader

	newInterpreter := func() (*interp.Interpreter, error) {
		i := interp.New(interp.Options{Stdout: stdout, Stderr: stderr})
		if err := i.Use(stdlib.Symbols); err != nil {
			return nil, err
		}
		if err := i.Use(interp.Exports{
			"gorokuctx/gorokuctx": map[string]reflect.Value{
				"Msg":    reflect.ValueOf(msg),
				"Client": reflect.ValueOf(client),
				"DB":     reflect.ValueOf(db),
				"Loader": reflect.ValueOf(loader),
			},
		}); err != nil {
			return nil, err
		}
		return i, nil
	}

	eval := func(i *interp.Interpreter, source string, needRunFunc bool) (reflect.Value, error) {
		type result struct {
			value reflect.Value
			err   error
		}
		done := make(chan result, 1)
		go func() {
			value, err := evalYaegiSource(i, source, needRunFunc)
			select {
			case done <- result{value: value, err: err}:
			default:
				// Nobody is listening: this goroutine was abandoned at the
				// deadline and has only now finished.
				evalstats.Leave()
			}
		}()
		select {
		case result := <-done:
			return result.value, result.err
		case <-ctx.Done():
			evalstats.Enter()
			return reflect.Value{}, errEvalTimeout
		}
	}

	run := func(value reflect.Value) (string, error, bool) {
		type result struct {
			text       string
			err        error
			multiValue bool
		}
		done := make(chan result, 1)
		go func() {
			text, err, multiValue := invokeYaegiRunner(value)
			select {
			case done <- result{text: text, err: err, multiValue: multiValue}:
			default:
				evalstats.Leave()
			}
		}()
		select {
		case result := <-done:
			return result.text, result.err, result.multiValue
		case <-ctx.Done():
			evalstats.Enter()
			return "", errEvalTimeout, false
		}
	}

	out := func(result string, err error) (string, string, string, error) {
		return result, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
	}

	i, err := newInterpreter()
	if err != nil {
		return out("", err)
	}
	if isFullPackageGo(code) {
		_, err = eval(i, code, false)
		return out("", err)
	}

	value, err := eval(i, buildYaegiSource(code, true), true)
	if err != nil {
		i, err = newInterpreter()
		if err != nil {
			return out("", err)
		}
		value, err = eval(i, buildYaegiSource(code, false), true)
	}
	if err != nil {
		return out("", err)
	}

	resultText, runErr, multiValue := run(value)
	if multiValue {
		i, err = newInterpreter()
		if err != nil {
			return out("", err)
		}
		value, err = eval(i, buildYaegiSource(code, false), true)
		if err != nil {
			return out("", err)
		}
		resultText, runErr, _ = run(value)
	}
	return out(resultText, runErr)
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
