package modules

import (
	"testing"
)

func TestParsePhone(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"+1 (555) 123-4567", "+15551234567"},
		{"+79123456789", "+79123456789"},
		{"  +44 20 7946 0958  ", "+442079460958"},
		{"123-456-7890", "1234567890"},
		{"", ""},
		{"abc", ""},
		{"+abc", "+"},
		{"+7 (9**) ***-**-89", "+7989"},
	}

	for _, tc := range tests {
		got := parsePhone(tc.input)
		if got != tc.want {
			t.Errorf("parsePhone(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}
