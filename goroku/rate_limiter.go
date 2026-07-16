package goroku

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// RateLimitReason identifies why an event was rejected.
type RateLimitReason uint8

const (
	RateLimitAllowed RateLimitReason = iota
	RateLimitUser
	RateLimitChat
	RateLimitCapacity
	RateLimitInvalid
)

// RateLimiterConfig configures independent weighted user and chat windows.
// MaxEntries bounds the combined number of active user and chat keys. When the
// bound is full, a request needing a new key is rejected rather than evicting
// active state.
type RateLimiterConfig struct {
	UserLimit  int
	ChatLimit  int
	UserWindow time.Duration
	ChatWindow time.Duration
	MaxEntries int
	Clock      func() time.Time
}

// RateLimitEvent describes one command attempt. The caller decides owner
// bypass and derives Applicable, Weight, and retention from command metadata.
// WindowMultiplier defaults to one and cannot shorten the configured windows.
type RateLimitEvent struct {
	UserID           int64
	ChatID           int64
	Weight           int
	WindowMultiplier float64
	Applicable       bool
}

// RateLimitDecision contains the result and the earliest useful retry delay.
// RetryAfter is zero for allowed, invalid, capacity, and permanently overweight
// events. UserUsed and ChatUsed report usage before a rejected event or after an
// accepted event.
type RateLimitDecision struct {
	Allowed    bool
	Reason     RateLimitReason
	RetryAfter time.Duration
	UserUsed   int
	ChatUsed   int
}

type rateLimitHit struct {
	expires time.Time
	weight  int
}

type rateLimitBucket struct {
	hits []rateLimitHit
	used int
}

// RateLimiter is a concurrency-safe weighted sliding-window limiter. It has no
// background worker: each call lazily expires state while holding the lock.
type RateLimiter struct {
	mu sync.Mutex

	userLimit  int
	chatLimit  int
	userWindow time.Duration
	chatWindow time.Duration
	maxEntries int
	clock      func() time.Time

	users map[int64]*rateLimitBucket
	chats map[int64]*rateLimitBucket
}

// BoundedRateLimiter names the cardinality-bounded limiter used by dispatchers.
// RateLimiter is kept as the shorter public name for existing callers.
type BoundedRateLimiter = RateLimiter

func NewBoundedRateLimiter(config RateLimiterConfig) (*BoundedRateLimiter, error) {
	return NewRateLimiter(config)
}

func NewRateLimiter(config RateLimiterConfig) (*RateLimiter, error) {
	if config.UserLimit <= 0 {
		return nil, fmt.Errorf("user rate limit must be positive")
	}
	if config.ChatLimit <= 0 {
		return nil, fmt.Errorf("chat rate limit must be positive")
	}
	if config.UserWindow <= 0 {
		return nil, fmt.Errorf("user rate-limit window must be positive")
	}
	if config.ChatWindow <= 0 {
		return nil, fmt.Errorf("chat rate-limit window must be positive")
	}
	if config.MaxEntries <= 0 {
		return nil, fmt.Errorf("rate-limit maximum entries must be positive")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}

	return &RateLimiter{
		userLimit:  config.UserLimit,
		chatLimit:  config.ChatLimit,
		userWindow: config.UserWindow,
		chatWindow: config.ChatWindow,
		maxEntries: config.MaxEntries,
		clock:      config.Clock,
		users:      make(map[int64]*rateLimitBucket),
		chats:      make(map[int64]*rateLimitBucket),
	}, nil
}

// Allow atomically checks and, when accepted, records an event. UserID zero
// disables only the user check; every event still has an independent chat key.
func (l *RateLimiter) Allow(event RateLimitEvent) RateLimitDecision {
	if !event.Applicable {
		return RateLimitDecision{Allowed: true, Reason: RateLimitAllowed}
	}
	if event.Weight <= 0 {
		return RateLimitDecision{Reason: RateLimitInvalid}
	}
	multiplier := event.WindowMultiplier
	if multiplier == 0 {
		multiplier = 1
	}
	if multiplier < 1 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return RateLimitDecision{Reason: RateLimitInvalid}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	l.expire(now)

	user := l.users[event.UserID]
	chat := l.chats[event.ChatID]
	missing := 0
	if event.UserID != 0 && user == nil {
		missing++
	}
	if chat == nil {
		missing++
	}
	if len(l.users)+len(l.chats)+missing > l.maxEntries {
		return RateLimitDecision{Allowed: false, Reason: RateLimitCapacity}
	}

	decision := RateLimitDecision{Allowed: true, Reason: RateLimitAllowed}
	if user != nil {
		decision.UserUsed = user.used
	}
	if chat != nil {
		decision.ChatUsed = chat.used
	}

	if event.UserID != 0 && event.Weight > l.userLimit-decision.UserUsed {
		decision.Allowed = false
		decision.Reason = RateLimitUser
		decision.RetryAfter = retryAfter(user, event.Weight, l.userLimit, now)
		return decision
	}
	if event.Weight > l.chatLimit-decision.ChatUsed {
		decision.Allowed = false
		decision.Reason = RateLimitChat
		decision.RetryAfter = retryAfter(chat, event.Weight, l.chatLimit, now)
		return decision
	}

	if event.UserID != 0 {
		if user == nil {
			user = &rateLimitBucket{}
			l.users[event.UserID] = user
		}
		insertHit(user, rateLimitHit{expires: now.Add(scaleDuration(l.userWindow, multiplier)), weight: event.Weight})
		user.used += event.Weight
		decision.UserUsed = user.used
	}
	if chat == nil {
		chat = &rateLimitBucket{}
		l.chats[event.ChatID] = chat
	}
	insertHit(chat, rateLimitHit{expires: now.Add(scaleDuration(l.chatWindow, multiplier)), weight: event.Weight})
	chat.used += event.Weight
	decision.ChatUsed = chat.used
	return decision
}

// EntryCount returns the combined active user and chat key count.
func (l *RateLimiter) EntryCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.expire(l.clock())
	return len(l.users) + len(l.chats)
}

func (l *RateLimiter) expire(now time.Time) {
	for id, bucket := range l.users {
		expireBucket(bucket, now)
		if bucket.used == 0 {
			delete(l.users, id)
		}
	}
	for id, bucket := range l.chats {
		expireBucket(bucket, now)
		if bucket.used == 0 {
			delete(l.chats, id)
		}
	}
}

func expireBucket(bucket *rateLimitBucket, now time.Time) {
	first := 0
	for first < len(bucket.hits) && !bucket.hits[first].expires.After(now) {
		bucket.used -= bucket.hits[first].weight
		first++
	}
	if first > 0 {
		bucket.hits = bucket.hits[first:]
	}
}

func retryAfter(bucket *rateLimitBucket, weight, limit int, now time.Time) time.Duration {
	if weight > limit || bucket == nil {
		return 0
	}
	used := bucket.used
	for _, hit := range bucket.hits {
		used -= hit.weight
		if weight <= limit-used {
			retry := hit.expires.Sub(now)
			if retry > 0 {
				return retry
			}
			return 0
		}
	}
	return 0
}

func insertHit(bucket *rateLimitBucket, hit rateLimitHit) {
	index := len(bucket.hits)
	for index > 0 && hit.expires.Before(bucket.hits[index-1].expires) {
		index--
	}
	bucket.hits = append(bucket.hits, rateLimitHit{})
	copy(bucket.hits[index+1:], bucket.hits[index:])
	bucket.hits[index] = hit
}

func scaleDuration(window time.Duration, multiplier float64) time.Duration {
	if float64(window) > float64(math.MaxInt64)/multiplier {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(float64(window) * multiplier)
}
