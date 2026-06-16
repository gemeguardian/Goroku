package utils

import (
	"os"
	"strings"
	"testing"
)

func TestGitNoGitEnvironment(t *testing.T) {
	// Force GOROKU_NO_GIT=1 env var
	os.Setenv("GOROKU_NO_GIT", "1")
	defer os.Unsetenv("GOROKU_NO_GIT")

	if !IsNoGit() {
		t.Error("Expected IsNoGit to be true when GOROKU_NO_GIT=1")
	}

	if hash := GetGitHash(); hash != "" {
		t.Errorf("Expected empty hash, got %q", hash)
	}

	if url := GetCommitURL(); url != "Unknown" {
		t.Errorf("Expected 'Unknown' URL, got %q", url)
	}

	if IsWrongUpstreamOrigin() {
		t.Error("Expected IsWrongUpstreamOrigin=false when Git is disabled")
	}

	if status := GetGitStatus(); status != "Git disabled" {
		t.Errorf("Expected 'Git disabled', got %q", status)
	}

	if msg := GetLastCommitMessage(); msg != "Unknown" {
		t.Errorf("Expected 'Unknown' commit message, got %q", msg)
	}

	if count := GetCommitCount(); count != 0 {
		t.Errorf("Expected 0 commit count, got %d", count)
	}

	if !IsUpToDate() {
		t.Error("Expected IsUpToDate=true when Git is disabled")
	}

	if branch := GetBranch(); branch != "master" {
		t.Errorf("Expected 'master' branch, got %q", branch)
	}
}

func TestGitRealRepository(t *testing.T) {
	// Ensure GOROKU_NO_GIT is not set
	os.Unsetenv("GOROKU_NO_GIT")

	// We are running tests inside a real Git repo (/root/eblan/Goroku)
	// Some commands should succeed and return real outputs
	branch := GetBranch()
	if branch == "" {
		t.Error("GetBranch returned empty branch name in real repo")
	}

	hash := GetGitHash()
	if hash != "" {
		url := GetCommitURL()
		if !strings.Contains(url, hash) {
			t.Errorf("Expected GetCommitURL to contain hash: %q", url)
		}
	}

	status := GetGitStatus()
	if status == "" {
		t.Error("GetGitStatus returned empty status in real repo")
	}

	commitCount := GetCommitCount()
	t.Logf("Commit count: %d", commitCount)

	commitMsg := GetLastCommitMessage()
	t.Logf("Last commit message: %q", commitMsg)
}
