package goroku

import "strings"

// Args returns the command arguments as a single trimmed string, with the
// command word itself removed.
//
//	.html <b>hi</b>   ->  "<b>hi</b>"
func (m *Message) Args() string {
	if m == nil {
		return ""
	}
	text := m.RawText
	if text == "" {
		text = m.Text
	}
	return strings.TrimSpace(argsAfterCommand(text))
}

// ArgsList splits Args on whitespace. Empty input yields a nil slice, so
// len(msg.ArgsList()) is a safe way to count arguments.
func (m *Message) ArgsList() []string {
	args := m.Args()
	if args == "" {
		return nil
	}
	return strings.Fields(args)
}

// Arg returns the i-th whitespace-separated argument, or "" when absent.
// Callers do not need to bounds-check.
func (m *Message) Arg(i int) string {
	args := m.ArgsList()
	if i < 0 || i >= len(args) {
		return ""
	}
	return args[i]
}

// ArgsOrReply returns the command arguments, falling back to the text of the
// replied-to message when no arguments were given. This is the "operate on
// what I typed, or on what I replied to" pattern that most commands want.
//
// It returns "" when there are neither arguments nor a replied-to message with
// text, which callers should treat as "nothing to do".
func (m *Message) ArgsOrReply() string {
	if args := m.Args(); args != "" {
		return args
	}
	reply, err := m.GetReplyMessage()
	if err != nil || reply == nil {
		return ""
	}
	if reply.RawText != "" {
		return reply.RawText
	}
	return reply.Text
}

// argsAfterCommand drops the leading prefix+command token from raw message
// text. It is prefix-agnostic: everything up to the first space is the command.
func argsAfterCommand(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexAny(text, " \t\n"); idx >= 0 {
		return text[idx+1:]
	}
	return ""
}
