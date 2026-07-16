package goroku

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakeRateLimitClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeRateLimitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeRateLimitClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestRateLimiter(t *testing.T, clock *fakeRateLimitClock, userLimit, chatLimit, maxEntries int) *RateLimiter {
	t.Helper()
	limiter, err := NewRateLimiter(RateLimiterConfig{
		UserLimit:  userLimit,
		ChatLimit:  chatLimit,
		UserWindow: 10 * time.Second,
		ChatWindow: 10 * time.Second,
		MaxEntries: maxEntries,
		Clock:      clock.Now,
	})
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	return limiter
}

func TestRateLimiterExactBoundaryAndRetry(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(1_000, 0)}
	limiter := newTestRateLimiter(t, clock, 5, 20, 10)
	event := RateLimitEvent{UserID: 1, ChatID: 10, Weight: 5, Applicable: true}

	if got := limiter.Allow(event); !got.Allowed || got.UserUsed != 5 {
		t.Fatalf("boundary decision = %+v, want allowed with user usage 5", got)
	}
	got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 10, Weight: 1, Applicable: true})
	if got.Allowed || got.Reason != RateLimitUser || got.RetryAfter != 10*time.Second {
		t.Fatalf("over-limit decision = %+v, want user rejection with 10s retry", got)
	}
	if got.UserUsed != 5 || got.ChatUsed != 5 {
		t.Fatalf("rejected event changed usage: %+v", got)
	}
}

func TestRateLimiterExpiryAtExactWindow(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(2_000, 0)}
	limiter := newTestRateLimiter(t, clock, 2, 2, 2)
	event := RateLimitEvent{UserID: 1, ChatID: 1, Weight: 2, Applicable: true}

	if got := limiter.Allow(event); !got.Allowed {
		t.Fatalf("initial decision = %+v, want allowed", got)
	}
	clock.Advance(10*time.Second - time.Nanosecond)
	if got := limiter.Allow(event); got.Allowed || got.RetryAfter != time.Nanosecond {
		t.Fatalf("pre-expiry decision = %+v, want 1ns retry", got)
	}
	clock.Advance(time.Nanosecond)
	if got := limiter.EntryCount(); got != 0 {
		t.Fatalf("EntryCount() at expiry = %d, want 0", got)
	}
	if got := limiter.Allow(event); !got.Allowed {
		t.Fatalf("decision at exact expiry = %+v, want allowed", got)
	}
}

func TestRateLimiterWeightedEventsAndApplicability(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(3_000, 0)}
	limiter := newTestRateLimiter(t, clock, 7, 20, 10)

	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 2}); !got.Allowed {
		t.Fatalf("non-applicable decision = %+v, want allowed", got)
	}
	if got := limiter.EntryCount(); got != 0 {
		t.Fatalf("non-applicable event created %d entries", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 2, Applicable: true}); !got.Allowed {
		t.Fatalf("weight-2 decision = %+v, want allowed", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 5, Applicable: true}); !got.Allowed || got.UserUsed != 7 {
		t.Fatalf("weight-5 decision = %+v, want exact weighted boundary", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 0, Applicable: true}); got.Reason != RateLimitInvalid {
		t.Fatalf("zero-weight decision = %+v, want invalid", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 2, ChatID: 2, Weight: math.MaxInt, Applicable: true}); got.Reason != RateLimitUser {
		t.Fatalf("extreme-weight decision = %+v, want user rejection", got)
	}
}

func TestRateLimiterSevereEventRetainsLongerCharge(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(3_500, 0)}
	limiter := newTestRateLimiter(t, clock, 7, 20, 10)
	ordinary := RateLimitEvent{UserID: 1, ChatID: 1, Weight: 2, Applicable: true}
	severe := RateLimitEvent{UserID: 1, ChatID: 1, Weight: 5, WindowMultiplier: 2.5, Applicable: true}

	if got := limiter.Allow(ordinary); !got.Allowed {
		t.Fatal(got)
	}
	if got := limiter.Allow(severe); !got.Allowed {
		t.Fatal(got)
	}
	clock.Advance(10 * time.Second)
	if got := limiter.Allow(ordinary); !got.Allowed || got.UserUsed != 7 {
		t.Fatalf("decision after ordinary expiry = %+v, want severe charge retained", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 1, Applicable: true}); got.Allowed || got.RetryAfter != 10*time.Second {
		t.Fatalf("sustained decision = %+v, want block until replacement ordinary hit expires", got)
	}
	clock.Advance(10 * time.Second)
	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 3, Applicable: true}); got.Allowed || got.RetryAfter != 5*time.Second {
		t.Fatalf("severe retention decision = %+v, want 5s remaining", got)
	}
	clock.Advance(5 * time.Second)
	if got := limiter.EntryCount(); got != 0 {
		t.Fatalf("entries after severe expiry = %d, want 0", got)
	}
}

func TestRateLimiterUserAndChatIndependence(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(4_000, 0)}
	limiter := newTestRateLimiter(t, clock, 2, 3, 20)

	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 10, Weight: 2, Applicable: true}); !got.Allowed {
		t.Fatalf("first decision = %+v, want allowed", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 2, ChatID: 20, Weight: 2, Applicable: true}); !got.Allowed {
		t.Fatalf("independent decision = %+v, want allowed", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 20, Weight: 1, Applicable: true}); got.Reason != RateLimitUser {
		t.Fatalf("user-limited decision = %+v, want user reason", got)
	}
	if got := limiter.Allow(RateLimitEvent{UserID: 0, ChatID: 10, Weight: 2, Applicable: true}); got.Reason != RateLimitChat {
		t.Fatalf("chat-limited decision = %+v, want chat reason", got)
	}
}

func TestRateLimiterCardinalityBoundAndLazyCleanup(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(5_000, 0)}
	limiter := newTestRateLimiter(t, clock, 10, 10, 2)

	if got := limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 1, Applicable: true}); !got.Allowed {
		t.Fatalf("initial decision = %+v, want allowed", got)
	}
	got := limiter.Allow(RateLimitEvent{UserID: 2, ChatID: 2, Weight: 1, Applicable: true})
	if got.Allowed || got.Reason != RateLimitCapacity {
		t.Fatalf("capacity decision = %+v, want fail-closed rejection", got)
	}
	if count := limiter.EntryCount(); count != 2 {
		t.Fatalf("EntryCount() = %d, want bounded count 2", count)
	}

	clock.Advance(10 * time.Second)
	if got := limiter.Allow(RateLimitEvent{UserID: 2, ChatID: 2, Weight: 1, Applicable: true}); !got.Allowed {
		t.Fatalf("post-cleanup decision = %+v, want allowed", got)
	}
	if count := limiter.EntryCount(); count != 2 {
		t.Fatalf("post-cleanup EntryCount() = %d, want 2", count)
	}
}

func TestRateLimiterConcurrentCalls(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(6_000, 0)}
	limiter := newTestRateLimiter(t, clock, 50, 50, 2)
	const calls = 200

	var wg sync.WaitGroup
	allowed := make(chan struct{}, calls)
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 1, Applicable: true}).Allowed {
				allowed <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(allowed)

	if got := len(allowed); got != 50 {
		t.Fatalf("allowed calls = %d, want exactly 50", got)
	}
}

func TestRateLimiterDoesNotCreateGoroutines(t *testing.T) {
	clock := &fakeRateLimitClock{now: time.Unix(7_000, 0)}
	limiter := newTestRateLimiter(t, clock, 10_000, 10_000, 2)
	before := runtime.NumGoroutine()

	for range 1_000 {
		limiter.Allow(RateLimitEvent{UserID: 1, ChatID: 1, Weight: 1, Applicable: true})
	}

	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutines grew from %d to %d", before, got)
	}
}
