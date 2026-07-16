package main

import (
	"errors"
	"testing"

	"goroku/goroku"
)

func TestRestartRequestedRecognizesJoinedResult(t *testing.T) {
	if !restartRequested(errors.Join(goroku.ErrRestartRequested, errors.New("shutdown failed"))) {
		t.Fatal("joined restart result was not recognized")
	}
	if restartRequested(errors.New("ordinary failure")) {
		t.Fatal("ordinary failure requested restart")
	}
}

func TestNextRestartGuardPersistsAcrossExec(t *testing.T) {
	tests := []struct {
		name                string
		first, second       bool
		setFirst, setSecond bool
		wantErr             bool
	}{
		{name: "first restart", setFirst: true},
		{name: "second restart", first: true, setSecond: true},
		{name: "loop blocked", first: true, second: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setFirst, setSecond, err := nextRestartGuard(test.first, test.second)
			if (err != nil) != test.wantErr || setFirst != test.setFirst || setSecond != test.setSecond {
				t.Fatalf("nextRestartGuard = %v, %v, %v", setFirst, setSecond, err)
			}
		})
	}
}
