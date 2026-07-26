package goroku

import (
	"context"
	"fmt"
	stdhtml "html"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
)

func (c *CustomTelegramClient) ResolvePeer(chat any) (tg.InputPeerClass, error) {
	return c.ResolvePeerContext(nil, chat)
}

func (c *CustomTelegramClient) ResolvePeerContext(ctx context.Context, chat any) (tg.InputPeerClass, error) {
	return c.resolvePeerInternal(c.rpcContext(ctx), chat)
}

func (c *CustomTelegramClient) ResolvePeerRef(chat ChatRef) (tg.InputPeerClass, error) {
	return c.ResolvePeerRefContext(nil, chat)
}

func (c *CustomTelegramClient) ResolvePeerRefContext(ctx context.Context, chat ChatRef) (tg.InputPeerClass, error) {
	return c.resolvePeerInternal(c.rpcContext(ctx), chat.AsLegacy())
}

func (c *CustomTelegramClient) resolvePeerInternal(ctx context.Context, chat any) (tg.InputPeerClass, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if peer, ok := chat.(tg.InputPeerClass); ok {
		return peer, nil
	}
	// ChatRef / EntityRef / UserRef / ChannelRef
	if ref, ok := chat.(interface{ AsLegacy() any }); ok {
		return c.resolvePeerInternal(ctx, ref.AsLegacy())
	}

	if c.rawAPI == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if id, ok := chat.(int64); ok {
		if id == c.TGIDValue() {
			return &tg.InputPeerSelf{}, nil
		}
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[cache.NormalizeEntityCacheKey(id)]
		c.cacheMu.RUnlock()
		if ok && record.Entity != nil {
			return record.Entity, nil
		}
	} else if username, ok := chat.(string); ok {
		usernameLower := strings.ToLower(strings.TrimPrefix(username, "@"))
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[cache.EntityCacheKey{Username: usernameLower}]
		c.cacheMu.RUnlock()
		if ok && record.Entity != nil {
			return record.Entity, nil
		}
	}

	switch v := chat.(type) {
	case int64:
		id := v
		if peer, err := c.resolvePeerFromTelegram(ctx, id); err == nil {
			return peer, nil
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		idStr := strconv.FormatInt(id, 10)
		if strings.HasPrefix(idStr, "-100") {
			return nil, fmt.Errorf("channel %d not found in entity cache", id)
		} else if id < 0 {
			return nil, fmt.Errorf("chat %d not found in entity cache", id)
		}
		return nil, fmt.Errorf("user %d not found in entity cache", id)
	case int:
		id := int64(v)
		if id == c.TGIDValue() {
			return &tg.InputPeerSelf{}, nil
		}
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[cache.NormalizeEntityCacheKey(id)]
		c.cacheMu.RUnlock()
		if ok && record.Entity != nil {
			return record.Entity, nil
		}
		if peer, err := c.resolvePeerFromTelegram(ctx, id); err == nil {
			return peer, nil
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		idStr := strconv.FormatInt(id, 10)
		if strings.HasPrefix(idStr, "-100") {
			return nil, fmt.Errorf("channel %d not found in entity cache", id)
		} else if id < 0 {
			return nil, fmt.Errorf("chat %d not found in entity cache", id)
		}
		return nil, fmt.Errorf("user %d not found in entity cache", id)
	case string:
		v = strings.TrimPrefix(v, "@")
		vLower := strings.ToLower(v)
		res, err := c.rawAPI.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: v})
		if err != nil {
			return nil, err
		}
		if len(res.Users) > 0 {
			user, ok := res.Users[0].(*tg.User)
			if !ok {
				return nil, fmt.Errorf("unexpected user type %T", res.Users[0])
			}
			var peer tg.InputPeerClass
			if user.Self {
				peer = &tg.InputPeerSelf{}
			} else {
				peer = &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
			}
			record := cache.CacheRecordEntity{Entity: peer, Exp: time.Now().Unix() + 86400*30, TS: time.Now().Unix()}
			c.cacheMu.Lock()
			c.GorokuEntityCache[cache.EntityCacheKey{ID: user.ID}] = record
			c.GorokuEntityCache[cache.EntityCacheKey{Username: vLower}] = record
			c.cacheMu.Unlock()
			return peer, nil
		}
		if len(res.Chats) > 0 {
			switch ch := res.Chats[0].(type) {
			case *tg.Chat:
				peer := &tg.InputPeerChat{ChatID: ch.ID}
				record := cache.CacheRecordEntity{Entity: peer, Exp: time.Now().Unix() + 86400*30, TS: time.Now().Unix()}
				c.cacheMu.Lock()
				c.GorokuEntityCache[cache.EntityCacheKey{ID: ch.ID}] = record
				c.GorokuEntityCache[cache.EntityCacheKey{ID: -ch.ID}] = record
				c.cacheMu.Unlock()
				return peer, nil
			case *tg.Channel:
				peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
				record := cache.CacheRecordEntity{Entity: peer, Exp: time.Now().Unix() + 86400*30, TS: time.Now().Unix()}
				c.cacheMu.Lock()
				c.GorokuEntityCache[cache.EntityCacheKey{ID: ch.ID}] = record
				c.GorokuEntityCache[cache.EntityCacheKey{ID: cache.TelegramChannelChatID(ch.ID)}] = record
				c.GorokuEntityCache[cache.EntityCacheKey{Username: vLower}] = record
				c.cacheMu.Unlock()
				return peer, nil
			}
		}
	}
	return nil, fmt.Errorf("cannot resolve peer: %v", chat)
}

func (c *CustomTelegramClient) resolvePeerFromTelegram(ctx context.Context, id int64) (tg.InputPeerClass, error) {
	idStr := strconv.FormatInt(id, 10)
	if strings.HasPrefix(idStr, "-100") {
		rawChanID, err := strconv.ParseInt(strings.TrimPrefix(idStr, "-100"), 10, 64)
		if err != nil {
			return nil, err
		}
		res, err := c.rawAPI.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: rawChanID, AccessHash: 0}})
		if err != nil {
			return nil, err
		}
		var chats []tg.ChatClass
		switch cVal := res.(type) {
		case *tg.MessagesChats:
			chats = cVal.Chats
		case *tg.MessagesChatsSlice:
			chats = cVal.Chats
		}
		if len(chats) > 0 {
			entChans := make(map[int64]*tg.Channel)
			for _, chatClass := range chats {
				if ch, ok := chatClass.(*tg.Channel); ok {
					entChans[ch.ID] = ch
				}
			}
			c.cacheEntities(tg.Entities{Channels: entChans})
			c.cacheMu.RLock()
			record, ok := c.GorokuEntityCache[cache.NormalizeEntityCacheKey(id)]
			c.cacheMu.RUnlock()
			if ok && record.Entity != nil {
				return record.Entity, nil
			}
		}
	} else if id < 0 {
		res, err := c.rawAPI.MessagesGetChats(ctx, []int64{-id})
		if err != nil {
			return nil, err
		}
		var chats []tg.ChatClass
		switch cVal := res.(type) {
		case *tg.MessagesChats:
			chats = cVal.Chats
		case *tg.MessagesChatsSlice:
			chats = cVal.Chats
		}
		if len(chats) > 0 {
			entChats := make(map[int64]*tg.Chat)
			for _, chatClass := range chats {
				if ch, ok := chatClass.(*tg.Chat); ok {
					entChats[ch.ID] = ch
				}
			}
			c.cacheEntities(tg.Entities{Chats: entChats})
			c.cacheMu.RLock()
			record, ok := c.GorokuEntityCache[cache.NormalizeEntityCacheKey(id)]
			c.cacheMu.RUnlock()
			if ok && record.Entity != nil {
				return record.Entity, nil
			}
		}
	} else {
		res, err := c.rawAPI.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: id, AccessHash: 0}})
		if err != nil {
			return nil, err
		}
		if len(res) > 0 {
			entUsers := make(map[int64]*tg.User)
			for _, uClass := range res {
				if u, ok := uClass.(*tg.User); ok {
					entUsers[u.ID] = u
				}
			}
			c.cacheEntities(tg.Entities{Users: entUsers})
			c.cacheMu.RLock()
			record, ok := c.GorokuEntityCache[cache.NormalizeEntityCacheKey(id)]
			c.cacheMu.RUnlock()
			if ok && record.Entity != nil {
				return record.Entity, nil
			}
		}
	}
	return nil, fmt.Errorf("peer %d not resolved from Telegram", id)
}

func (c *CustomTelegramClient) cacheEntities(e tg.Entities) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.GorokuEntityCache == nil {
		c.GorokuEntityCache = make(map[cache.EntityCacheKey]cache.CacheRecordEntity)
	}
	// Called for every update: this is where the cache grew one to three
	// entries per newly seen participant, with nothing ever removing them.
	c.sweepEntityCacheLocked(time.Now().Unix())
	exp := time.Now().Unix() + 86400*30 // 30 days cache expiration

	for _, user := range e.Users {
		var peer tg.InputPeerClass
		if user.Self {
			peer = &tg.InputPeerSelf{}
		} else {
			peer = &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
		}
		c.GorokuEntityCache[cache.EntityCacheKey{ID: user.ID}] = cache.CacheRecordEntity{
			Entity: peer,
			Exp:    exp,
			TS:     time.Now().Unix(),
		}
		if user.Username != "" {
			c.GorokuEntityCache[cache.EntityCacheKey{Username: strings.ToLower(user.Username)}] = cache.CacheRecordEntity{
				Entity: peer,
				Exp:    exp,
				TS:     time.Now().Unix(),
			}
		}
	}

	for _, chat := range e.Chats {
		peer := &tg.InputPeerChat{ChatID: chat.ID}
		record := cache.CacheRecordEntity{
			Entity: peer,
			Exp:    exp,
			TS:     time.Now().Unix(),
		}
		c.GorokuEntityCache[cache.EntityCacheKey{ID: chat.ID}] = record
		c.GorokuEntityCache[cache.EntityCacheKey{ID: -chat.ID}] = record
	}

	for _, channel := range e.Channels {
		peer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
		record := cache.CacheRecordEntity{
			Entity: peer,
			Exp:    exp,
			TS:     time.Now().Unix(),
		}
		c.GorokuEntityCache[cache.EntityCacheKey{ID: channel.ID}] = record
		c.GorokuEntityCache[cache.EntityCacheKey{ID: cache.TelegramChannelChatID(channel.ID)}] = record
		if channel.Username != "" {
			c.GorokuEntityCache[cache.EntityCacheKey{Username: strings.ToLower(channel.Username)}] = record
		}
	}

	// A single update can carry hundreds of entities, so the cap has to be
	// re-applied after the batch, not only before it.
	c.sweepEntityCacheLocked(time.Now().Unix())
}

func (c *CustomTelegramClient) buildMessageFromTG(msg *tg.Message) *Message {
	hMsg := &Message{
		ID:       int64(msg.ID),
		ChatID:   0,
		SenderID: 0,
		Text:     entitiesToHTML(msg.Message, msg.Entities),
		RawText:  msg.Message,
		Entities: msg.Entities,
		Out:      msg.Out,
		Client:   c,
		ViaBotID: msg.ViaBotID,
	}
	if msg.ReplyTo != nil {
		if header, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
			hMsg.ReplyToMsgID = int64(header.ReplyToMsgID)
		}
	}
	if msg.Media != nil {
		hMsg.Media = msg.Media
	}
	if fwd, ok := msg.GetFwdFrom(); ok {
		hMsg.IsForwarded = true
		hMsg.FwdFrom = fwd
	}

	switch peer := msg.PeerID.(type) {
	case *tg.PeerUser:
		hMsg.ChatID = peer.UserID
		hMsg.IsPrivate = true
	case *tg.PeerChat:
		hMsg.ChatID = -peer.ChatID
		hMsg.IsGroup = true
	case *tg.PeerChannel:
		hMsg.ChatID = cache.TelegramChannelChatID(peer.ChannelID)
		hMsg.IsChannel = true
	}

	if msg.FromID != nil {
		switch peer := msg.FromID.(type) {
		case *tg.PeerUser:
			hMsg.SenderID = peer.UserID
		case *tg.PeerChannel:
			// Anonymous admins and channel-signed posts: the sender is a
			// channel, not a user. SenderID stayed 0, so such a sender could
			// not be blacklisted or given a tsec rule at all. Record the
			// channel separately instead of pretending there is no sender.
			hMsg.SenderChannelID = cache.TelegramChannelChatID(peer.ChannelID)
			hMsg.SenderIsChannel = true
		case *tg.PeerChat:
			hMsg.SenderChannelID = -peer.ChatID
			hMsg.SenderIsChannel = true
		}
	} else if msg.Out || (hMsg.IsPrivate && hMsg.ChatID == c.TGIDValue()) {
		hMsg.SenderID = c.TGIDValue()
	} else if hMsg.IsPrivate {
		hMsg.SenderID = hMsg.ChatID
	}
	if c.TGIDValue() != 0 && hMsg.SenderID == c.TGIDValue() {
		hMsg.Out = true
	}

	return hMsg
}

type htmlTagEvent struct {
	offset  int
	isClose bool
	tagType string
	tagArg  string
	length  int
	order   int
}

func entitiesToHTML(text string, entities []tg.MessageEntityClass) string {
	if len(entities) == 0 {
		return stdhtml.EscapeString(text)
	}

	u16 := utf16.Encode([]rune(text))
	var events []htmlTagEvent

	for idx, entity := range entities {
		var offset, length int
		var tagType, tagArg string
		valid := false

		switch e := entity.(type) {
		case *tg.MessageEntityBold:
			offset, length, tagType = e.Offset, e.Length, "b"
			valid = true
		case *tg.MessageEntityItalic:
			offset, length, tagType = e.Offset, e.Length, "i"
			valid = true
		case *tg.MessageEntityUnderline:
			offset, length, tagType = e.Offset, e.Length, "u"
			valid = true
		case *tg.MessageEntityStrike:
			offset, length, tagType = e.Offset, e.Length, "s"
			valid = true
		case *tg.MessageEntityCode:
			offset, length, tagType = e.Offset, e.Length, "code"
			valid = true
		case *tg.MessageEntityPre:
			offset, length, tagType = e.Offset, e.Length, "pre"
			valid = true
		case *tg.MessageEntitySpoiler:
			offset, length, tagType = e.Offset, e.Length, "tg-spoiler"
			valid = true
		case *tg.MessageEntityBlockquote:
			offset, length, tagType = e.Offset, e.Length, "blockquote"
			if e.Collapsed {
				tagArg = " expandable"
			}
			valid = true
		case *tg.MessageEntityTextURL:
			offset, length, tagType, tagArg = e.Offset, e.Length, "a", fmt.Sprintf(" href=%q", stdhtml.EscapeString(e.URL))
			valid = true
		case *tg.MessageEntityMentionName:
			offset, length, tagType, tagArg = e.Offset, e.Length, "a", fmt.Sprintf(" href=\"tg://user?id=%d\"", e.UserID)
			valid = true
		case *tg.MessageEntityCustomEmoji:
			offset, length, tagType, tagArg = e.Offset, e.Length, "tg-emoji", fmt.Sprintf(" emoji-id=\"%d\"", e.DocumentID)
			valid = true
		}

		if valid && offset >= 0 && offset <= len(u16) && offset+length <= len(u16) {
			events = append(events, htmlTagEvent{
				offset:  offset,
				isClose: false,
				tagType: tagType,
				tagArg:  tagArg,
				length:  length,
				order:   idx,
			})
			events = append(events, htmlTagEvent{
				offset:  offset + length,
				isClose: true,
				tagType: tagType,
				order:   idx,
			})
		}
	}

	sort.Slice(events, func(i, j int) bool {
		ei, ej := events[i], events[j]
		if ei.offset != ej.offset {
			return ei.offset < ej.offset
		}
		if ei.isClose != ej.isClose {
			return ei.isClose
		}
		if ei.isClose {
			return ei.order > ej.order
		}
		if ei.length != ej.length {
			return ei.length > ej.length
		}
		return ei.order < ej.order
	})

	var result strings.Builder
	lastOffset := 0

	for _, ev := range events {
		if ev.offset > lastOffset {
			chunk := string(utf16.Decode(u16[lastOffset:ev.offset]))
			result.WriteString(stdhtml.EscapeString(chunk))
			lastOffset = ev.offset
		}

		if ev.isClose {
			result.WriteString("</" + ev.tagType + ">")
		} else {
			result.WriteString("<" + ev.tagType + ev.tagArg + ">")
		}
	}

	if lastOffset < len(u16) {
		chunk := string(utf16.Decode(u16[lastOffset:]))
		result.WriteString(stdhtml.EscapeString(chunk))
	}

	return result.String()
}

func (c *CustomTelegramClient) EntitiesToHTML(text string, entities []tg.MessageEntityClass) string {
	return entitiesToHTML(text, entities)
}

func EntitiesToHTML(text string, entities []tg.MessageEntityClass) string {
	return entitiesToHTML(text, entities)
}

func TelegramChannelChatID(channelID int64) int64 {
	return cache.TelegramChannelChatID(channelID)
}
