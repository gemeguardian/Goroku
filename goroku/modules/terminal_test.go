package modules

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTerminalFinalTextKeepsFullOutput(t *testing.T) {
	m := &TerminalMod{}
	stdout := "stdout-start\n" + strings.Repeat("x", 5000) + "\nstdout-end"
	stderr := "stderr-start\n" + strings.Repeat("y", 2000) + "\nstderr-end"
	rc := 0

	finalText := m.buildTerminalText("test", stdout, stderr, &rc, time.Second, false)
	for _, want := range []string{"stdout-start", "stdout-end", "stderr-start", "stderr-end"} {
		if !strings.Contains(finalText, want) {
			t.Fatalf("final terminal output lost %q", want)
		}
	}

	runningText := m.buildTerminalText("test", stdout, stderr, nil, time.Second, true)
	for _, omitted := range []string{"stdout-start", "stderr-start"} {
		if strings.Contains(runningText, omitted) {
			t.Fatalf("running terminal update was not truncated: found %q", omitted)
		}
	}
}

func TestResolveTerminalShell(t *testing.T) {
	available := map[string]string{
		"bash":          "/usr/bin/bash",
		"zsh":           "/usr/bin/zsh",
		"sh":            "/bin/sh",
		"/opt/bin/fish": "/opt/bin/fish",
	}
	lookPath := func(name string) (string, error) {
		if path, ok := available[name]; ok {
			return path, nil
		}
		return "", errors.New("not found")
	}

	tests := []struct {
		name       string
		preference string
		remove     []string
		want       string
		wantErr    string
	}{
		{name: "auto preserves bash", preference: "auto", want: "/usr/bin/bash"},
		{name: "auto uses zsh without bash", preference: "auto", remove: []string{"bash"}, want: "/usr/bin/zsh"},
		{name: "auto supports minimal sh", preference: "auto", remove: []string{"bash", "zsh"}, want: "/bin/sh"},
		{name: "explicit zsh", preference: "zsh", want: "/usr/bin/zsh"},
		{name: "custom absolute path", preference: "/opt/bin/fish", want: "/opt/bin/fish"},
		{name: "explicit shell does not fall back", preference: "dash", wantErr: "unavailable"},
		{name: "relative path is rejected", preference: "./zsh", wantErr: "absolute path"},
		{name: "no shell available", preference: "auto", remove: []string{"bash", "zsh", "sh"}, wantErr: "no usable shell"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			removed := make(map[string]string, len(tt.remove))
			for _, name := range tt.remove {
				removed[name] = available[name]
				delete(available, name)
			}
			t.Cleanup(func() {
				for name, path := range removed {
					available[name] = path
				}
			})

			got, err := resolveTerminalShell(tt.preference, lookPath)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveTerminalShell() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTerminalShell() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveTerminalShell() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTerminalShellConfig(t *testing.T) {
	m := &TerminalMod{}
	if err := m.ConfigReady(map[string]any{"FLOOD_WAIT_PROTECT": int64(3), "SHELL": " zsh "}); err != nil {
		t.Fatalf("ConfigReady() error = %v", err)
	}
	floodWait, shell := m.terminalConfig()
	if floodWait != 3 || shell != "zsh" {
		t.Fatalf("terminalConfig() = (%d, %q), want (3, zsh)", floodWait, shell)
	}

	if err := m.ConfigReady(map[string]any{"SHELL": 123}); err == nil {
		t.Fatal("ConfigReady() accepted non-string shell")
	}
	_, shell = m.terminalConfig()
	if shell != "zsh" {
		t.Fatalf("invalid reload changed shell to %q", shell)
	}

	fields := m.ConfigSchema()
	for _, field := range fields {
		if field.Key == "SHELL" {
			if field.Type != "string" || field.Default != "auto" {
				t.Fatalf("SHELL schema = type %q default %#v", field.Type, field.Default)
			}
			return
		}
	}
	t.Fatal("SHELL config field is missing")
}

func TestTerminalCommandWaitCapturesFastOutput(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf trailing-output")
	var output bytes.Buffer
	cmd.Stdout = terminalWriter(func(p []byte) (int, error) {
		time.Sleep(25 * time.Millisecond)
		return output.Write(p)
	})
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got := output.String(); got != "trailing-output" {
		t.Fatalf("captured stdout = %q, want trailing-output", got)
	}
}
