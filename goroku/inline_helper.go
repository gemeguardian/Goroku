package goroku

import "goroku/goroku/inline"

// InlineManager returns the typed inline manager assigned to the client.
// It keeps types.go free from the inline package import, removing the
// possibility of a future import cycle between goroku and inline.
func (c *CustomTelegramClient) InlineManager() *inline.InlineManager {
	if c.GorokuInline == nil {
		return nil
	}
	if im, ok := c.GorokuInline.(*inline.InlineManager); ok {
		return im
	}
	return nil
}
