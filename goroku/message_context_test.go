package goroku

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type contextRecordingInvoker struct {
	contexts []context.Context
	err      error
}

func (r *contextRecordingInvoker) Invoke(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
	r.contexts = append(r.contexts, ctx)
	return r.err
}

type messageContextKey struct{}

type blockingContextInvoker struct {
	entered chan struct{}
	once    sync.Once
}

func (b *blockingContextInvoker) Invoke(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func TestMessageRPCOperationsUseMessageContext(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	rpcErr := errors.New("stop RPC")
	invoker := &contextRecordingInvoker{err: rpcErr}
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	client.rawAPI = tg.NewClient(invoker)
	messageCtx := context.WithValue(context.Background(), messageContextKey{}, "message")

	operations := map[string]func() error{
		"reply": func() error { return (&Message{ChatID: 42, Client: client, ctx: messageCtx}).Reply("text") },
		"reply lookup": func() error {
			_, err := (&Message{ChatID: 42, ReplyToMsgID: 1, Client: client, ctx: messageCtx}).GetReplyMessage()
			return err
		},
		"edit":   func() error { return (&Message{ID: 1, ChatID: 42, Client: client, ctx: messageCtx}).Edit("text") },
		"delete": func() error { return (&Message{ID: 1, ChatID: 42, Client: client, ctx: messageCtx}).Delete() },
		"answer": func() error { return (&Message{ChatID: 42, Client: client, ctx: messageCtx}).Answer("text") },
	}

	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			invoker.contexts = nil
			if err := operation(); !errors.Is(err, rpcErr) {
				t.Fatalf("operation error = %v, want %v", err, rpcErr)
			}
			if len(invoker.contexts) != 1 || invoker.contexts[0].Value(messageContextKey{}) != "message" {
				t.Fatalf("RPC contexts = %#v, want Message context", invoker.contexts)
			}
		})
	}
}

func TestMessageCancellationReleasesExecutorDuringPeerResolution(t *testing.T) {
	testMessageCancellationReleasesExecutor(t, 99, "text")
}

func TestMessageCancellationReleasesExecutorDuringLongFileFallback(t *testing.T) {
	testMessageCancellationReleasesExecutor(t, 42, strings.Repeat("x", telegramMessageLimit+1))
}

func testMessageCancellationReleasesExecutor(t *testing.T, chatID int64, answer string) {
	t.Helper()
	db := initializedTestDatabase(t, NewDatabase(42))
	invoker := &blockingContextInvoker{entered: make(chan struct{})}
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	client.rawAPI = tg.NewClient(invoker)
	executor, err := NewBoundedExecutor(BoundedExecutorConfig{Capacity: 1})
	if err != nil {
		t.Fatalf("NewBoundedExecutor() error = %v", err)
	}
	taskErr := make(chan error, 1)
	if err := executor.Submit(func(ctx context.Context) {
		msg := &Message{ChatID: chatID, Client: client, ctx: ctx}
		if answer == "text" {
			taskErr <- msg.Reply(answer)
			return
		}
		taskErr <- msg.Answer(answer)
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	select {
	case <-invoker.entered:
	case <-time.After(time.Second):
		t.Fatal("RPC did not start")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := executor.Close(closeCtx); err != nil {
		t.Fatalf("Close() did not release canceled RPC task: %v", err)
	}
	select {
	case err := <-taskErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("message operation error = %v, want context.Canceled", err)
		}
	default:
		t.Fatal("executor drained before message operation returned")
	}
}

func TestMessageRPCOutsideDispatcherFallsBackToClientContext(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	rpcErr := errors.New("stop RPC")
	invoker := &contextRecordingInvoker{err: rpcErr}
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	client.rawAPI = tg.NewClient(invoker)
	client.ctx = context.WithValue(context.Background(), messageContextKey{}, "client")

	err := (&Message{ChatID: 42, Client: client}).Reply("text")
	if !errors.Is(err, rpcErr) {
		t.Fatalf("Reply() error = %v, want %v", err, rpcErr)
	}
	if len(invoker.contexts) != 1 || invoker.contexts[0].Value(messageContextKey{}) != "client" {
		t.Fatalf("RPC contexts = %#v, want client fallback context", invoker.contexts)
	}
}
