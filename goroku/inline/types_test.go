package inline

import (
	"testing"
)

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		inputs   []string
		expected string
	}{
		{[]string{"", "hello", "world"}, "hello"},
		{[]string{"", "", ""}, ""},
		{[]string{"apple", "banana"}, "apple"},
	}

	for _, tc := range tests {
		got := firstNonEmpty(tc.inputs...)
		if got != tc.expected {
			t.Errorf("firstNonEmpty(%v) = %q; want %q", tc.inputs, got, tc.expected)
		}
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<b>hello</b>", "hello"},
		{"hello <a href=''>world</a>!", "hello world!"},
		{"plain text", "plain text"},
	}

	for _, tc := range tests {
		got := stripHTML(tc.input)
		if got != tc.expected {
			t.Errorf("stripHTML(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}
