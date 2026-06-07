package utils

import (
	"os"
	"path/filepath"
)

func GetVersionRaw() string {
	return "2.0.0"
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
