package web

import "goroku/goroku/webiface"

// Compile-time implementation assertions for web consumer ports.
var (
	_ webiface.TelegramClient = (TelegramClient)(nil)
	_ inlineBotProvider       = (webiface.InlineProvider)(nil)
)
