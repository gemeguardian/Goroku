package main

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strings"

	"goroku/goroku"
	"goroku/goroku/modules"
)

func main() {
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
			fmt.Println("Type force_insecure to ignore this warning")
			fmt.Println("Type no_sudo if your system has no sudo (Debian vibes)")
			fmt.Print("> ")

			reader := bufio.NewReader(os.Stdin)
			text, _ := reader.ReadString('\n')
			text = strings.TrimSpace(strings.ToLower(text))

			if text == "no_sudo" {
				os.Setenv("NO_SUDO", "1")
				fmt.Println("Added NO_SUDO in your environment variables")
				goroku.Restart()
				return
			} else if text != "force_insecure" {
				os.Exit(1)
			}
		}
	}

	// Clean up restart variables
	os.Unsetenv("GOROKU_DO_NOT_RESTART")
	os.Unsetenv("GOROKU_DO_NOT_RESTART2")

	// Call main runner of goroku package with the registered static modules
	goroku.Main([]goroku.Module{
		&modules.APIProtection{},
		&modules.Eval{},
		&modules.Help{},
		&modules.GorokuBackup{},
		&modules.GorokuConfig{},
		&modules.GorokuInfo{},
		&modules.GorokuPluginSecurity{},
		&modules.GorokuSecurity{},
		&modules.GorokuSettings{},
		&modules.GorokuWeb{},
		&modules.InlineStuff{},
		&modules.LoaderModule{},
		&modules.Presets{},
		&modules.Quickstart{},
		&modules.SettingsModule{},
		&modules.TerminalMod{},
		&modules.Test{},
		&modules.Translate{},
		&modules.TranslationsModule{},
		&modules.Updater{},
	})
}
