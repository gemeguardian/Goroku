package utils

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func IsNoGit() bool {
	return os.Getenv("GOROKU_NO_GIT") == "1"
}

func GetGitHash() string {
	if IsNoGit() {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func GetCommitURL() string {
	hash := GetGitHash()
	if hash == "" {
		return "Unknown"
	}
	short := hash
	if len(short) > 7 {
		short = short[:7]
	}
	return `<a href="https://github.com/gemeguardian/Goroku/commit/` + hash + `">#` + short + `</a>`
}

func IsWrongUpstreamOrigin() bool {
	if IsNoGit() {
		return false
	}
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return false
	}
	origin := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(origin, "github.com/coddrago/heroku")
}

func GetGitStatus() string {
	if IsNoGit() {
		return "Git disabled"
	}
	cmd := exec.Command("git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return "Not a Git repo"
	}
	output := strings.TrimSpace(string(out))
	if output == "" {
		return "Clean"
	}
	lines := strings.Split(output, "\n")
	count := len(lines)
	word := "files"
	if count == 1 {
		word = "file"
	}
	return strconv.Itoa(count) + " " + word + " modified"
}

func GetLastCommitMessage() string {
	if IsNoGit() {
		return "Unknown"
	}
	cmd := exec.Command("git", "log", "-1", "--pretty=%B")
	out, err := cmd.Output()
	if err != nil {
		return "Unknown"
	}
	return strings.TrimSpace(string(out))
}

func GetCommitCount() int {
	if IsNoGit() {
		return 0
	}
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return count
}

// IsUpToDate checks if the local repo is up-to-date with the remote.
// It fetches the remote and compares HEAD to origin/HEAD.
func IsUpToDate() bool {
	if IsNoGit() {
		return true
	}
	if IsWrongUpstreamOrigin() {
		return true
	}
	// Fetch silently
	_ = exec.Command("git", "fetch", "--quiet").Run()

	local, errL := exec.Command("git", "rev-parse", "HEAD").Output()
	remote, errR := exec.Command("git", "rev-parse", "@{u}").Output()
	if errL != nil || errR != nil {
		return true
	}
	return strings.TrimSpace(string(local)) == strings.TrimSpace(string(remote))
}

// GetBranch returns the currently checked-out git branch name.
func GetBranch() string {
	if IsNoGit() {
		return "master"
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "master"
	}
	return strings.TrimSpace(string(out))
}
