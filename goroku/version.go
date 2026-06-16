package goroku

import (
	"fmt"
	"os"
	"path/filepath"

	"goroku/goroku/utils"
)

var Version = [3]int{1, 0, 0}

func init() {
	utils.VersionRaw = GetVersionString()
}

func GetVersionString() string {
	return fmt.Sprintf("%d.%d.%d", Version[0], Version[1], Version[2])
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
