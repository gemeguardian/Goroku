package goroku

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ConnectionState is the observable state of an MTProto transport.
type ConnectionState int32

const (
	ConnectionDisconnected ConnectionState = iota
	ConnectionConnecting
	ConnectionConnected
)

func (s ConnectionState) String() string {
	switch s {
	case ConnectionConnecting:
		return "connecting"
	case ConnectionConnected:
		return "connected"
	default:
		return "disconnected"
	}
}

// ConnectionState reports the transport state. It is safe to call from any
// goroutine, including while ConnectContext is running.
func (c *CustomTelegramClient) ConnectionState() ConnectionState {
	if c == nil {
		return ConnectionDisconnected
	}
	return ConnectionState(c.connState.Load())
}

// Connected reports whether the transport is up. Used by the readiness probe.
func (c *CustomTelegramClient) Connected() bool {
	return c.ConnectionState() == ConnectionConnected
}

func (c *CustomTelegramClient) setConnectionState(state ConnectionState) {
	if c == nil {
		return
	}
	c.connState.Store(int32(state))
}

// Supervisor defaults. The ceiling is deliberately close to a minute: a longer
// pause makes a recoverable outage look like a dead bot, a shorter one burns
// Telegram's connection limits during a real outage.
const (
	supervisorBaseBackoff  = time.Second
	supervisorMaxBackoff   = 60 * time.Second
	supervisorMaxFailures  = 10
	supervisorHealthyAfter = time.Minute
)

// clientSupervisor keeps a dead MTProto connection from going unnoticed. When
// client.Run returns, it reconnects with exponential backoff; when the failure
// budget is spent it asks the process to restart, so systemd or Docker can
// bring it back. Standing dead and silent is never an outcome.
type clientSupervisor struct {
	tgID int64

	// waitRunEnd blocks until the current run loop exits and returns the error
	// it ended with. ok is false when ctx ended first — the app is shutting down.
	waitRunEnd func(ctx context.Context) (err error, ok bool)
	// reconnect re-establishes the transport.
	reconnect func(ctx context.Context) error
	// requestRestart asks the process to exit for a supervised restart.
	requestRestart func()
	// sleep waits d or until ctx ends; false means ctx ended.
	sleep func(ctx context.Context, d time.Duration) bool
	// now supplies the clock, so tests can decide whether a run counted as healthy.
	now func() time.Time
	// jitter spreads reconnects of separate accounts after a shared outage.
	// Injectable so backoff growth is assertable.
	jitter func(time.Duration) time.Duration

	baseBackoff  time.Duration
	maxBackoff   time.Duration
	maxFailures  int
	healthyAfter time.Duration
}

func newClientSupervisor(c *CustomTelegramClient) *clientSupervisor {
	return &clientSupervisor{
		tgID:           c.TGIDValue(),
		waitRunEnd:     c.waitRunEnd,
		reconnect:      c.ConnectContext,
		requestRestart: func() {},
		sleep:          sleepContext,
		now:            time.Now,
		jitter:         lowerHalfJitter,
		baseBackoff:    supervisorBaseBackoff,
		maxBackoff:     supervisorMaxBackoff,
		maxFailures:    supervisorMaxFailures,
		healthyAfter:   supervisorHealthyAfter,
	}
}

// sleepContext waits for d, returning false if ctx ended first.
func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// lowerHalfJitter returns a duration in [d/2, d]: never shorter than half the
// nominal delay, never longer than it.
func lowerHalfJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d - time.Duration(rand.Int63n(int64(d/2)+1)) //nolint:gosec // pacing, not a secret
}

// backoffFor returns the pause before reconnect attempt number failures
// (1-based), capped at maxBackoff and jittered to avoid a thundering herd of
// accounts reconnecting in lockstep after a shared outage.
func (s *clientSupervisor) backoffFor(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := s.baseBackoff
	for i := 1; i < failures && delay < s.maxBackoff; i++ {
		delay *= 2
	}
	if delay > s.maxBackoff {
		delay = s.maxBackoff
	}
	if s.jitter == nil {
		return delay
	}
	return s.jitter(delay)
}

// isTerminalRunError reports whether reconnecting is pointless. An unregistered
// auth key means the session is gone; HandleAuthKeyUnregistered has already
// dealt with it and retrying would only burn connection limits.
func isTerminalRunError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED")
}

// run supervises the connection until ctx ends, the session dies for good, or
// the reconnect budget is exhausted.
func (s *clientSupervisor) run(ctx context.Context) {
	failures := 0
	for {
		runErr, ok := s.waitRunEnd(ctx)
		if !ok {
			return
		}
		if isTerminalRunError(runErr) {
			L().Error("Telegram session is no longer valid; not reconnecting",
				zap.Int64("tg_id", s.tgID), zap.Error(runErr))
			return
		}
		L().Warn("Telegram connection ended; reconnecting",
			zap.Int64("tg_id", s.tgID), zap.Error(runErr))

		for {
			failures++
			if failures > s.maxFailures {
				L().Error("Telegram reconnect budget exhausted; requesting process restart",
					zap.Int64("tg_id", s.tgID), zap.Int("failures", failures-1))
				s.requestRestart()
				return
			}
			if !s.sleep(ctx, s.backoffFor(failures)) {
				return
			}
			connectedAt := s.now()
			if err := s.reconnect(ctx); err != nil {
				L().Warn("Telegram reconnect attempt failed",
					zap.Int64("tg_id", s.tgID), zap.Int("attempt", failures), zap.Error(err))
				continue
			}
			L().Info("Telegram connection restored",
				zap.Int64("tg_id", s.tgID), zap.Int("attempts", failures))
			// A connection that survives healthyAfter clears the budget. Without
			// this, a link that flaps once an hour would eventually exhaust it and
			// restart the process for no reason.
			if !s.now().Before(connectedAt.Add(s.healthyAfter)) {
				failures = 0
			}
			break
		}
	}
}

// waitRunEnd blocks until the run goroutine started by ConnectContext exits.
func (c *CustomTelegramClient) waitRunEnd(ctx context.Context) (error, bool) {
	c.runMu.Lock()
	done := c.runDone
	c.runMu.Unlock()
	if done == nil {
		// Nothing is running: treat it as an ended connection so the supervisor
		// reconnects rather than spinning on a nil channel.
		return nil, ctx.Err() == nil
	}
	select {
	case <-done:
	case <-ctx.Done():
		return nil, false
	}
	if ctx.Err() != nil {
		return nil, false
	}
	c.runMu.Lock()
	err := c.runErr
	c.runMu.Unlock()
	return err, true
}

// superviseConnection starts the reconnect supervisor for client, bound to the
// application lifecycle context. It returns immediately. Without a lifecycle
// context there is no Run() to keep alive — a client built directly in a test
// — and nothing is supervised.
func (h *Goroku) superviseConnection(client *CustomTelegramClient) {
	h.lifecycleMu.Lock()
	ctx := h.runContext
	h.lifecycleMu.Unlock()
	if ctx == nil || client == nil {
		return
	}
	supervisor := newClientSupervisor(client)
	supervisor.requestRestart = func() { h.RequestRestart() }
	go supervisor.run(ctx)
}
