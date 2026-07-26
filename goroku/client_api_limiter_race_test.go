package goroku

import (
	"context"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
)

// newForbiddenTestClient builds a client with the given constructors blocked.
func newForbiddenTestClient(ids ...uint32) *CustomTelegramClient {
	c := &CustomTelegramClient{}
	c.SetForbiddenConstructors(ids)
	return c
}

// The forbidden list is a security control read on every outgoing RPC and
// rewritten by the config reload goroutine. Reads and writes must be
// synchronized, and a blocked constructor must stay blocked throughout.
func TestForbiddenConstructorsSurviveConcurrentReload(t *testing.T) {
	const blocked uint32 = 4242

	client := newForbiddenTestClient(blocked)
	invoker := &forbiddenInvoker{
		parent: &mockInvoker{onInvoke: func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			return nil
		}},
		client: client,
	}

	const readers, writers, iterations = 8, 4, 500
	var wg sync.WaitGroup
	leaked := make(chan struct{}, readers*iterations)

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if err := invoker.Invoke(context.Background(), typedTestRequest{typeID: blocked}, nil); err == nil {
					leaked <- struct{}{}
				}
			}
		}()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Every published list keeps the blocked constructor: this
				// mirrors a config reload that changes the rest of the list.
				client.SetForbiddenConstructors([]uint32{blocked, uint32(worker*iterations + j)})
			}
		}(i)
	}
	wg.Wait()
	close(leaked)

	if n := len(leaked); n > 0 {
		t.Fatalf("%d forbidden calls got through during a concurrent reload", n)
	}
}

func TestForbidConstructorsDoesNotLoseConcurrentUpdates(t *testing.T) {
	client := newForbiddenTestClient()

	const writers, perWriter = 8, 50
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				client.ForbidConstructor(uint32(worker*perWriter + j))
			}
		}(i)
	}
	wg.Wait()

	got := client.ForbiddenConstructors()
	if len(got) != writers*perWriter {
		t.Fatalf("forbidden list has %d entries, want %d: concurrent appends were lost", len(got), writers*perWriter)
	}
	seen := make(map[uint32]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for i := 0; i < writers*perWriter; i++ {
		if !seen[uint32(i)] {
			t.Fatalf("constructor %d is missing from the forbidden list", i)
		}
	}
}

// Callers must not be able to reach into the published slice.
func TestForbiddenConstructorsReturnsACopy(t *testing.T) {
	client := newForbiddenTestClient(1, 2, 3)

	snapshot := client.ForbiddenConstructors()
	snapshot[0] = 999

	if got := client.ForbiddenConstructors(); got[0] != 1 {
		t.Fatalf("forbidden list [0] = %d after mutating a snapshot, want 1", got[0])
	}
}
