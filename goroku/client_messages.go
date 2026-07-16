package goroku

import (
	"context"
	"fmt"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

func (c *CustomTelegramClient) GetMessage(chat ChatRef, msgID int64) (*Message, error) {
	return c.GetMessageContext(nil, chat, msgID)
}

func (c *CustomTelegramClient) GetMessageContext(ctx context.Context, chat ChatRef, msgID int64) (*Message, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	peer, err := c.ResolvePeerRefContext(ctx, chat)
	if err != nil {
		return nil, err
	}

	var res tg.MessagesMessagesClass
	if peerChan, ok := peer.(*tg.InputPeerChannel); ok {
		inputChannel := &tg.InputChannel{
			ChannelID:  peerChan.ChannelID,
			AccessHash: peerChan.AccessHash,
		}
		res, err = c.rawAPI.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: inputChannel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}},
		})
	} else {
		res, err = c.rawAPI.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}})
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
	hMsg.ctx = ctx

	return hMsg, nil
}

// DownloadMedia downloads the document media of a message into a writer.
func (c *CustomTelegramClient) DownloadMedia(media tg.MessageMediaClass, writer io.Writer) error {
	return c.DownloadMediaContext(nil, media, writer)
}

func (c *CustomTelegramClient) DownloadMediaContext(ctx context.Context, media tg.MessageMediaClass, writer io.Writer) error {
	if c.rawAPI == nil {
		return ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
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

	_, err := downloader.NewDownloader().Download(c.rawAPI, loc).Stream(ctx, writer)
	return err
}
