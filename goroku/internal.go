package goroku

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var activeAppState struct {
	sync.Mutex
	app *Goroku
}

func FwProtect() {
	time.Sleep(time.Duration(1000) * time.Millisecond)
}

// Die is a compatibility shim that requests a coordinated application stop.
func Die() {
	activeAppState.Lock()
	app := activeAppState.app
	activeAppState.Unlock()
	if app != nil {
		app.RequestStop()
	}
}

// Restart is a compatibility shim that requests coordinated shutdown. Process
// replacement is the responsibility of the package main that called Run.
func Restart() {
	activeAppState.Lock()
	app := activeAppState.app
	activeAppState.Unlock()
	if app != nil {
		app.RequestRestart()
	}
}

func setActiveApp(app *Goroku) bool {
	activeAppState.Lock()
	defer activeAppState.Unlock()
	if activeAppState.app != nil && activeAppState.app != app {
		return false
	}
	activeAppState.app = app
	return true
}

func clearActiveApp(app *Goroku) {
	activeAppState.Lock()
	if activeAppState.app == app {
		activeAppState.app = nil
	}
	activeAppState.Unlock()
}

func PrintBanner(banner string) {
	fmt.Print("\033[2J\033[3;1f")
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(execPath)
	bannerPath := filepath.Join(baseDir, "assets", banner)

	content, err := os.ReadFile(bannerPath) //nolint:gosec
	if err == nil {
		fmt.Println(string(content))
	} else {
		// Try fallback relative path
		content, err = os.ReadFile(filepath.Join("assets", banner)) //nolint:gosec
		if err == nil {
			fmt.Println(string(content))
		}
	}
}

func CheckCommitAncestor(commit, repoPath string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", commit, "refs/remotes/origin/master")
	cmd.Dir = repoPath
	err := cmd.Run()
	return err == nil
}

func GetBranchName(repoPath string) string {
	headPath := filepath.Join(repoPath, ".git", "HEAD")
	content, err := os.ReadFile(headPath) //nolint:gosec
	if err == nil {
		lines := strings.Split(string(content), "\n")
		if len(lines) > 0 {
			line := strings.TrimSpace(lines[0])
			if strings.HasPrefix(line, "ref:") {
				parts := strings.Split(line, "/")
				if len(parts) > 0 {
					return parts[len(parts)-1]
				}
			}
		}
	}

	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}

	return "master"
}

func ResetToMaster(repoPath string) error {
	cmd1 := exec.Command("git", "reset", "--hard", "HEAD")
	cmd1.Dir = repoPath
	if err := cmd1.Run(); err != nil {
		return fmt.Errorf("git reset hard failed: %w", err)
	}

	cmd2 := exec.Command("git", "checkout", "master", "-f")
	cmd2.Dir = repoPath
	if err := cmd2.Run(); err != nil {
		return fmt.Errorf("git checkout master failed: %w", err)
	}
	return nil
}

func RestoreWorktree(repoPath string) bool {
	cmd1 := exec.Command("git", "restore", ".")
	cmd1.Dir = repoPath
	if err := cmd1.Run(); err == nil {
		return true
	}

	cmd2 := exec.Command("git", "reset", "--hard")
	cmd2.Dir = repoPath
	return cmd2.Run() == nil
}

func CheckBranch(meID int64, allowedIDs []int64) {
	if os.Getenv("GOROKU_NO_GIT") == "1" {
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		return
	}
	repoPath := filepath.Dir(filepath.Dir(execPath))

	isAllowed := false
	for _, id := range allowedIDs {
		if meID == id {
			isAllowed = true
			break
		}
	}

	if isAllowed {
		return
	}

	branchName := GetBranchName(repoPath)
	isAncestor := CheckCommitAncestor("origin/master", repoPath) // Or equivalent commit ancestry check
	if isAncestor || branchName == "master" {
		return
	}

	if err := ResetToMaster(repoPath); err != nil {
		fmt.Printf("Error resetting to master: %v\n", err)
	}
	if !RestoreWorktree(repoPath) {
		fmt.Println("Error restoring worktree")
	}
	Restart()
}

func HandleAuthKeyUnregistered(tgID int64, sessionPath string) {
	fmt.Printf("🔴 AUTH_KEY_UNREGISTERED detected for client %d. Cleaning up session/config and restarting to initial state...\n", tgID)
	if sessionPath != "" {
		if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Error removing session: %v\n", err)
		}
	}
	configPath := filepath.Join(BaseDir, fmt.Sprintf("config-%d.json", tgID))
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Error removing config: %v\n", err)
	}
	Restart()
}
