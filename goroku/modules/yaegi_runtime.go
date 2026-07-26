package modules

import (
	"bytes"
	"context"
	"reflect"
	"strings"

	"goroku/goroku"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func runLiveYaegiEval(ctx context.Context, msg *goroku.Message, client *goroku.CustomTelegramClient, db *goroku.Database, code string) (string, string, string, error) {
	var stdout, stderr bytes.Buffer
	loader := client.Loader

	newInterpreter := func() (*interp.Interpreter, error) {
		i := interp.New(interp.Options{Stdout: &stdout, Stderr: &stderr})
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
			done <- result{value: value, err: err}
		}()
		select {
		case result := <-done:
			return result.value, result.err
		case <-ctx.Done():
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
			done <- result{text: text, err: err, multiValue: multiValue}
		}()
		select {
		case result := <-done:
			return result.text, result.err, result.multiValue
		case <-ctx.Done():
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
