package goroku

import (
	"os"
	"path/filepath"
)

var Version = [3]int{2, 0, 0}

func GetVersionString() string {
	return "2.0.0"
}

func IsNoGit() bool {
	return os.Getenv("GOROKU_NO_GIT") == "1"
}

func GetVersionBranch() string {
	if IsNoGit() {
		return "master"
	}
	execPath, err := os.Executable()
	if err != nil {
		return "master"
	}
	repoPath := filepath.Dir(filepath.Dir(execPath))
	return GetBranchName(repoPath)
}
