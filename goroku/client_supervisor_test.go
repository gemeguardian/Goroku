package goroku

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// newTestSupervisor returns a supervisor with every side effect captured and
// the clock frozen, so no run ever counts as healthy and backoff keeps growing.
func newTestSupervisor(t *testing.T) (*clientSupervisor, *[]time.Duration, *int) {
	t.Helper()
	frozen := time.Unix(1700000000, 0)
	var sleeps []time.Duration
	restarts := 0
	s := &clientSupervisor{
		tgID:           42,
		requestRestart: func() { restarts++ },
		sleep: func(ctx context.Context, d time.Duration) bool {
			sleeps = append(sleeps, d)
			return ctx.Err() == nil
		},
		now:          func() time.Time { return frozen },
		jitter:       func(d time.Duration) time.Duration { return d },
		baseBackoff:  time.Second,
		maxBackoff:   60 * time.Second,
		maxFailures:  10,
		healthyAfter: time.Minute,
	}
	return s, &sleeps, &restarts
}

// A connection that keeps dying must keep coming back, and the pauses between
// attempts must grow instead of hammering Telegram.
func TestSupervisorReconnectsWithGrowingBackoff(t *testing.T) {
	s, sleeps, restarts := newTestSupervisor(t)

	ends := make(chan error, 3)
	for i := 0; i < 3; i++ {
		ends <- errors.New("transport died")
	}
	reconnects := 0
	s.waitRunEnd = func(ctx context.Context) (error, bool) {
		select {
		case err := <-ends:
			return err, true
		default:
			return nil, false
		}
	}
	s.reconnect = func(ctx context.Context) error {
		reconnects++
		return nil
	}

	s.run(context.Background())

	if reconnects != 3 {
		t.Fatalf("reconnects = %d, want 3", reconnects)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(*sleeps) != len(want) {
		t.Fatalf("backoff pauses = %v, want %v", *sleeps, want)
	}
	for i, d := range want {
		if (*sleeps)[i] != d {
			t.Fatalf("backoff pauses = %v, want %v", *sleeps, want)
		}
	}
	if *restarts != 0 {
		t.Fatalf("restarts = %d, want 0 while reconnects still succeed", *restarts)
	}
}

func TestSupervisorBackoffIsCapped(t *testing.T) {
	s, _, _ := newTestSupervisor(t)
	if got := s.backoffFor(100); got != s.maxBackoff {
		t.Fatalf("backoffFor(100) = %v, want the %v ceiling", got, s.maxBackoff)
	}
	if got := s.backoffFor(0); got != s.baseBackoff {
		t.Fatalf("backoffFor(0) = %v, want %v", got, s.baseBackoff)
	}
}

func TestLowerHalfJitterStaysInRange(t *testing.T) {
	d := 8 * time.Second
	for i := 0; i < 200; i++ {
		got := lowerHalfJitter(d)
		if got < d/2 || got > d {
			t.Fatalf("lowerHalfJitter(%v) = %v, want within [%v, %v]", d, got, d/2, d)
		}
	}
}

// Standing dead is not an outcome: once the budget is gone the process must be
// restarted so systemd or Docker brings it back.
func TestSupervisorRequestsRestartAfterBudgetExhausted(t *testing.T) {
	s, sleeps, restarts := newTestSupervisor(t)
	s.maxFailures = 3

	s.waitRunEnd = func(ctx context.Context) (error, bool) { return errors.New("transport died"), true }
	reconnects := 0
	s.reconnect = func(ctx context.Context) error {
		reconnects++
		return errors.New("dial failed")
	}

	s.run(context.Background())

	if *restarts != 1 {
		t.Fatalf("restarts = %d, want 1", *restarts)
	}
	if reconnects != 3 {
		t.Fatalf("reconnect attempts = %d, want maxFailures=3", reconnects)
	}
	if len(*sleeps) != 3 {
		t.Fatalf("backoff pauses = %v, want one per attempt", *sleeps)
	}
}

// An unregistered auth key is terminal; retrying only burns connection limits.
func TestSupervisorDoesNotRetryUnregisteredAuthKey(t *testing.T) {
	s, sleeps, restarts := newTestSupervisor(t)

	delivered := false
	s.waitRunEnd = func(ctx context.Context) (error, bool) {
		if delivered {
			t.Fatal("supervisor kept going after a terminal auth failure")
		}
		delivered = true
		return errors.New("rpc error code 401: AUTH_KEY_UNREGISTERED"), true
	}
	s.reconnect = func(ctx context.Context) error {
		t.Fatal("reconnected after AUTH_KEY_UNREGISTERED")
		return nil
	}

	s.run(context.Background())

	if len(*sleeps) != 0 || *restarts != 0 {
		t.Fatalf("terminal failure produced backoff %v and %d restarts, want neither", *sleeps, *restarts)
	}
}

// Canceling the application lifecycle must stop the supervisor rather than
// leave it looping for the life of the process.
func TestSupervisorStopsOnLifecycleCancel(t *testing.T) {
	before := runtime.NumGoroutine()

	s, _, _ := newTestSupervisor(t)
	s.waitRunEnd = func(ctx context.Context) (error, bool) {
		<-ctx.Done()
		return nil, false
	}
	s.reconnect = func(ctx context.Context) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop after the lifecycle context was canceled")
	}

	// The scheduler needs a moment to retire the goroutine.
	for i := 0; i < 100 && runtime.NumGoroutine() > before; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before+2 {
		t.Fatalf("goroutines = %d after cancel, was %d before: supervisor leaked", got, before)
	}
}

// A connection that stays up past healthyAfter clears the failure budget, so a
// link that flaps once an hour never restarts the process.
func TestSupervisorResetsBudgetAfterHealthyRun(t *testing.T) {
	s, sleeps, restarts := newTestSupervisor(t)
	s.maxFailures = 2

	clock := time.Unix(1700000000, 0)
	s.now = func() time.Time { return clock }
	s.reconnect = func(ctx context.Context) error {
		// The connection lives well past healthyAfter before dying again.
		clock = clock.Add(2 * time.Minute)
		return nil
	}
	remaining := 4
	s.waitRunEnd = func(ctx context.Context) (error, bool) {
		if remaining == 0 {
			return nil, false
		}
		remaining--
		return errors.New("transport died"), true
	}

	s.run(context.Background())

	if *restarts != 0 {
		t.Fatalf("restarts = %d, want 0: every run was healthy", *restarts)
	}
	for _, d := range *sleeps {
		if d != s.baseBackoff {
			t.Fatalf("backoff pauses = %v, want every one reset to %v", *sleeps, s.baseBackoff)
		}
	}
}

func TestConnectionStateTransitions(t *testing.T) {
	c := NewCustomTelegramClient(42)
	if got := c.ConnectionState(); got != ConnectionDisconnected {
		t.Fatalf("fresh client state = %v, want disconnected", got)
	}
	if c.Connected() {
		t.Fatal("fresh client reports connected")
	}

	c.setConnectionState(ConnectionConnecting)
	if got := c.ConnectionState(); got != ConnectionConnecting || c.Connected() {
		t.Fatalf("state = %v, Connected = %v, want connecting/false", got, c.Connected())
	}

	c.setConnectionState(ConnectionConnected)
	if !c.Connected() {
		t.Fatal("client does not report connected after transition")
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.Connected() {
		t.Fatal("client still reports connected after Close")
	}
}

func TestConnectionStateString(t *testing.T) {
	for state, want := range map[ConnectionState]string{
		ConnectionDisconnected: "disconnected",
		ConnectionConnecting:   "connecting",
		ConnectionConnected:    "connected",
	} {
		if got := state.String(); got != want {
			t.Errorf("ConnectionState(%d).String() = %q, want %q", state, got, want)
		}
	}
}
