package goroku

import (
	"reflect"
	"testing"
)

func TestMessageArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "command with args", raw: ".html <b>hi</b>", want: "<b>hi</b>"},
		{name: "command only", raw: ".html", want: ""},
		{name: "trailing space only", raw: ".html   ", want: ""},
		{name: "multiple words", raw: ".say hello there world", want: "hello there world"},
		{name: "inner spacing preserved", raw: ".say a  b", want: "a  b"},
		{name: "empty", raw: "", want: ""},
		{name: "newline separated", raw: ".eval\nprintln(1)", want: "println(1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &Message{RawText: tc.raw}
			if got := msg.Args(); got != tc.want {
				t.Errorf("Args() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMessageArgsFallsBackToText(t *testing.T) {
	msg := &Message{Text: ".say fallback"}
	if got := msg.Args(); got != "fallback" {
		t.Errorf("Args() = %q, want %q", got, "fallback")
	}
}

func TestMessageArgsList(t *testing.T) {
	msg := &Message{RawText: ".ban  42   spam here "}
	want := []string{"42", "spam", "here"}
	if got := msg.ArgsList(); !reflect.DeepEqual(got, want) {
		t.Errorf("ArgsList() = %#v, want %#v", got, want)
	}

	if got := (&Message{RawText: ".ban"}).ArgsList(); got != nil {
		t.Errorf("ArgsList() with no args = %#v, want nil", got)
	}
}

// Arg is bounds-safe so callers do not have to length-check before indexing.
func TestMessageArgIsBoundsSafe(t *testing.T) {
	msg := &Message{RawText: ".ban 42 spam"}

	if got := msg.Arg(0); got != "42" {
		t.Errorf("Arg(0) = %q, want %q", got, "42")
	}
	if got := msg.Arg(1); got != "spam" {
		t.Errorf("Arg(1) = %q, want %q", got, "spam")
	}
	if got := msg.Arg(2); got != "" {
		t.Errorf("Arg(2) past end = %q, want %q", got, "")
	}
	if got := msg.Arg(-1); got != "" {
		t.Errorf("Arg(-1) = %q, want %q", got, "")
	}
}

func TestMessageArgsOrReplyPrefersArgs(t *testing.T) {
	msg := &Message{RawText: ".html typed"}
	if got := msg.ArgsOrReply(); got != "typed" {
		t.Errorf("ArgsOrReply() = %q, want %q", got, "typed")
	}
}

// With no args and no reply there is nothing to act on; "" is the signal.
func TestMessageArgsOrReplyEmptyWithoutReply(t *testing.T) {
	msg := &Message{RawText: ".html"}
	if got := msg.ArgsOrReply(); got != "" {
		t.Errorf("ArgsOrReply() = %q, want %q", got, "")
	}
}

func TestMessageHelpersNilSafe(t *testing.T) {
	var msg *Message
	if got := msg.Args(); got != "" {
		t.Errorf("Args() on nil = %q", got)
	}
	if got := msg.ArgsList(); got != nil {
		t.Errorf("ArgsList() on nil = %#v", got)
	}
	if got := msg.Arg(0); got != "" {
		t.Errorf("Arg(0) on nil = %q", got)
	}
}
