package goroku

import (
	"sync"
	"testing"

	"github.com/gotd/td/tg"
)

// The identity is published by the client.Run goroutine once the session
// authorizes, while the web login coordinator, the security manager and every
// module read it. Reads and writes have to be synchronized.
func TestClientIdentityIsRaceFree(t *testing.T) {
	client := NewCustomTelegramClient(0)

	const readers, writers, iterations = 8, 4, 500
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = client.TGIDValue()
				_ = client.Username()
				_ = client.Me()
			}
		}()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				id := int64(worker*iterations + j + 1)
				client.SetIdentity(id, "user", &tg.User{ID: id})
			}
		}(i)
	}
	wg.Wait()

	if client.TGIDValue() == 0 {
		t.Fatal("identity was never published")
	}
}

// SetIdentity publishes all three fields under one lock, so a reader that
// takes the lock once sees a consistent triple. Reading through the individual
// accessors is two separate snapshots by definition, which is why Identity()
// exists.
func TestSetIdentityPublishesConsistently(t *testing.T) {
	client := NewCustomTelegramClient(0)

	stop := make(chan struct{})
	mismatches := make(chan int64, 1)
	go func() {
		defer close(mismatches)
		for {
			select {
			case <-stop:
				return
			default:
			}
			id, _, me := client.Identity()
			if id != 0 && (me == nil || me.ID != id) {
				select {
				case mismatches <- id:
				default:
				}
				return
			}
		}
	}()

	for i := int64(1); i <= 2000; i++ {
		client.SetIdentity(i, "user", &tg.User{ID: i})
	}
	close(stop)

	for id := range mismatches {
		t.Fatalf("observed TGID %d beside a stale Me: identity was published in pieces", id)
	}
}

func TestIdentityAccessorsAreNilSafe(t *testing.T) {
	var client *CustomTelegramClient
	if got := client.TGIDValue(); got != 0 {
		t.Fatalf("TGIDValue on nil client = %d, want 0", got)
	}
	if got := client.Username(); got != "" {
		t.Fatalf("Username on nil client = %q, want empty", got)
	}
	if got := client.Me(); got != nil {
		t.Fatalf("Me on nil client = %v, want nil", got)
	}
	client.SetIdentity(1, "x", &tg.User{})
	client.SetTGID(1)
	client.SetUsername("x")
	client.SetMe(&tg.User{})
}
