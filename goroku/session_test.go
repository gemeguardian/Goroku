package goroku

import (
	"testing"
)

func TestGetTGIDFromSessionPath(t *testing.T) {
	tests := []struct {
		path    string
		wantID  int64
		wantErr bool
	}{
		{"/path/to/goroku-123456789.session", 123456789, false},
		{"goroku-0.session", 0, false},
		{"heroku-42.session", 42, false},
		{"hikka-999.session", 999, false},
		{"invalid.session", 0, true},
		{"goroku-abc.session", 0, true},
		{"", 0, true},
	}

	for _, tc := range tests {
		got, err := getTGIDFromSessionPath(tc.path)
		if tc.wantErr {
			if err == nil {
				t.Errorf("getTGIDFromSessionPath(%q) = %d, nil; want error", tc.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("getTGIDFromSessionPath(%q) error: %v", tc.path, err)
			continue
		}
		if got != tc.wantID {
			t.Errorf("getTGIDFromSessionPath(%q) = %d; want %d", tc.path, got, tc.wantID)
		}
	}
}
