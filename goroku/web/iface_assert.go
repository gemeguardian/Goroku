package web

import "goroku/goroku/webiface"

// Compile-time implementation assertions for web consumer ports.
var (
	// Local TelegramClient alias must stay a subtype of the public port.
	_ webiface.TelegramClient = (TelegramClient)(nil)
	// webiface.InlineProvider satisfies the local bot-auth surface.
	_ inlineBotProvider = (webiface.InlineProvider)(nil)
)
