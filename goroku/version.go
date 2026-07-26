package goroku

import (
	"fmt"
	"os"
	"path/filepath"

	"goroku/goroku/utils"
)

// Version is the default SemVer triple when VersionInfo is unset.
var Version = [3]int{1, 0, 0}

// DefaultVersionInfo is the hardcoded fallback VersionInfo for plain `go build`
// / dev builds with no ldflags injection. It mirrors the default value of
// VersionInfo and is the reference for IsReleaseBuild.
const DefaultVersionInfo = "1.0.0"

// VersionInfo is the release version string shown to users.
// Override at link time, for example:
//
//	go build -ldflags "-X goroku/goroku.VersionInfo=1.2.3"
var VersionInfo = DefaultVersionInfo

// Commit is an optional VCS revision (short SHA). Override at link time:
//
//	go build -ldflags "-X goroku/goroku.Commit=$(git rev-parse --short HEAD)"
var Commit = ""

func init() {
	utils.VersionRaw = GetVersionString()
}

func GetVersionString() string {
	if VersionInfo != "" {
		return VersionInfo
	}
	return fmt.Sprintf("%d.%d.%d", Version[0], Version[1], Version[2])
}

// IsReleaseBuild reports whether this binary was produced by a tagged release
// pipeline (ldflags injection) rather than a plain `go build` / dev build.
// It returns true when VersionInfo differs from the hardcoded default OR when
// Commit was injected. A dev build (default VersionInfo and empty Commit)
// returns false. Used by release tooling and health surfaces to distinguish a
// real release artifact from a fallback build.
func IsReleaseBuild() bool {
	return VersionInfo != DefaultVersionInfo || Commit != ""
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
