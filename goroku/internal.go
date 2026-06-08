package goroku

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var restarting int32


func FwProtect() {
	time.Sleep(time.Duration(1000) * time.Millisecond)
}

func Die() {
	if os.Getenv("DOCKER") != "" {
		os.Exit(0)
	}
	sysDie()
}

func Restart() {
	if !atomic.CompareAndSwapInt32(&restarting, 0, 1) {
		select {}
	}

	for _, arg := range os.Args {
		if arg == "--sandbox" {
			os.Exit(0)
		}
	}

	if os.Getenv("GOROKU_DO_NOT_RESTART2") != "" {
		fmt.Println("GorokuTL version 1.0.2 or higher is required.")
		os.Exit(0)
	}

	if os.Getenv("GOROKU_DO_NOT_RESTART") == "" {
		os.Setenv("GOROKU_DO_NOT_RESTART", "1")
	} else {
		os.Setenv("GOROKU_DO_NOT_RESTART2", "1")
	}

	execPath, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}

	// Try compiling the new binary before executing it if main.go exists
	projectDir := filepath.Dir(execPath)
	if _, err := os.Stat(filepath.Join(projectDir, "main.go")); err == nil {
		fmt.Println("🔨 Compiling new binary before restart...")
		buildCmd := exec.Command("go", "build", "-o", filepath.Base(execPath))
		buildCmd.Dir = projectDir
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Printf("⚠️ Compilation failed: %v. Restarting with old binary...\n", err)
		} else {
			fmt.Println("✅ Compilation successful!")
		}
	}

	fmt.Println("🔄 Restarting...")

	if os.Getenv("LAVHOST") != "" {
		cmd := exec.Command("lavhost", "restart")
		cmd.Run()
		return
	}

	sysRestart(execPath)
}

func PrintBanner(banner string) {
	fmt.Print("\033[2J\033[3;1f")
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(execPath)
	bannerPath := filepath.Join(baseDir, "assets", banner)

	content, err := os.ReadFile(bannerPath)
	if err == nil {
		fmt.Println(string(content))
	} else {
		// Try fallback relative path
		content, err = os.ReadFile(filepath.Join("assets", banner))
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
	content, err := os.ReadFile(headPath)
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

func ResetToMaster(repoPath string) {
	cmd1 := exec.Command("git", "reset", "--hard", "HEAD")
	cmd1.Dir = repoPath
	_ = cmd1.Run()

	cmd2 := exec.Command("git", "checkout", "master", "-f")
	cmd2.Dir = repoPath
	_ = cmd2.Run()
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

	ResetToMaster(repoPath)
	RestoreWorktree(repoPath)
	Restart()
}

func HandleAuthKeyUnregistered(tgID int64, sessionPath string) {
	fmt.Printf("🔴 AUTH_KEY_UNREGISTERED detected for client %d. Cleaning up session/config and restarting to initial state...\n", tgID)
	if sessionPath != "" {
		_ = os.Remove(sessionPath)
	}
	configPath := filepath.Join(BaseDir, fmt.Sprintf("config-%d.json", tgID))
	_ = os.Remove(configPath)
	Restart()
}

