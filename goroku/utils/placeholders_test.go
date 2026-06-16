package utils

import (
	"testing"
)

func TestPlaceholdersLifecycleAndFormatting(t *testing.T) {
	// 1. Register test placeholders
	RegisterPlaceholder("user_name", func() string {
		return "Alice"
	}, "test_mod", "The name of the user")

	RegisterPlaceholder("user_age", func() string {
		return "30"
	}, "test_mod", "The age of the user")

	RegisterPlaceholder("sys_ver", func() string {
		return "1.0.0"
	}, "sys_mod", "System version")

	// 2. Verify GetPlaceholder
	if val := GetPlaceholder("user_name"); val != "Alice" {
		t.Errorf("Expected 'Alice', got '%s'", val)
	}
	if val := GetPlaceholder("nonexistent"); val != "" {
		t.Errorf("Expected empty string for nonexistent placeholder, got '%s'", val)
	}

	// 3. Verify FormatPlaceholders
	msg := "Hello {user_name}, you are {user_age} years old. System version is {sys_ver}."
	formatted := FormatPlaceholders(msg)
	expected := "Hello Alice, you are 30 years old. System version is 1.0.0."
	if formatted != expected {
		t.Errorf("Expected: %q, got: %q", expected, formatted)
	}

	// 4. Verify HelpPlaceholders
	help := HelpPlaceholders("test_mod", "⭐")
	if len(help) != 2 {
		t.Fatalf("Expected 2 help strings, got %d", len(help))
	}
	// The order in maps is random, so verify both exist
	hasNameHelp := false
	hasAgeHelp := false
	for _, h := range help {
		if h == "⭐ {user_name} - The name of the user" {
			hasNameHelp = true
		}
		if h == "⭐ {user_age} - The age of the user" {
			hasAgeHelp = true
		}
	}
	if !hasNameHelp || !hasAgeHelp {
		t.Errorf("Help strings are incorrect: %v", help)
	}

	// 5. Verify UnregisterPlaceholders
	UnregisterPlaceholders("test_mod")
	if val := GetPlaceholder("user_name"); val != "" {
		t.Errorf("Expected empty string for unregistered placeholder, got '%s'", val)
	}
	if val := GetPlaceholder("sys_ver"); val != "1.0.0" {
		t.Errorf("Expected '1.0.0' for placeholder from other module, got '%s'", val)
	}
}
