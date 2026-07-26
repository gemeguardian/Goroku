package goroku

import (
	"fmt"
	"os"
	"sync"

	"github.com/gotd/td/tg"
)

// AssetInput is a typed input to StoreAsset. Construct it with AssetMessage,
// AssetText, or AssetFile. It replaces the previous untyped message parameter
// so the asset transport surface no longer accepts arbitrary any values.
type AssetInput interface {
	assetInput()
}

// AssetMessage stores the text of an existing Message to the assets channel.
type AssetMessage struct {
	Msg *Message
}

// AssetText stores a plain text string. If Text is an existing file path, the
// file is uploaded as a document instead of being sent as text.
type AssetText struct {
	Text string
}

// AssetFile stores a file payload as a Telegram document. File mirrors the
// payload types accepted by CustomTelegramClient.SendFile (string path, []byte,
// or io.Reader); that payload typing is a separate residual outside R7.1.
type AssetFile struct {
	File any
}

func (AssetMessage) assetInput() {}
func (AssetText) assetInput()    {}
func (AssetFile) assetInput()    {}

// AssetTransport performs Telegram RPC for asset store/fetch.
// Callers must not hold DocumentStore locks while invoking these methods.
type AssetTransport interface {
	StoreAsset(input AssetInput, targetChatID int64, assetsTopicID int) (int, error)
	FetchAsset(contentChannelID int64, assetID int) (*Message, error)
}

var _ AssetTransport = telegramAssetTransport{}

// AssetRepository stores and fetches Telegram assets using a narrow transport.
// Channel/topic metadata is read from DocumentStore under a short RLock; RPC
// runs only after that lock is released.
type AssetRepository struct {
	mu        sync.RWMutex
	transport AssetTransport
	store     *DocumentStore
}

func (r *AssetRepository) SetTransport(t AssetTransport) {
	r.mu.Lock()
	r.transport = t
	r.mu.Unlock()
}

func (r *AssetRepository) transportSnapshot() AssetTransport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transport
}

// snapshot returns transport and forum asset coordinates without holding locks
// across the subsequent Telegram RPC.
func (r *AssetRepository) snapshot() (AssetTransport, int, int64) {
	transport := r.transportSnapshot()
	if r.store == nil {
		return transport, 0, 0
	}

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var assetsTopicID int
	if forums := r.store.data["goroku.forums"]; forums != nil {
		if forumsCache, ok := forums["forums_cache"].(map[string]any); ok {
			if hubot, ok := forumsCache["goroku-userbot"].(map[string]any); ok {
				assetsTopicID = int(asInt64(hubot["Assets"], 0))
			}
		}
		return transport, assetsTopicID, asInt64(forums["channel_id"], 0)
	}
	return transport, assetsTopicID, 0
}

// StoreAsset stores a message or file to the assets channel.
// Returns the message ID (asset ID).
func (r *AssetRepository) StoreAsset(input AssetInput) (int, error) {
	transport, assetsTopicID, contentChannelID := r.snapshot()
	if transport == nil {
		return 0, fmt.Errorf("client not initialized in database")
	}
	if assetsTopicID == 0 {
		return 0, fmt.Errorf("Tried to save asset to non-existing asset topic.")
	}
	if contentChannelID == 0 {
		return 0, fmt.Errorf("Tried to save asset with non-existing content channel.")
	}
	return transport.StoreAsset(input, -1000000000000-contentChannelID, assetsTopicID)
}

// FetchAsset fetches a previously saved asset by its asset_id.
func (r *AssetRepository) FetchAsset(assetID int) (*Message, error) {
	transport, assetsTopicID, contentChannelID := r.snapshot()
	if transport == nil {
		return nil, fmt.Errorf("client not initialized in database")
	}
	if assetsTopicID == 0 {
		return nil, fmt.Errorf("Tried to fetch asset from non-existing asset topic.")
	}
	if contentChannelID == 0 {
		return nil, fmt.Errorf("Tried to fetch asset with non-existing content channel.")
	}
	return transport.FetchAsset(contentChannelID, assetID)
}

type telegramAssetTransport struct {
	client *CustomTelegramClient
}

func newTelegramAssetTransport(client *CustomTelegramClient) AssetTransport {
	return telegramAssetTransport{client: client}
}

func (c telegramAssetTransport) StoreAsset(input AssetInput, targetChatID int64, assetsTopicID int) (int, error) {
	opts := []MsgOption{WithReplyTo(int64(assetsTopicID))}
	var (
		res any
		err error
	)
	switch v := input.(type) {
	case AssetMessage:
		if v.Msg == nil {
			return 0, fmt.Errorf("StoreAsset: nil message")
		}
		res, err = c.client.SendMessageWithOptions(ChatRefID(targetChatID), v.Msg.Text, opts...)
	case AssetText:
		if _, statErr := os.Stat(v.Text); statErr == nil {
			res, err = c.client.SendFileWithOptions(ChatRefID(targetChatID), v.Text, "", opts...)
		} else {
			res, err = c.client.SendMessageWithOptions(ChatRefID(targetChatID), v.Text, opts...)
		}
	case AssetFile:
		res, err = c.client.SendFileWithOptions(ChatRefID(targetChatID), v.File, "", opts...)
	default:
		return 0, fmt.Errorf("unsupported asset input type: %T", input)
	}
	if err != nil {
		return 0, err
	}
	return int(GetSentMessageID(res)), nil
}

func (c telegramAssetTransport) FetchAsset(contentChannelID int64, assetID int) (*Message, error) {
	peer, err := c.client.ResolvePeer(contentChannelID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve content channel: %v", err)
	}
	peerChan, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, fmt.Errorf("content channel is not a channel peer")
	}

	res, err := c.client.rawAPI.ChannelsGetMessages(c.client.ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: peerChan.ChannelID, AccessHash: peerChan.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: assetID}},
	})
	if err != nil {
		return nil, err
	}

	var msg *tg.Message
	switch messages := res.(type) {
	case *tg.MessagesMessagesSlice:
		if len(messages.Messages) > 0 {
			msg, _ = messages.Messages[0].(*tg.Message)
		}
	case *tg.MessagesMessages:
		if len(messages.Messages) > 0 {
			msg, _ = messages.Messages[0].(*tg.Message)
		}
	case *tg.MessagesChannelMessages:
		if len(messages.Messages) > 0 {
			msg, _ = messages.Messages[0].(*tg.Message)
		}
	}
	if msg == nil {
		return nil, ErrNotFound
	}
	return &Message{
		ID:      int64(msg.ID),
		Text:    entitiesToHTML(msg.Message, msg.Entities),
		RawText: msg.Message,
		Out:     msg.Out,
		Client:  c.client,
	}, nil
}
