package goroku

import (
	"os"
	"regexp"
	"testing"
)

func TestVersionInfo(t *testing.T) {
	// Test GetVersionString format (semantic versioning like X.Y.Z)
	ver := GetVersionString()
	matched, _ := regexp.MatchString(`^\d+\.\d+\.\d+$`, ver)
	if !matched {
		t.Errorf("Expected semantic version format (X.Y.Z), got %q", ver)
	}

	// Test IsNoGit environment variable
	os.Setenv("GOROKU_NO_GIT", "1")
	if !IsNoGit() {
		t.Error("Expected IsNoGit to be true when GOROKU_NO_GIT=1")
	}
	if branch := GetVersionBranch(); branch != "master" {
		t.Errorf("Expected GetVersionBranch to be 'master' when Git is disabled, got %q", branch)
	}

	os.Unsetenv("GOROKU_NO_GIT")
	if IsNoGit() {
		t.Error("Expected IsNoGit to be false when GOROKU_NO_GIT is unset")
	}
}
