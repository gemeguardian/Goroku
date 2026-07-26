package modules

import "testing"

func TestFormatHelpQuoteHasNoLeadingBlankLine(t *testing.T) {
	got := formatHelpQuote([]string{"first", "second"})
	want := "<blockquote expandable>first\nsecond</blockquote>"
	if got != want {
		t.Fatalf("formatHelpQuote() = %q, want %q", got, want)
	}
}

func TestHelpQuotesAreSeparated(t *testing.T) {
	got := formatHelpQuote([]string{"core"}) + "\n" + formatHelpQuote([]string{"user"})
	want := "<blockquote expandable>core</blockquote>\n<blockquote expandable>user</blockquote>"
	if got != want {
		t.Fatalf("help quotes = %q, want %q", got, want)
	}
}
