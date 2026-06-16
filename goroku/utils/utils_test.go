package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGetArgs(t *testing.T) {
	// Test GetArgsRaw
	if got := GetArgsRaw(".command arg1 arg2"); got != "arg1 arg2" {
		t.Errorf("GetArgsRaw failed: expected 'arg1 arg2', got %q", got)
	}
	if got := GetArgsRaw(".command"); got != "" {
		t.Errorf("GetArgsRaw failed: expected empty, got %q", got)
	}

	// Test GetArgs
	args := GetArgs(".command arg1   arg2  arg3")
	expected := []string{"arg1", "arg2", "arg3"}
	if !reflect.DeepEqual(args, expected) {
		t.Errorf("GetArgs failed: expected %v, got %v", expected, args)
	}

	// Test GetArgsSplitBy
	splitArgs := GetArgsSplitBy(".command arg1 | arg2 | arg3", "|")
	if !reflect.DeepEqual(splitArgs, expected) {
		t.Errorf("GetArgsSplitBy failed: expected %v, got %v", expected, splitArgs)
	}

	// Test GetArgsInt
	intArgs := GetArgsInt(".command 10 20 abc 30")
	expectedInts := []int{10, 20, 30}
	if !reflect.DeepEqual(intArgs, expectedInts) {
		t.Errorf("GetArgsInt failed: expected %v, got %v", expectedInts, intArgs)
	}

	// Test GetArgsBool
	boolArgs := GetArgsBool(".command true yes 1 on false no 0 off invalid")
	expectedBools := []bool{true, true, true, true, false, false, false, false}
	if !reflect.DeepEqual(boolArgs, expectedBools) {
		t.Errorf("GetArgsBool failed: expected %v, got %v", expectedBools, boolArgs)
	}
}

func TestOtherUtils(t *testing.T) {
	// Test Rand length
	r1 := Rand(10)
	if len(r1) != 10 {
		t.Errorf("Rand failed: expected length 10, got %d", len(r1))
	}
	r2 := Rand(10)
	if r1 == r2 {
		t.Error("Rand failed: generated identical random strings")
	}

	// Test Merge
	a := map[string]interface{}{
		"key1": "val1",
		"nested": map[string]interface{}{
			"n1": "v1",
		},
	}
	b := map[string]interface{}{
		"key2": "val2",
		"nested": map[string]interface{}{
			"n2": "v2",
		},
	}
	merged := Merge(a, b, true)
	expectedMerged := map[string]interface{}{
		"key1": "val1",
		"key2": "val2",
		"nested": map[string]interface{}{
			"n1": "v1",
			"n2": "v2",
		},
	}
	if !reflect.DeepEqual(merged, expectedMerged) {
		t.Errorf("Merge failed: expected %v, got %v", expectedMerged, merged)
	}

	// Test Chunks
	list := []interface{}{1, 2, 3, 4, 5}
	chunks := Chunks(list, 2)
	if len(chunks) != 3 {
		t.Fatalf("Chunks failed: expected 3 chunks, got %d", len(chunks))
	}
	if !reflect.DeepEqual(chunks[0], []interface{}{1, 2}) ||
		!reflect.DeepEqual(chunks[1], []interface{}{3, 4}) ||
		!reflect.DeepEqual(chunks[2], []interface{}{5}) {
		t.Errorf("Chunks failed: unexpected chunk layout %v", chunks)
	}

	// Test FormatFileSize
	if got := FormatFileSize(0); got != "0 B" {
		t.Errorf("FormatFileSize(0) failed: got %q", got)
	}
	if got := FormatFileSize(1024); got != "1.0 KB" {
		t.Errorf("FormatFileSize(1024) failed: got %q", got)
	}
	if got := FormatFileSize(1048576); got != "1.0 MB" {
		t.Errorf("FormatFileSize(1048576) failed: got %q", got)
	}

	// Test IsURL
	if !IsURL("https://google.com/search") {
		t.Error("IsURL failed for valid URL")
	}
	if IsURL("google.com") {
		t.Error("IsURL should fail for URL without scheme")
	}

	// Test IsURLRegex
	if !IsURLRegex("https://google.com") {
		t.Error("IsURLRegex failed for valid URL")
	}
	if IsURLRegex("invalid-link") {
		t.Error("IsURLRegex should fail for invalid link")
	}
}

func TestCensorSensitive(t *testing.T) {
	// Test Bot Token pattern censoring
	inputToken := "Here is my bot token 123456789:AAF123456789_abcdefghijklmnopqrstuv"
	censored := CensorSensitive(inputToken)
	expected := "Here is my bot token [REDACTED]"
	if censored != expected {
		t.Errorf("CensorSensitive failed for bot token: expected %q, got %q", expected, censored)
	}

	// Test Redis URL censoring
	inputRedis := "redis://user:password@localhost:6379/0"
	censored = CensorSensitive(inputRedis)
	if censored != "[REDACTED]" {
		t.Errorf("CensorSensitive failed for Redis URL: expected '[REDACTED]', got %q", censored)
	}

	// Test Hex String censoring (32 chars hex key)
	inputHex := "my key is abcdef0123456789abcdef0123456789"
	censored = CensorSensitive(inputHex)
	if censored != "my key is [REDACTED]" {
		t.Errorf("CensorSensitive failed for hex key: expected 'my key is [REDACTED]', got %q", censored)
	}

	// Test extras censoring
	inputExtra := "Please censor mysecretword"
	censored = CensorSensitive(inputExtra, "mysecretword")
	if censored != "Please censor [REDACTED]" {
		t.Errorf("CensorSensitive failed for extras: expected 'Please censor [REDACTED]', got %q", censored)
	}

	// Test environment variable censoring
	os.Setenv("BOT_TOKEN", "super_secret_env_token_value")
	defer os.Unsetenv("BOT_TOKEN")
	inputEnv := "Token is super_secret_env_token_value"
	censored = CensorSensitive(inputEnv)
	if censored != "Token is [REDACTED]" {
		t.Errorf("CensorSensitive failed for env vars: expected 'Token is [REDACTED]', got %q", censored)
	}
}

func TestSecureFile(t *testing.T) {
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test.txt")

	err := os.WriteFile(tempFile, []byte("hello"), 0644)
	if err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	SecureFile(tempFile)

	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatalf("Failed to stat temp file: %v", err)
	}

	// On Unix systems, verify permission is 0600 (-rw-------)
	// We mask with 0777 to check owner, group, other permissions
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("SecureFile failed: expected file permissions to be 0600, got %O", perm)
	}
}

func TestNetworkUtils(t *testing.T) {
	hostname := GetHostname()
	if hostname == "" {
		t.Error("GetHostname returned empty string")
	}

	ip := GetIPAddress()
	if ip == "" {
		t.Error("GetIPAddress returned empty string")
	}

	interfaces := GetNetworkInterfaces()
	// Should at least compile and return a map
	if interfaces == nil {
		t.Error("GetNetworkInterfaces returned nil map")
	}

	// ResolveDomain should either succeed or return "Unable to resolve"
	resolved := ResolveDomain("localhost")
	if resolved == "" {
		t.Error("ResolveDomain returned empty string")
	}

	// IsPortOpen checking closed ports should be false
	isOpen := IsPortOpen("127.0.0.1", 9999) // using a high port that is likely closed
	t.Logf("Port 9999 open: %t", isOpen)
}

func TestPlatformUtils(t *testing.T) {
	upt := Uptime()
	if upt < 0 {
		t.Errorf("Uptime returned negative value: %d", upt)
	}

	formatted := FormattedUptime()
	if formatted == "" {
		t.Error("FormattedUptime returned empty string")
	}

	ram := GetRAMUsage()
	if ram <= 0 {
		t.Errorf("GetRAMUsage returned non-positive value: %f", ram)
	}

	cpu := GetCPUUsage()
	if cpu != "0.00" {
		t.Errorf("Expected GetCPUUsage to be '0.00', got %q", cpu)
	}

	platform := GetPlatformName()
	if platform == "" {
		t.Error("GetPlatformName returned empty string")
	}

	emoji := GetPlatformEmoji()
	if emoji == "" {
		t.Error("GetPlatformEmoji returned empty string")
	}

	goPath := GetGoPath()
	if goPath == "" {
		t.Error("GetGoPath returned empty string")
	}
}

func TestPlatformUnixUtils(t *testing.T) {
	disk := GetDiskUsage()
	if disk == "" {
		t.Error("GetDiskUsage returned empty string")
	}
}

