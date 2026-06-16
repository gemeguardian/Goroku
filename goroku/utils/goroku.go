package utils

import (
	"os"
	"path/filepath"
)

var VersionRaw = "1.0.0"

func GetVersionRaw() string {
	return VersionRaw
}

func GetBaseDir() string {
	execPath, err := os.Executable()
	if err == nil {
		return filepath.Dir(execPath)
	}
	return "."
}

// GetDir returns the path to the goroku package directory.
// This mirrors Python's utils.get_dir() helper.
func GetDir() string {
	return filepath.Join(GetBaseDir(), "goroku")
}
