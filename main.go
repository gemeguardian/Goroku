package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"goroku/goroku"
	"goroku/goroku/modules"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Root user warning, mirroring __main__.py logic
	currentUser, err := user.Current()
	if err == nil && currentUser.Username == "root" {
		hasRootArg := false
		for _, arg := range os.Args {
			if arg == "--root" {
				hasRootArg = true
				break
			}
		}

		hasTriggerEnv := false
		for _, envKey := range []string{"DOCKER", "NO_SUDO"} {
			if os.Getenv(envKey) != "" {
				hasTriggerEnv = true
				break
			}
		}

		if !hasRootArg && !hasTriggerEnv {
			fmt.Println(strings.Repeat("🚷", 15))
			fmt.Println("You attempted to run Goroku on behalf of root user")
			fmt.Println("Please, create a new user and restart script")
			fmt.Println("If this action was intentional, pass --root argument instead")
			fmt.Println(strings.Repeat("🚷", 15))
			fmt.Println()

			noSudo, guardErr := confirmRootRun(os.Stdin, stdinIsTerminal())
			if guardErr != nil {
				return guardErr
			}
			if noSudo {
				_ = os.Setenv("NO_SUDO", "1")
				fmt.Println("Added NO_SUDO in your environment variables")
			}
		}
	}

	app := goroku.NewApp([]goroku.ModuleFactory{
		func() goroku.Module { return &modules.APIProtection{} },
		func() goroku.Module { return &modules.Eval{} },
		func() goroku.Module { return &modules.Help{} },
		func() goroku.Module { return &modules.GorokuBackup{} },
		func() goroku.Module { return &modules.GorokuConfig{} },
		func() goroku.Module { return &modules.GorokuInfo{} },
		func() goroku.Module { return &modules.GorokuSecurity{} },
		func() goroku.Module { return &modules.GorokuSettings{} },
		func() goroku.Module { return &modules.GorokuWeb{} },
		func() goroku.Module { return &modules.InlineStuff{} },
		func() goroku.Module { return &modules.LoaderModule{} },
		func() goroku.Module { return &modules.Presets{} },
		func() goroku.Module { return &modules.SettingsModule{} },
		func() goroku.Module { return &modules.TerminalMod{} },
		func() goroku.Module { return &modules.Test{} },
		func() goroku.Module { return &modules.Translate{} },
		func() goroku.Module { return &modules.TranslationsModule{} },
		func() goroku.Module { return &modules.Updater{} },
	})
	app.SetClientReadyHook(modules.StartQuickstart)
	if doctorFlagRequested(os.Args[1:]) {
		app.ParseArguments()
		if app.Doctor || app.ConfigValidate {
			os.Exit(goroku.RunDoctor(goroku.DoctorOptions{
				ConfigPath:         goroku.ConfigPath,
				DataRoot:           goroku.BaseDir,
				ConfigValidateOnly: app.ConfigValidate,
			}))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = app.Run(ctx)
	if restartRequested(err) {
		if err != goroku.ErrRestartRequested {
			fmt.Fprintf(os.Stderr, "restart requested after lifecycle errors: %v\n", err)
		}
		if app.Sandbox {
			return nil
		}
		return restartProcess()
	}
	if errors.Is(err, context.Canceled) {
		if err != context.Canceled {
			return err
		}
		return nil
	}
	return err
}

func restartRequested(err error) bool {
	return errors.Is(err, goroku.ErrRestartRequested)
}

func doctorFlagRequested(args []string) bool {
	for _, a := range args {
		switch a {
		case "--doctor", "--doctor=true", "--config-validate", "--config-validate=true":
			return true
		}
	}
	return false
}

func nextRestartGuard(first, second bool) (setFirst, setSecond bool, err error) {
	if second {
		return false, false, fmt.Errorf("restart aborted: the process already restarted twice without becoming healthy")
	}
	if first {
		return false, true, nil
	}
	return true, false, nil
}

func restartProcess() error {
	setFirst, setSecond, err := nextRestartGuard(os.Getenv("GOROKU_DO_NOT_RESTART") != "", os.Getenv("GOROKU_DO_NOT_RESTART2") != "")
	if err != nil {
		return err
	}
	if setFirst {
		_ = os.Setenv("GOROKU_DO_NOT_RESTART", "1")
	}
	if setSecond {
		_ = os.Setenv("GOROKU_DO_NOT_RESTART2", "1")
	}
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	projectDir := filepath.Dir(execPath)
	if _, err := os.Stat(filepath.Join(projectDir, "main.go")); err == nil {
		fmt.Println("Compiling new binary before restart...")
		buildCmd := exec.Command("go", "build", "-o", filepath.Base(execPath)) //nolint:gosec
		buildCmd.Dir = projectDir
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Printf("Compilation failed: %v. Restarting with old binary...\n", err)
		}
	}
	fmt.Println("Restarting...")
	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil { //nolint:gosec
		return fmt.Errorf("replace process: %w", err)
	}
	return nil
}

// errRootNoTerminal explains how to start as root when nobody can answer the
// prompt. Under systemd or cron the process used to die with a bare
// "refusing to run as root" and no indication of what to do about it.
var errRootNoTerminal = errors.New(
	"refusing to run as root: started without a terminal, so the confirmation prompt cannot be answered.\n" +
		"Pass --root on the command line, or set NO_SUDO=1 (or DOCKER=1) in the unit's environment,\n" +
		"or — preferably — run the service as an unprivileged user")

// stdinIsTerminal reports whether stdin is a character device. It is a fast
// pre-check only: /dev/null is a character device too, which is why
// confirmRootRun also treats an unanswerable read as "no terminal".
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// confirmRootRun runs the interactive root confirmation. It reports
// errRootNoTerminal whenever the prompt cannot be answered — no character
// device at all (pipe, socket, closed handle), or a device that immediately
// yields EOF (/dev/null under systemd's StandardInput=null).
func confirmRootRun(stdin io.Reader, isTerminal bool) (noSudo bool, err error) {
	if !isTerminal {
		return false, errRootNoTerminal
	}

	fmt.Println("Type force_insecure to ignore this warning")
	fmt.Println("Type no_sudo if your system has no sudo (Debian vibes)")
	fmt.Print("> ")

	text, readErr := bufio.NewReader(stdin).ReadString('\n')
	text = strings.TrimSpace(strings.ToLower(text))
	if readErr != nil && text == "" {
		return false, errRootNoTerminal
	}

	switch text {
	case "no_sudo":
		return true, nil
	case "force_insecure":
		return false, nil
	}
	return false, fmt.Errorf("refusing to run as root")
}
