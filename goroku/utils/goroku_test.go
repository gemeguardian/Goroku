package utils

import (
	"regexp"
	"strings"
	"testing"
)

func TestGorokuUtils(t *testing.T) {
	// 1. GetVersionRaw
	ver := GetVersionRaw()
	matched, _ := regexp.MatchString(`^\d+\.\d+\.\d+$`, ver)
	if !matched {
		t.Errorf("Expected semantic version format (X.Y.Z), got %q", ver)
	}

	// 2. GetBaseDir
	baseDir := GetBaseDir()
	if baseDir == "" {
		t.Error("GetBaseDir returned empty string")
	}

	// 3. GetDir
	dir := GetDir()
	if !strings.HasSuffix(dir, "goroku") {
		t.Errorf("Expected GetDir to end with 'goroku', got %q", dir)
	}
}
