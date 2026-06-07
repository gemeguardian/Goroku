package goroku

import (
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"goroku/goroku/inline"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

type CacheRecordEntity struct {
	Entity interface{}
	Exp    int64
	TS     int64
}

func TelegramChannelChatID(channelID int64) int64 {
	return -1000000000000 - channelID
}

func normalizeEntityCacheKey(entity interface{}) interface{} {
	switch v := entity.(type) {
	case string:
		s := strings.ToLower(strings.TrimPrefix(v, "@"))
		if strings.HasPrefix(s, "-100") {
			if id, err := strconv.ParseInt(strings.TrimPrefix(s, "-100"), 10, 64); err == nil {
				return id
			}
		}
		return s
	case int:
		return normalizeEntityCacheKey(int64(v))
	case int64:
		if v < -1000000000000 {
			return -(v + 1000000000000)
		}
		if v < 0 {
			return -v
		}
		return v
	case tg.InputPeerClass:
		switch p := v.(type) {
		case *tg.InputPeerUser:
			return p.UserID
		case *tg.InputPeerChannel:
			return p.ChannelID
		case *tg.InputPeerChat:
			return p.ChatID
		}
	}
	return entity
}

func cachePeerAliases(cache map[interface{}]CacheRecordEntity, peer tg.InputPeerClass, record CacheRecordEntity) {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		cache[p.UserID] = record
	case *tg.InputPeerChannel:
		cache[p.ChannelID] = record
		cache[TelegramChannelChatID(p.ChannelID)] = record
	case *tg.InputPeerChat:
		cache[p.ChatID] = record
		cache[-p.ChatID] = record
	}
}

func inputPeerUserID(peer tg.InputPeerClass) int64 {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return p.UserID
	case *tg.InputPeerSelf:
		return 0
	}
	return 0
}

func chatParticipantUserID(participant tg.ChatParticipantClass) int64 {
	switch p := participant.(type) {
	case *tg.ChatParticipant:
		return p.UserID
	case *tg.ChatParticipantAdmin:
		return p.UserID
	case *tg.ChatParticipantCreator:
		return p.UserID
	}
	return 0
}

func inputUserFromPeer(peer tg.InputPeerClass) (tg.InputUserClass, error) {
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		return &tg.InputUserSelf{}, nil
	case *tg.InputPeerUser:
		return &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}, nil
	default:
		return nil, fmt.Errorf("peer %T is not a user", peer)
	}
}

func (r CacheRecordEntity) Expired() bool {
	return r.Exp < time.Now().Unix()
}

type CacheRecordPerms struct {
	Perms interface{}
	Exp   int64
	TS    int64
}

func (r CacheRecordPerms) Expired() bool {
	return r.Exp < time.Now().Unix()
}

type CacheRecordFullChannel struct {
	ChannelID   interface{}
	FullChannel interface{}
	Exp         int64
	TS          int64
}

func (r CacheRecordFullChannel) Expired() bool {
	return r.Exp < time.Now().Unix()
}

type CacheRecordFullUser struct {
	UserID   interface{}
	FullUser interface{}
	Exp      int64
	TS       int64
}

func (r CacheRecordFullUser) Expired() bool {
	return r.Exp < time.Now().Unix()
}

func (c *CustomTelegramClient) GetEntity(entity interface{}, exp int64, force bool) (interface{}, error) {
	cacheKey := normalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && (exp == 0 || !record.Expired()) {
			return record.Entity, nil
		}
	}

	// Resolve actual peer info if possible
	peer, err := c.ResolvePeer(entity)
	if err == nil {
		record := CacheRecordEntity{
			Entity: peer,
			Exp:    time.Now().Unix() + exp,
			TS:     time.Now().Unix(),
		}
		c.cacheMu.Lock()
		c.GorokuEntityCache[cacheKey] = record
		cachePeerAliases(c.GorokuEntityCache, peer, record)
		c.cacheMu.Unlock()
		return peer, nil
	}

	return nil, err
}

func (c *CustomTelegramClient) GetPermsCached(entity interface{}, user interface{}, exp int64, force bool) (interface{}, error) {
	entityKey := normalizeEntityCacheKey(entity)
	userKey := normalizeEntityCacheKey(user)
	if !force {
		c.cacheMu.RLock()
		var record CacheRecordPerms
		var ok bool
		if subMap, exists := c.GorokuPermsCache[entityKey]; exists {
			record, ok = subMap[userKey]
		}
		c.cacheMu.RUnlock()
		if ok && (exp == 0 || !record.Expired()) {
			return record.Perms, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	if user == nil {
		user = c.TGID
		userKey = c.TGID
	}
	userPeer, err := c.ResolvePeer(user)
	if err != nil {
		return nil, err
	}

	perms, err := c.fetchPermissions(peer, userPeer)
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	if _, ok := c.GorokuPermsCache[entityKey]; !ok {
		c.GorokuPermsCache[entityKey] = make(map[interface{}]CacheRecordPerms)
	}

	c.GorokuPermsCache[entityKey][userKey] = CacheRecordPerms{
		Perms: perms,
		Exp:   time.Now().Unix() + exp,
		TS:    time.Now().Unix(),
	}
	c.cacheMu.Unlock()

	return perms, nil
}

func (c *CustomTelegramClient) fetchPermissions(peer tg.InputPeerClass, userPeer tg.InputPeerClass) (interface{}, error) {
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		res, err := c.rawAPI.ChannelsGetParticipant(c.ctx, &tg.ChannelsGetParticipantRequest{
			Channel:     &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
			Participant: userPeer,
		})
		if err != nil {
			return nil, err
		}
		return res.Participant, nil
	case *tg.InputPeerChat:
		res, err := c.rawAPI.MessagesGetFullChat(c.ctx, p.ChatID)
		if err != nil {
			return nil, err
		}
		full, ok := res.FullChat.(*tg.ChatFull)
		if !ok {
			return nil, fmt.Errorf("unexpected full chat type %T", res.FullChat)
		}
		participants, ok := full.Participants.AsNotForbidden()
		if !ok {
			return nil, fmt.Errorf("chat participants are forbidden")
		}
		userID := inputPeerUserID(userPeer)
		if userID == 0 {
			userID = c.TGID
		}
		for _, participant := range participants.Participants {
			if chatParticipantUserID(participant) == userID {
				return participant, nil
			}
		}
		return nil, fmt.Errorf("participant %d not found", userID)
	case *tg.InputPeerUser, *tg.InputPeerSelf:
		return map[string]interface{}{"is_private": true}, nil
	default:
		return nil, fmt.Errorf("unsupported peer type %T", peer)
	}
}

func (c *CustomTelegramClient) GetFullChannel(entity interface{}, exp int64, force bool) (interface{}, error) {
	cacheKey := normalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuFullChannelCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && !record.Expired() {
			return record.FullChannel, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	channelPeer, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, fmt.Errorf("entity %v is not a channel", entity)
	}

	fullChannel, err := c.rawAPI.ChannelsGetFullChannel(c.ctx, &tg.InputChannel{ChannelID: channelPeer.ChannelID, AccessHash: channelPeer.AccessHash})
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	c.GorokuFullChannelCache[cacheKey] = CacheRecordFullChannel{
		ChannelID:   cacheKey,
		FullChannel: fullChannel,
		Exp:         time.Now().Unix() + exp,
		TS:          time.Now().Unix(),
	}
	c.cacheMu.Unlock()

	return fullChannel, nil
}

func (c *CustomTelegramClient) GetFullUser(entity interface{}, exp int64, force bool) (interface{}, error) {
	cacheKey := normalizeEntityCacheKey(entity)
	if !force {
		c.cacheMu.RLock()
		record, ok := c.GorokuFullUserCache[cacheKey]
		c.cacheMu.RUnlock()
		if ok && !record.Expired() {
			return record.FullUser, nil
		}
	}

	peer, err := c.ResolvePeer(entity)
	if err != nil {
		return nil, err
	}
	inputUser, err := inputUserFromPeer(peer)
	if err != nil {
		return nil, err
	}

	fullUser, err := c.rawAPI.UsersGetFullUser(c.ctx, inputUser)
	if err != nil {
		return nil, err
	}

	c.cacheMu.Lock()
	c.GorokuFullUserCache[cacheKey] = CacheRecordFullUser{
		UserID:   cacheKey,
		FullUser: fullUser,
		Exp:      time.Now().Unix() + exp,
		TS:       time.Now().Unix(),
	}
	c.cacheMu.Unlock()

	return fullUser, nil
}

func (c *CustomTelegramClient) ResolvePeer(chat interface{}) (tg.InputPeerClass, error) {
	if c.rawAPI == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if id, ok := chat.(int64); ok {
		if id == c.TGID {
			return &tg.InputPeerSelf{}, nil
		}
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[normalizeEntityCacheKey(id)]
		c.cacheMu.RUnlock()
		if ok {
			if peer, ok := record.Entity.(tg.InputPeerClass); ok {
				return peer, nil
			}
		}
	} else if username, ok := chat.(string); ok {
		usernameLower := strings.ToLower(strings.TrimPrefix(username, "@"))
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[usernameLower]
		c.cacheMu.RUnlock()
		if ok {
			if peer, ok := record.Entity.(tg.InputPeerClass); ok {
				return peer, nil
			}
		}
	}

	switch v := chat.(type) {
	case tg.InputPeerClass:
		return v, nil
	case int64:
		id := v
		idStr := strconv.FormatInt(id, 10)
		if strings.HasPrefix(idStr, "-100") {
			return nil, fmt.Errorf("channel %d not found in entity cache", id)
		} else if id < 0 {
			return nil, fmt.Errorf("chat %d not found in entity cache", id)
		}
		return nil, fmt.Errorf("user %d not found in entity cache", id)
	case int:
		id := int64(v)
		if id == c.TGID {
			return &tg.InputPeerSelf{}, nil
		}
		c.cacheMu.RLock()
		record, ok := c.GorokuEntityCache[normalizeEntityCacheKey(id)]
		c.cacheMu.RUnlock()
		if ok {
			if peer, ok := record.Entity.(tg.InputPeerClass); ok {
				return peer, nil
			}
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
		res, err := c.rawAPI.ContactsResolveUsername(c.ctx, &tg.ContactsResolveUsernameRequest{Username: v})
		if err != nil {
			return nil, err
		}
		if len(res.Users) > 0 {
			user := res.Users[0].(*tg.User)
			var peer tg.InputPeerClass
			if user.Self {
				peer = &tg.InputPeerSelf{}
			} else {
				peer = &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
			}
			record := CacheRecordEntity{Entity: peer, Exp: time.Now().Unix() + 86400*30, TS: time.Now().Unix()}
			c.cacheMu.Lock()
			c.GorokuEntityCache[user.ID] = record
			c.GorokuEntityCache[vLower] = record
			c.cacheMu.Unlock()
			return peer, nil
		}
		if len(res.Chats) > 0 {
			switch ch := res.Chats[0].(type) {
			case *tg.Chat:
				peer := &tg.InputPeerChat{ChatID: ch.ID}
				record := CacheRecordEntity{Entity: peer, Exp: time.Now().Unix() + 86400*30, TS: time.Now().Unix()}
				c.cacheMu.Lock()
				c.GorokuEntityCache[ch.ID] = record
				c.GorokuEntityCache[-ch.ID] = record
				c.cacheMu.Unlock()
				return peer, nil
			case *tg.Channel:
				peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
				record := CacheRecordEntity{Entity: peer, Exp: time.Now().Unix() + 86400*30, TS: time.Now().Unix()}
				c.cacheMu.Lock()
				c.GorokuEntityCache[ch.ID] = record
				c.GorokuEntityCache[TelegramChannelChatID(ch.ID)] = record
				c.GorokuEntityCache[vLower] = record
				c.cacheMu.Unlock()
				return peer, nil
			}
		}
	}
	return nil, fmt.Errorf("cannot resolve peer: %v", chat)
}

func (c *CustomTelegramClient) cacheEntities(e tg.Entities) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.GorokuEntityCache == nil {
		c.GorokuEntityCache = make(map[interface{}]CacheRecordEntity)
	}
	exp := time.Now().Unix() + 86400*30 // 30 days cache expiration

	for _, user := range e.Users {
		var peer tg.InputPeerClass
		if user.Self {
			peer = &tg.InputPeerSelf{}
		} else {
			peer = &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
		}
		c.GorokuEntityCache[user.ID] = CacheRecordEntity{
			Entity: peer,
			Exp:    exp,
			TS:     time.Now().Unix(),
		}
		if user.Username != "" {
			c.GorokuEntityCache[strings.ToLower(user.Username)] = CacheRecordEntity{
				Entity: peer,
				Exp:    exp,
				TS:     time.Now().Unix(),
			}
		}
	}

	for _, chat := range e.Chats {
		peer := &tg.InputPeerChat{ChatID: chat.ID}
		record := CacheRecordEntity{
			Entity: peer,
			Exp:    exp,
			TS:     time.Now().Unix(),
		}
		c.GorokuEntityCache[chat.ID] = record
		c.GorokuEntityCache[-chat.ID] = record
	}

	for _, channel := range e.Channels {
		peer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
		record := CacheRecordEntity{
			Entity: peer,
			Exp:    exp,
			TS:     time.Now().Unix(),
		}
		c.GorokuEntityCache[channel.ID] = record
		c.GorokuEntityCache[TelegramChannelChatID(channel.ID)] = record
		if channel.Username != "" {
			c.GorokuEntityCache[strings.ToLower(channel.Username)] = record
		}
	}
}

func (c *CustomTelegramClient) buildMessageFromTG(msg *tg.Message) *Message {
	hMsg := &Message{
		ID:       int64(msg.ID),
		ChatID:   0,
		SenderID: 0,
		Text:     msg.Message,
		RawText:  msg.Message,
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
		hMsg.ChatID = TelegramChannelChatID(peer.ChannelID)
		hMsg.IsChannel = true
	}

	if msg.FromID != nil {
		switch peer := msg.FromID.(type) {
		case *tg.PeerUser:
			hMsg.SenderID = peer.UserID
		}
	} else if msg.Out || (hMsg.IsPrivate && hMsg.ChatID == c.TGID) {
		hMsg.SenderID = c.TGID
	}
	if hMsg.IsPrivate && hMsg.ChatID == c.TGID && hMsg.SenderID == c.TGID {
		hMsg.Out = true
	}

	return hMsg
}

type forbiddenInvoker struct {
	parent tg.Invoker
	client *CustomTelegramClient
}

func (f *forbiddenInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if input != nil {
		if t, ok := input.(interface{ TypeID() uint32 }); ok {
			typeID := int(t.TypeID())
			for _, forbidden := range f.client.ForbiddenConstructors {
				if typeID == forbidden {
					log.Printf("🎉 [API Protection] Blocked forbidden constructor call: %d\n", typeID)
					return fmt.Errorf("constructor %d is forbidden", typeID)
				}
			}

			// Rate limiting check
			db, okDB := f.client.GorokuDB.(*Database)
			if okDB && db != nil {
				disableProtectionVal := db.Get("APILimiter", "disable_protection", true)
				disableProtection, _ := disableProtectionVal.(bool)
				if !disableProtection {
					f.client.RatelimitMu.Lock()
					bypassed := time.Now().Before(f.client.BypassSuspendUntil)
					f.client.RatelimitMu.Unlock()

					if !bypassed {
						// If currently suspended, wait
						f.client.RatelimitMu.Lock()
						for time.Now().Before(f.client.SuspendUntil) {
							dur := time.Until(f.client.SuspendUntil)
							f.client.RatelimitMu.Unlock()
							time.Sleep(dur)
							f.client.RatelimitMu.Lock()
						}
						f.client.RatelimitMu.Unlock()

						typeName := fmt.Sprintf("%T", input)
						isTargetRequest := strings.HasPrefix(typeName, "*tg.Messages") ||
							strings.HasPrefix(typeName, "*tg.Channels") ||
							strings.HasPrefix(typeName, "*tg.Account")

						if isTargetRequest {
							f.client.RatelimitMu.Lock()
							now := time.Now()
							f.client.Ratelimiter = append(f.client.Ratelimiter, RateLimitRecord{Name: typeName, TS: now})

							// Filter records within time sample
							timeSampleSec := 15
							if sampleVal := db.Get("APILimiter", "time_sample", 15); sampleVal != nil {
								if sampleInt, ok := sampleVal.(int); ok {
									timeSampleSec = sampleInt
								} else if sampleFloat, ok := sampleVal.(float64); ok {
									timeSampleSec = int(sampleFloat)
								}
							}

							cutoff := now.Add(-time.Duration(timeSampleSec) * time.Second)
							var filtered []RateLimitRecord
							for _, r := range f.client.Ratelimiter {
								if r.TS.After(cutoff) {
									filtered = append(filtered, r)
								}
							}
							f.client.Ratelimiter = filtered

							threshold := 100
							if threshVal := db.Get("APILimiter", "threshold", 100); threshVal != nil {
								if threshInt, ok := threshVal.(int); ok {
									threshold = threshInt
								} else if threshFloat, ok := threshVal.(float64); ok {
									threshold = int(threshFloat)
								}
							}

							localFloodWait := 30
							if lfwVal := db.Get("APILimiter", "local_floodwait", 30); lfwVal != nil {
								if lfwInt, ok := lfwVal.(int); ok {
									localFloodWait = lfwInt
								} else if lfwFloat, ok := lfwVal.(float64); ok {
									localFloodWait = int(lfwFloat)
								}
							}

							if len(f.client.Ratelimiter) > threshold && !f.client.FloodWaitLock {
								f.client.FloodWaitLock = true
								f.client.SuspendUntil = now.Add(time.Duration(localFloodWait) * time.Second)
								f.client.RatelimitMu.Unlock()

								// Dump report and send
								reportBytes, _ := json.MarshalIndent(f.client.Ratelimiter, "", "  ")
								caption := fmt.Sprintf("⚠️ <b>Goroku local floodwait triggered!</b>\n"+
									"Suspended all target calls for %d seconds to prevent API ban.", localFloodWait)

								go func(data []byte, capText string) {
									_, _ = f.client.SendFile(f.client.TGID, data, capText)
								}(reportBytes, caption)

								// Sleep
								time.Sleep(time.Duration(localFloodWait) * time.Second)

								f.client.RatelimitMu.Lock()
								f.client.FloodWaitLock = false
								f.client.Ratelimiter = nil
								f.client.RatelimitMu.Unlock()
							} else {
								f.client.RatelimitMu.Unlock()
							}
						}
					}
				}
			}
		}
	}
	return f.parent.Invoke(ctx, input, output)
}

func (c *CustomTelegramClient) Connect() error {
	if c.APIID == 0 || c.APIHash == "" {
		return fmt.Errorf("telegram api_id/api_hash is not configured")
	}

	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.readyCh = make(chan struct{})
	connectErrCh := make(chan error, 1)

	sessionPath := c.SessionPath
	if sessionPath == "" {
		sessionPath = filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", c.TGID))
	}
	storage := &session.FileStorage{Path: sessionPath}

	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		c.cacheEntities(e)
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}
		log.Printf("[Telegram] New message received: ID=%d, Text=%q, Out=%t, PeerID=%v, FromID=%v\n", msg.ID, msg.Message, msg.Out, msg.PeerID, msg.FromID)

		hMsg := c.buildMessageFromTG(msg)
		if c.Loader != nil {
			if modules, ok := c.Loader.(*Modules); ok {
				disp := modules.GetDispatcher()
				if disp != nil {
					disp.HandleCommand(hMsg)
					disp.HandleIncoming(hMsg)
				}
			}
		}
		return nil
	})

	editHandler := func(ctx context.Context, e tg.Entities, msg tg.MessageClass) error {
		c.cacheEntities(e)
		m, ok := msg.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(m)
		if c.Loader != nil {
			if modules, ok := c.Loader.(*Modules); ok {
				disp := modules.GetDispatcher()
				if disp != nil {
					disp.HandleIncoming(hMsg)
				}
			}
		}
		return nil
	}

	dispatcher.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		return editHandler(ctx, e, u.Message)
	})

	dispatcher.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return editHandler(ctx, e, u.Message)
	})

	dispatcher.OnBotInlineQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotInlineQuery) error {
		c.cacheEntities(e)
		if c.Loader != nil {
			if modules, ok := c.Loader.(*Modules); ok {
				disp := modules.GetDispatcher()
				if disp != nil {
					disp.HandleInlineQuery(u)
				}
			}
		}
		return nil
	})

	dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotCallbackQuery) error {
		c.cacheEntities(e)
		if c.Loader != nil {
			if modules, ok := c.Loader.(*Modules); ok {
				disp := modules.GetDispatcher()
				if disp != nil {
					disp.HandleCallbackQuery(u)
				}
			}
		}
		return nil
	})

	sysVer := os.Getenv("SYSTEM_VERSION")
	if sysVer == "" {
		sysVer = generateRandomSystemVersion()
	}
	client := telegram.NewClient(int(c.APIID), c.APIHash, telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  dispatcher,
		Device: telegram.DeviceConfig{
			SystemVersion: sysVer,
		},
	})

	c.client = client
	c.rawAPI = tg.NewClient(&forbiddenInvoker{parent: client, client: c})

	go func() {
		err := client.Run(c.ctx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				select {
				case connectErrCh <- err:
				default:
				}
				select {
				case <-c.readyCh:
				default:
					close(c.readyCh)
				}
				return err
			}

			if status.Authorized {
				me, err := client.Self(ctx)
				if err == nil {
					c.TGID = me.ID
					c.Username = me.Username
					c.GorokuMe = me
				}
				_ = c.CacheDialogs()
			}

			select {
			case <-c.readyCh:
			default:
				close(c.readyCh)
			}
			<-ctx.Done()
			return nil
		})
		if err != nil {
			log.Printf("gotd client run error: %v\n", err)
			select {
			case connectErrCh <- err:
			default:
			}
			select {
			case <-c.readyCh:
			default:
				close(c.readyCh)
			}
		}
	}()

	select {
	case <-c.readyCh:
		select {
		case err := <-connectErrCh:
			return err
		default:
		}
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("connection timeout")
	}
}

func (c *CustomTelegramClient) CacheDialogs() error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	res, err := c.rawAPI.MessagesGetDialogs(c.ctx, &tg.MessagesGetDialogsRequest{
		Limit:      100,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return err
	}

	var chats []tg.ChatClass
	var users []tg.UserClass
	switch dlg := res.(type) {
	case *tg.MessagesDialogsSlice:
		chats = dlg.Chats
		users = dlg.Users
	case *tg.MessagesDialogs:
		chats = dlg.Chats
		users = dlg.Users
	}

	entities := tg.Entities{
		Users:    make(map[int64]*tg.User),
		Chats:    make(map[int64]*tg.Chat),
		Channels: make(map[int64]*tg.Channel),
	}

	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			entities.Users[user.ID] = user
		}
	}

	for _, ch := range chats {
		if chat, ok := ch.(*tg.Chat); ok {
			entities.Chats[chat.ID] = chat
		} else if channel, ok := ch.(*tg.Channel); ok {
			entities.Channels[channel.ID] = channel
		}
	}

	c.cacheEntities(entities)
	return nil
}

func (c *CustomTelegramClient) ResolveUsername(username string) (bool, error) {
	_, err := c.ResolvePeer(username)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (c *CustomTelegramClient) CheckBot(username string) (bool, error) {
	if im, ok := c.GorokuInline.(interface {
		CheckBot(username string) (bool, error)
	}); ok && im != nil {
		return im.CheckBot(username)
	}
	return false, fmt.Errorf("inline manager not available or does not support CheckBot")
}

func (c *CustomTelegramClient) SendFile(chat interface{}, file interface{}, caption string) (interface{}, error) {
	return c.SendFileWithOptions(chat, file, caption)
}

func (c *CustomTelegramClient) SendFileWithOptions(chat interface{}, file interface{}, caption string, opts ...MsgOption) (interface{}, error) {
	peer, err := c.ResolvePeer(chat)
	if err != nil {
		if id, ok := chat.(int64); ok {
			peer = &tg.InputPeerUser{UserID: id}
		} else {
			return nil, err
		}
	}

	up := uploader.NewUploader(c.rawAPI)
	var inputFile tg.InputFileClass
	var filename string
	var mimeType string = "application/octet-stream"

	switch v := file.(type) {
	case string:
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			resp, err := http.Get(v)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("failed to download file: %d", resp.StatusCode)
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			filename = filepath.Base(v)
			if idx := strings.Index(filename, "?"); idx != -1 {
				filename = filename[:idx]
			}
			if filename == "" {
				filename = "file.bin"
			}
			inputFile, err = up.FromBytes(c.ctx, filename, data)
			if err != nil {
				return nil, err
			}
		} else {
			filename = filepath.Base(v)
			inputFile, err = up.FromPath(c.ctx, v)
			if err != nil {
				return nil, err
			}
		}
	case []byte:
		filename = "file.bin"
		inputFile, err = up.FromBytes(c.ctx, filename, v)
		if err != nil {
			return nil, err
		}
	case io.Reader:
		filename = "file.bin"
		inputFile, err = up.FromReader(c.ctx, filename, v)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported file type: %T", file)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".mp4":
		mimeType = "video/mp4"
	}

	var media tg.InputMediaClass
	if mimeType == "image/jpeg" || mimeType == "image/png" {
		photoMedia := &tg.InputMediaUploadedPhoto{
			File: inputFile,
		}
		photoMedia.SetFlags()
		media = photoMedia
	} else {
		media = &tg.InputMediaUploadedDocument{
			File:     inputFile,
			MimeType: mimeType,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: filename},
			},
		}
	}

	plainCaption, captionEntities := parseHTML(caption)
	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    media,
		Message:  plainCaption,
		Entities: captionEntities,
		RandomID: rand.Int63(),
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := c.rawAPI.MessagesSendMedia(c.ctx, req)
	return res, err
}

func (c *CustomTelegramClient) SendMessage(chat interface{}, message string) (interface{}, error) {
	return c.SendMessageWithOptions(chat, message)
}

func (c *CustomTelegramClient) SendMessageWithOptions(chat interface{}, message string, opts ...MsgOption) (interface{}, error) {
	peer, err := c.ResolvePeer(chat)
	if err != nil {
		if id, ok := chat.(int64); ok {
			peer = &tg.InputPeerUser{UserID: id}
		} else {
			return nil, err
		}
	}

	plainText, entities := parseHTML(message)
	req := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  plainText,
		Entities: entities,
		RandomID: rand.Int63(),
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := c.rawAPI.MessagesSendMessage(c.ctx, req)
	return res, err
}

func (c *CustomTelegramClient) EditMessage(chat interface{}, msgID int64, text string, opts ...MsgOption) (interface{}, error) {
	peer, err := c.ResolvePeer(chat)
	if err != nil {
		if id, ok := chat.(int64); ok {
			peer = &tg.InputPeerUser{UserID: id}
		} else {
			return nil, err
		}
	}

	plainText, entities := parseHTML(text)
	req := &tg.MessagesEditMessageRequest{
		Peer:     peer,
		ID:       int(msgID),
		Message:  plainText,
		Entities: entities,
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := c.rawAPI.MessagesEditMessage(c.ctx, req)
	return res, err
}

const telegramMessageLimit = 4096

type answerMode int

const (
	answerModeDirect answerMode = iota
	answerModeInlineList
	answerModeFile
)

type answerPlan struct {
	mode     answerMode
	pages    []string
	fileText string
}

func telegramTextLen(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func splitPlainTextForTelegram(text string, limit int) []string {
	if telegramTextLen(text) <= limit {
		return []string{text}
	}

	var chunks []string
	remaining := text
	for remaining != "" {
		if telegramTextLen(remaining) <= limit {
			chunks = append(chunks, remaining)
			break
		}

		cut := 0
		units := 0
		for idx, r := range remaining {
			rUnits := telegramTextLen(string(r))
			if units+rUnits > limit {
				break
			}
			units += rUnits
			cut = idx + len(string(r))
		}
		if cut <= 0 {
			cut = len([]rune(remaining[:1]))
		}

		splitAt := cut
		for _, sep := range []string{"\n", " "} {
			if idx := strings.LastIndex(remaining[:cut], sep); idx > 0 {
				splitAt = idx
				break
			}
		}

		chunk := strings.TrimRight(remaining[:splitAt], "\n ")
		if chunk == "" {
			chunk = remaining[:cut]
			splitAt = cut
		}
		chunks = append(chunks, chunk)
		remaining = strings.TrimLeft(remaining[splitAt:], "\n ")
	}
	return chunks
}

func planLongAnswer(rawText string, canUseInline bool) answerPlan {
	plainText, _ := parseHTML(rawText)
	if telegramTextLen(plainText) < telegramMessageLimit {
		return answerPlan{mode: answerModeDirect}
	}

	plainPages := splitPlainTextForTelegram(plainText, telegramMessageLimit)
	if canUseInline && len(plainPages) <= 10 {
		pages := make([]string, len(plainPages))
		for i, page := range plainPages {
			pages[i] = stdhtml.EscapeString(page)
		}
		return answerPlan{mode: answerModeInlineList, pages: pages}
	}

	return answerPlan{mode: answerModeFile, fileText: plainText}
}

func (m *Message) Answer(text string, opts ...MsgOption) error {
	m.Answered = true
	if m.GrepQuery != "" {
		lines := strings.Split(text, "\n")
		var matchingLines []string
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(m.GrepQuery))

		for _, line := range lines {
			matched := false
			if err == nil {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(m.GrepQuery))
			}

			if m.GrepInvert {
				if !matched {
					matchingLines = append(matchingLines, line)
				}
			} else {
				if matched {
					if err == nil {
						line = re.ReplaceAllString(line, "<u>$0</u>")
					} else {
						line = strings.ReplaceAll(line, m.GrepQuery, "<u>"+m.GrepQuery+"</u>")
					}
					matchingLines = append(matchingLines, line)
				}
			}
		}

		if len(matchingLines) == 0 {
			text = "<i>(grep output empty)</i>"
		} else {
			text = strings.Join(matchingLines, "\n")
		}
	}

	// Apply cut (keep first N lines)
	if m.CutLines > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > m.CutLines {
			lines = lines[:m.CutLines]
		}
		text = strings.Join(lines, "\n")
	}

	plainText, _ := parseHTML(text)

	// Apply split (send as multiple messages instead of file)
	if m.SplitOutput && telegramTextLen(plainText) >= telegramMessageLimit {
		chunks := splitPlainTextForTelegram(plainText, telegramMessageLimit)
		for i, chunk := range chunks {
			chunk = stdhtml.EscapeString(chunk)
			if i == 0 {
				if m.Out {
					_, _ = m.Client.EditMessage(m.ChatID, m.ID, chunk, opts...)
				} else {
					_, _ = m.Client.SendMessageWithOptions(m.ChatID, chunk, opts...)
				}
			} else {
				_, _ = m.Client.SendMessageWithOptions(m.ChatID, chunk, opts...)
			}
		}
		return nil
	}

	plan := planLongAnswer(text, m.GrepQuery == "")
	switch plan.mode {
	case answerModeInlineList:
		if m.Client != nil {
			if im, ok := m.Client.GorokuInline.(*inline.InlineManager); ok && im != nil && im.IsComplete() {
				if _, err := im.List(m, plan.pages); err == nil {
					return nil
				}
			}
		}
		fallthrough
	case answerModeFile:
		fileText := plan.fileText
		if fileText == "" {
			fileText = plainText
		}
		if m.Out {
			_, _ = m.Client.EditMessage(m.ChatID, m.ID, "💾 <i>Output is too long. Sending as file...</i>")
		}
		tmpFile, err := os.CreateTemp("", "command_result_*.txt")
		if err == nil {
			defer os.Remove(tmpFile.Name())
			_, _ = tmpFile.WriteString(fileText)
			_ = tmpFile.Close()
			_, err = m.Client.SendFile(m.ChatID, tmpFile.Name(), "💾 Output too long")
			return err
		}
		_, err = m.Client.SendFile(m.ChatID, []byte(fileText), "💾 Output too long")
		return err
	}
	if m.Out {
		_, err := m.Client.EditMessage(m.ChatID, m.ID, text, opts...)
		return err
	}
	_, err := m.Client.SendMessageWithOptions(m.ChatID, text, opts...)
	return err
}

func WithInvertMedia(invert bool) MsgOption {
	return func(req interface{}) {
		if r, ok := req.(*tg.MessagesSendMessageRequest); ok {
			r.SetInvertMedia(invert)
		} else if r, ok := req.(*tg.MessagesEditMessageRequest); ok {
			r.SetInvertMedia(invert)
		}
	}
}

func WithNoWebpage(noWebpage bool) MsgOption {
	return func(req interface{}) {
		if r, ok := req.(*tg.MessagesSendMessageRequest); ok {
			r.SetNoWebpage(noWebpage)
		} else if r, ok := req.(*tg.MessagesEditMessageRequest); ok {
			r.SetNoWebpage(noWebpage)
		}
	}
}

func WithWebPageMedia(url string, optional bool, forceLarge bool) MsgOption {
	return func(req interface{}) {
		if url == "" {
			return
		}
		media := &tg.InputMediaWebPage{
			URL:             url,
			Optional:        optional,
			ForceLargeMedia: forceLarge,
		}
		media.SetFlags()
		if r, ok := req.(*tg.MessagesEditMessageRequest); ok {
			r.SetMedia(media)
			r.SetNoWebpage(false)
		} else if r, ok := req.(*tg.MessagesSendMediaRequest); ok {
			r.Media = media
		}
	}
}

func (c *CustomTelegramClient) ForbidConstructor(constructor int) {
	c.ForbiddenConstructors = append(c.ForbiddenConstructors, constructor)
}

func (c *CustomTelegramClient) ForbidConstructors(constructors []int) {
	c.ForbiddenConstructors = append(c.ForbiddenConstructors, constructors...)
}

func (c *CustomTelegramClient) SendCodeRequest(phone string) error {
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	sentCode, err := c.client.Auth().SendCode(c.ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return err
	}
	if sc, ok := sentCode.(*tg.AuthSentCode); ok {
		c.phoneCodeHash = sc.PhoneCodeHash
	}
	return nil
}

func (c *CustomTelegramClient) SignIn(phone, code, password string) error {
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	log.Printf("[DEBUG SignIn] phone=%q, code=%q, phoneCodeHash=%q, password=%q\n", phone, code, c.phoneCodeHash, password)
	var err error
	if password != "" {
		// 2FA password flow
		_, err = c.client.Auth().Password(c.ctx, password)
	} else {
		// Phone code flow
		_, err = c.client.Auth().SignIn(c.ctx, phone, code, c.phoneCodeHash)
	}
	if err == nil {
		if me, selfErr := c.client.Self(c.ctx); selfErr == nil {
			c.TGID = me.ID
			c.Username = me.Username
			c.GorokuMe = me
		}
	}
	return err
}

func (c *CustomTelegramClient) QRLogin() (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("client not connected")
	}
	token, err := c.client.QR().Export(c.ctx)
	if err != nil {
		return "", err
	}
	return token.URL(), nil
}

func (c *CustomTelegramClient) QRLoginStatus() (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("client not connected")
	}
	select {
	case <-c.qrLoginSignal:
		// Fast path: Telegram sent updateLoginToken.
	default:
		// gotd may not deliver updateLoginToken in every temporary web-login setup.
		// Import is still safe to poll: while pending it returns auth.loginToken.
	}

	auth, err := c.client.QR().Import(c.ctx)
	if err != nil {
		if strings.Contains(err.Error(), "AuthLoginToken") || strings.Contains(err.Error(), "auth.loginToken") {
			return "PENDING", nil
		}
		return "", err
	}
	if auth != nil && auth.User != nil {
		if user, ok := auth.User.(*tg.User); ok {
			c.TGID = user.ID
			c.Username = user.Username
			c.GorokuMe = user
		}
	}
	return "SUCCESS", nil
}

// splitText splits text into chunks of at most `length` runes, preferring to
// break at newlines then spaces (mirrors utils.SmartSplit but lives in goroku pkg).
func splitText(text string, length int) []string {
	runes := []rune(text)
	if len(runes) <= length {
		return []string{text}
	}
	var res []string
	for len(runes) > 0 {
		if len(runes) <= length {
			res = append(res, string(runes))
			break
		}
		chunk := runes[:length]
		cut := -1
		for i := length - 1; i >= length/2; i-- {
			if chunk[i] == '\n' {
				cut = i + 1
				break
			}
		}
		if cut == -1 {
			for i := length - 1; i >= length/2; i-- {
				if chunk[i] == ' ' {
					cut = i + 1
					break
				}
			}
		}
		if cut == -1 {
			cut = length
		}
		res = append(res, string(runes[:cut]))
		runes = runes[cut:]
	}
	return res
}

func (c *CustomTelegramClient) GetMessage(chat interface{}, msgID int64) (*Message, error) {
	peer, err := c.ResolvePeer(chat)
	if err != nil {
		return nil, err
	}

	var res tg.MessagesMessagesClass

	if peerChan, ok := peer.(*tg.InputPeerChannel); ok {
		inputChannel := &tg.InputChannel{
			ChannelID:  peerChan.ChannelID,
			AccessHash: peerChan.AccessHash,
		}
		res, err = c.rawAPI.ChannelsGetMessages(c.ctx, &tg.ChannelsGetMessagesRequest{
			Channel: inputChannel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}},
		})
	} else {
		res, err = c.rawAPI.MessagesGetMessages(c.ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}})
	}

	if err != nil {
		return nil, err
	}

	var tgMsg *tg.Message
	switch mClass := res.(type) {
	case *tg.MessagesMessagesSlice:
		if len(mClass.Messages) > 0 {
			if m, ok := mClass.Messages[0].(*tg.Message); ok {
				tgMsg = m
			}
		}
	case *tg.MessagesMessages:
		if len(mClass.Messages) > 0 {
			if m, ok := mClass.Messages[0].(*tg.Message); ok {
				tgMsg = m
			}
		}
	case *tg.MessagesChannelMessages:
		if len(mClass.Messages) > 0 {
			if m, ok := mClass.Messages[0].(*tg.Message); ok {
				tgMsg = m
			}
		}
	}

	if tgMsg == nil {
		return nil, fmt.Errorf("message not found")
	}

	hMsg := c.buildMessageFromTG(tgMsg)

	return hMsg, nil
}

// DownloadMedia downloads the document media of a message into a writer.
func (c *CustomTelegramClient) DownloadMedia(media interface{}, writer io.Writer) error {
	mediaDoc, ok := media.(*tg.MessageMediaDocument)
	if !ok {
		return fmt.Errorf("media is not a document")
	}
	doc, ok := mediaDoc.Document.(*tg.Document)
	if !ok {
		return fmt.Errorf("document media is empty or invalid")
	}

	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
	}

	_, err := downloader.NewDownloader().Download(c.rawAPI, loc).Stream(c.ctx, writer)
	return err
}

func (c *CustomTelegramClient) InlineQuery(botUsername string, query string, chatID int64) (*tg.MessagesBotResults, error) {
	peer, err := c.ResolvePeer(botUsername)
	if err != nil {
		return nil, err
	}

	var botUser tg.InputUserClass
	if u, ok := peer.(*tg.InputPeerUser); ok {
		botUser = &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}
	} else {
		return nil, fmt.Errorf("bot is not a user")
	}

	chatPeer, err := c.ResolvePeer(chatID)
	if err != nil {
		return nil, err
	}

	res, err := c.rawAPI.MessagesGetInlineBotResults(c.ctx, &tg.MessagesGetInlineBotResultsRequest{
		Bot:    botUser,
		Peer:   chatPeer,
		Query:  query,
		Offset: "",
	})
	return res, err
}

func (c *CustomTelegramClient) SendInlineBotResult(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error) {
	peer, err := c.ResolvePeer(chatID)
	if err != nil {
		return nil, err
	}

	var replyTo tg.InputReplyToClass
	if replyToMsgID != 0 {
		replyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(replyToMsgID)}
	}

	res, err := c.rawAPI.MessagesSendInlineBotResult(c.ctx, &tg.MessagesSendInlineBotResultRequest{
		Peer:     peer,
		QueryID:  queryID,
		ID:       resultID,
		RandomID: rand.Int63(),
		ReplyTo:  replyTo,
	})
	return res, err
}

func (c *CustomTelegramClient) RequestWebView(peerUsername string, platform string, url string) (string, error) {
	peer, err := c.ResolvePeer(peerUsername)
	if err != nil {
		return "", err
	}

	u, ok := peer.(*tg.InputPeerUser)
	if !ok {
		return "", fmt.Errorf("peer is not a user")
	}

	botUser := &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}

	res, err := c.rawAPI.MessagesRequestWebView(c.ctx, &tg.MessagesRequestWebViewRequest{
		Peer:        peer,
		Bot:         botUser,
		Platform:    platform,
		URL:         url,
		FromBotMenu: false,
	})
	if err != nil {
		return "", err
	}

	return res.URL, nil
}

func (c *CustomTelegramClient) FindChannelByTitle(title string) (interface{}, error) {
	if c.rawAPI == nil {
		return nil, fmt.Errorf("client not connected")
	}
	res, err := c.rawAPI.MessagesGetDialogs(c.ctx, &tg.MessagesGetDialogsRequest{
		Limit: 100,
	})
	if err != nil {
		return nil, err
	}

	var chats []tg.ChatClass
	switch dlg := res.(type) {
	case *tg.MessagesDialogsSlice:
		chats = dlg.Chats
	case *tg.MessagesDialogs:
		chats = dlg.Chats
	}

	for _, chat := range chats {
		var chatTitle string
		switch ch := chat.(type) {
		case *tg.Chat:
			chatTitle = ch.Title
		case *tg.Channel:
			chatTitle = ch.Title
		case *tg.ChatForbidden:
			chatTitle = ch.Title
		case *tg.ChannelForbidden:
			chatTitle = ch.Title
		}
		if chatTitle == title {
			switch ch := chat.(type) {
			case *tg.Chat:
				return &tg.InputPeerChat{ChatID: ch.ID}, nil
			case *tg.Channel:
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
			}
		}
	}
	return nil, fmt.Errorf("channel not found by title: %s", title)
}

func (c *CustomTelegramClient) CreateChannel(title, description string, megagroup, forum bool) (interface{}, error) {
	if c.rawAPI == nil {
		return nil, fmt.Errorf("client not connected")
	}
	res, err := c.rawAPI.ChannelsCreateChannel(c.ctx, &tg.ChannelsCreateChannelRequest{
		Title:     title,
		About:     description,
		Megagroup: megagroup,
		Forum:     forum,
	})
	if err != nil {
		return nil, err
	}

	var createdChat tg.ChatClass
	switch upd := res.(type) {
	case *tg.Updates:
		if len(upd.Chats) > 0 {
			createdChat = upd.Chats[0]
		}
	case *tg.UpdatesCombined:
		if len(upd.Chats) > 0 {
			createdChat = upd.Chats[0]
		}
	}

	if createdChat == nil {
		return nil, fmt.Errorf("no chat created in updates")
	}

	switch ch := createdChat.(type) {
	case *tg.Chat:
		return &tg.InputPeerChat{ChatID: ch.ID}, nil
	case *tg.Channel:
		return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
	}

	return nil, fmt.Errorf("unknown chat type created")
}

func (c *CustomTelegramClient) InviteBotToChannel(channelPeer interface{}) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	var botUser tg.InputUserClass
	if c.GorokuInline != nil {
		val := reflect.ValueOf(c.GorokuInline)
		if val.Kind() == reflect.Ptr {
			field := val.Elem().FieldByName("BotUsername")
			if field.IsValid() && field.Kind() == reflect.String {
				botUsername := field.String()
				peer, err := c.ResolvePeer(botUsername)
				if err == nil {
					if u, ok := peer.(*tg.InputPeerUser); ok {
						botUser = &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}
					}
				}
			}
		}
	}
	if botUser == nil {
		return fmt.Errorf("bot user not found or unresolved")
	}

	var inputChannel tg.InputChannelClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return fmt.Errorf("peer is not a channel")
	}

	_, err := c.rawAPI.ChannelsInviteToChannel(c.ctx, &tg.ChannelsInviteToChannelRequest{
		Channel: inputChannel,
		Users:   []tg.InputUserClass{botUser},
	})
	return err
}

func (c *CustomTelegramClient) ToggleForum(channelPeer interface{}, enabled bool) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}
	var inputChannel tg.InputChannelClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return fmt.Errorf("peer is not a channel")
	}

	_, err := c.rawAPI.ChannelsToggleForum(c.ctx, &tg.ChannelsToggleForumRequest{
		Channel: inputChannel,
		Enabled: enabled,
	})
	return err
}

func (c *CustomTelegramClient) CreateForumTopic(channelPeer interface{}, title, description string, iconEmojiID int64) (int64, error) {
	if c.rawAPI == nil {
		return 0, fmt.Errorf("client not connected")
	}
	var inputChannel tg.InputChannelClass
	var peer tg.InputPeerClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
		peer = ch
	} else {
		return 0, fmt.Errorf("peer is not a channel")
	}

	req := &tg.ChannelsCreateForumTopicRequest{
		Channel:  inputChannel,
		Title:    title,
		RandomID: rand.Int63(),
	}

	var premium bool
	if c.GorokuMe != nil {
		if u, ok := c.GorokuMe.(*tg.User); ok {
			premium = u.Premium
		}
	}

	if premium && iconEmojiID != 0 {
		req.SetIconEmojiID(iconEmojiID)
	}

	res, err := c.rawAPI.ChannelsCreateForumTopic(c.ctx, req)
	if err != nil {
		return 0, err
	}

	var msgID int
	if upd, ok := res.(*tg.Updates); ok {
		for _, u := range upd.Updates {
			switch ut := u.(type) {
			case *tg.UpdateMessageID:
				msgID = ut.ID
			case *tg.UpdateNewChannelMessage:
				if msg, ok := ut.Message.(*tg.Message); ok {
					msgID = msg.ID
				}
			}
		}
	}

	if msgID == 0 {
		return 0, fmt.Errorf("failed to retrieve topic ID from updates")
	}

	if description != "" {
		replyTo := &tg.InputReplyToMessage{
			ReplyToMsgID: msgID,
		}
		replyTo.SetTopMsgID(msgID)
		_, _ = c.rawAPI.MessagesSendMessage(c.ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  description,
			ReplyTo:  replyTo,
			RandomID: rand.Int63(),
		})
	}

	return int64(msgID), nil
}

func (c *CustomTelegramClient) SearchForumTopic(channelPeer interface{}, title string) (int64, error) {
	if c.rawAPI == nil {
		return 0, fmt.Errorf("client not connected")
	}
	var inputChannel tg.InputChannelClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return 0, fmt.Errorf("peer is not a channel")
	}

	res, err := c.rawAPI.ChannelsGetForumTopics(c.ctx, &tg.ChannelsGetForumTopicsRequest{
		Channel: inputChannel,
		Limit:   100,
	})
	if err != nil {
		return 0, err
	}

	for _, topicClass := range res.Topics {
		if topic, ok := topicClass.(*tg.ForumTopic); ok {
			if topic.Title == title {
				return int64(topic.ID), nil
			}
		}
	}
	return 0, fmt.Errorf("topic not found")
}

func (c *CustomTelegramClient) CreateGorokuFolder(botID int64) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	filters, err := c.rawAPI.MessagesGetDialogFilters(c.ctx)
	if err != nil {
		return err
	}

	folderID := 2
	for _, fClass := range filters.Filters {
		if df, ok := fClass.(*tg.DialogFilter); ok {
			if strings.TrimSpace(df.Title.Text) == "Goroku" {
				return nil // Goroku folder already exists
			}
			if df.ID >= folderID {
				folderID = df.ID + 1
			}
		}
	}

	res, err := c.rawAPI.MessagesGetDialogs(c.ctx, &tg.MessagesGetDialogsRequest{
		Limit:      100,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return err
	}

	var chats []tg.ChatClass
	var users []tg.UserClass
	switch dlg := res.(type) {
	case *tg.MessagesDialogsSlice:
		chats = dlg.Chats
		users = dlg.Users
	case *tg.MessagesDialogs:
		chats = dlg.Chats
		users = dlg.Users
	}

	var includePeers []tg.InputPeerClass
	var pinnedPeers []tg.InputPeerClass

	if botID != 0 {
		for _, u := range users {
			if user, ok := u.(*tg.User); ok && user.ID == botID {
				inlineBotPeer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
				pinnedPeers = append(pinnedPeers, inlineBotPeer)
				includePeers = append(includePeers, inlineBotPeer)
				break
			}
		}
	}

	officialIDs := map[int64]bool{
		2445389036: true,
		2341345589: true,
		2410964167: true,
	}

	for _, chat := range chats {
		var title string
		var isChannel bool
		var chatID int64
		var accessHash int64

		switch ch := chat.(type) {
		case *tg.Chat:
			title = ch.Title
			chatID = ch.ID
		case *tg.Channel:
			title = ch.Title
			chatID = ch.ID
			accessHash = ch.AccessHash
			isChannel = true
		}

		titleLower := strings.ToLower(title)
		match := strings.Contains(titleLower, "goroku") || officialIDs[chatID]
		if match {
			if isChannel {
				includePeers = append(includePeers, &tg.InputPeerChannel{ChannelID: chatID, AccessHash: accessHash})
			} else {
				includePeers = append(includePeers, &tg.InputPeerChat{ChatID: chatID})
			}
		}
	}

	_, err = c.rawAPI.MessagesUpdateDialogFilter(c.ctx, &tg.MessagesUpdateDialogFilterRequest{
		ID: folderID,
		Filter: &tg.DialogFilter{
			ID:              folderID,
			Title:           tg.TextWithEntities{Text: "Goroku"},
			Emoticon:        "🐱",
			PinnedPeers:     pinnedPeers,
			IncludePeers:    includePeers,
			ExcludePeers:    []tg.InputPeerClass{},
			ExcludeMuted:    false,
			ExcludeRead:     false,
			ExcludeArchived: false,
		},
	})
	return err
}

func parseHTML(htmlText string) (string, []tg.MessageEntityClass) {
	reEmoji := regexp.MustCompile(`(?i)<emoji\s+document_id=["']?([0-9]+)["']?>(.*?)</emoji>`)
	htmlText = reEmoji.ReplaceAllString(htmlText, `<tg-emoji emoji-id="$1">$2</tg-emoji>`)

	// Workaround gotd HTML parser bug: move trailing whitespaces out of closing tags to prevent entity length corruption
	reSpaceTag := regexp.MustCompile(`(?i)(\s+)(</(?:b|i|u|s|code|pre|tg-emoji|emoji|blockquote|tg-spoiler|spoiler)>)`)
	htmlText = reSpaceTag.ReplaceAllString(htmlText, `$2$1`)

	resolver := func(id int64) (tg.InputUserClass, error) {
		return &tg.InputUser{UserID: id}, nil
	}
	var b entity.Builder
	opt := html.String(resolver, htmlText)
	err := styling.Perform(&b, opt)
	if err != nil {
		return htmlText, nil
	}
	text, entities := b.Complete()
	return text, entities
}

func (c *CustomTelegramClient) Translate(chat interface{}, msgID int, toLang string) (string, error) {
	if c.rawAPI == nil {
		return "", fmt.Errorf("client not connected")
	}
	peer, err := c.ResolvePeer(chat)
	if err != nil {
		return "", err
	}
	res, err := c.rawAPI.MessagesTranslateText(c.ctx, &tg.MessagesTranslateTextRequest{
		Peer:   peer,
		ID:     []int{msgID},
		ToLang: toLang,
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, tw := range res.Result {
		sb.WriteString(tw.Text)
	}
	return sb.String(), nil
}
