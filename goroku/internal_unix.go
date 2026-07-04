//go:build linux || android
// +build linux android

package goroku

import (
	"os"
	"syscall"
)

func sysDie() {
	pgid, err := syscall.Getpgid(os.Getpid())
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}
	os.Exit(0)
}

func sysRestart(execPath string) {
	_ = syscall.Exec(execPath, os.Args, os.Environ()) //nolint:gosec
	os.Exit(1)
}
