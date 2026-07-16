package modules

import (
	"reflect"
	"testing"
)

func TestFilterLogsByLevel(t *testing.T) {
	lines := []string{
		"2026-06-17 18:55:06 INFO goroku/loader.go:125 Successfully registered module",
		"2026-06-17 18:55:07 WARN goroku/dispatcher.go:527 Rate limit exceeded",
		"2026-06-17 18:55:08 ERROR goroku/web/root.go:447 Failed to connect",
		"2026-06-17 18:55:09 DEBUG goroku/client.go:328 gotd client run",
		"2026-06-17 18:55:10 CRITICAL goroku/bootstrap.go:353 Panic recovered",
		"2026-06-17 18:55:11 PANIC goroku/bootstrap.go:353 Recovered",
		"2026-06-17 18:55:12 FATAL goroku/bootstrap.go:353 Fatal error",
	}

	tests := []struct {
		name     string
		level    int
		expected []string
	}{
		{
			name:     "ALL returns everything",
			level:    0,
			expected: lines,
		},
		{
			name:     "DEBUG returns all",
			level:    10,
			expected: lines,
		},
		{
			name:  "INFO filters out DEBUG",
			level: 20,
			expected: []string{
				"2026-06-17 18:55:06 INFO goroku/loader.go:125 Successfully registered module",
				"2026-06-17 18:55:07 WARN goroku/dispatcher.go:527 Rate limit exceeded",
				"2026-06-17 18:55:08 ERROR goroku/web/root.go:447 Failed to connect",
				"2026-06-17 18:55:10 CRITICAL goroku/bootstrap.go:353 Panic recovered",
				"2026-06-17 18:55:11 PANIC goroku/bootstrap.go:353 Recovered",
				"2026-06-17 18:55:12 FATAL goroku/bootstrap.go:353 Fatal error",
			},
		},
		{
			name:  "WARNING filters out INFO and DEBUG",
			level: 30,
			expected: []string{
				"2026-06-17 18:55:07 WARN goroku/dispatcher.go:527 Rate limit exceeded",
				"2026-06-17 18:55:08 ERROR goroku/web/root.go:447 Failed to connect",
				"2026-06-17 18:55:10 CRITICAL goroku/bootstrap.go:353 Panic recovered",
				"2026-06-17 18:55:11 PANIC goroku/bootstrap.go:353 Recovered",
				"2026-06-17 18:55:12 FATAL goroku/bootstrap.go:353 Fatal error",
			},
		},
		{
			name:  "ERROR filters out INFO, DEBUG, WARN",
			level: 40,
			expected: []string{
				"2026-06-17 18:55:08 ERROR goroku/web/root.go:447 Failed to connect",
				"2026-06-17 18:55:10 CRITICAL goroku/bootstrap.go:353 Panic recovered",
				"2026-06-17 18:55:11 PANIC goroku/bootstrap.go:353 Recovered",
				"2026-06-17 18:55:12 FATAL goroku/bootstrap.go:353 Fatal error",
			},
		},
		{
			name:  "CRITICAL filters out everything except critical/panic/fatal",
			level: 60,
			expected: []string{
				"2026-06-17 18:55:10 CRITICAL goroku/bootstrap.go:353 Panic recovered",
				"2026-06-17 18:55:11 PANIC goroku/bootstrap.go:353 Recovered",
				"2026-06-17 18:55:12 FATAL goroku/bootstrap.go:353 Fatal error",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterLogsByLevel(lines, tc.level)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("filterLogsByLevel(%d):\ngot:\n%v\nwant:\n%v", tc.level, got, tc.expected)
			}
		})
	}
}

func TestFilterLogsByLevelEmpty(t *testing.T) {
	got := filterLogsByLevel([]string{}, 20)
	if len(got) != 0 {
		t.Errorf("Expected empty result for empty input, got %d lines", len(got))
	}
}
