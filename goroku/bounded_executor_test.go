package goroku

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestExecutor(t *testing.T, capacity int, panicHandler ExecutorPanicHandler) *BoundedExecutor {
	t.Helper()
	executor, err := NewBoundedExecutor(BoundedExecutorConfig{Capacity: capacity, PanicHandler: panicHandler})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestBoundedExecutorMaxConcurrencyAndOverflow(t *testing.T) {
	executor := newTestExecutor(t, 3, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	var active atomic.Int32
	var maximum atomic.Int32

	task := func(context.Context) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
	}
	for range 3 {
		if err := executor.Submit(task); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		<-started
	}

	err := executor.Submit(task)
	var rejection *ExecutorRejectError
	if !errors.As(err, &rejection) || rejection.Reason != ExecutorRejectCapacity {
		t.Fatalf("Submit() error = %v, want capacity rejection", err)
	}
	if !errors.Is(err, ErrExecutorCapacity) {
		t.Fatalf("Submit() error = %v, want ErrExecutorCapacity", err)
	}
	close(release)
	if err := executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 3 {
		t.Fatalf("maximum concurrency = %d, want 3", maximum.Load())
	}
}

func TestBoundedExecutorIndependentCapacities(t *testing.T) {
	commands := newTestExecutor(t, 1, nil)
	watchers := newTestExecutor(t, 2, nil)
	release := make(chan struct{})
	task := func(context.Context) { <-release }

	if err := commands.Submit(task); err != nil {
		t.Fatal(err)
	}
	if err := watchers.Submit(task); err != nil {
		t.Fatal(err)
	}
	if err := watchers.Submit(task); err != nil {
		t.Fatal(err)
	}
	if err := commands.Submit(task); !errors.Is(err, ErrExecutorCapacity) {
		t.Fatalf("second command Submit() error = %v, want capacity rejection", err)
	}
	if err := watchers.Submit(task); !errors.Is(err, ErrExecutorCapacity) {
		t.Fatalf("third watcher Submit() error = %v, want capacity rejection", err)
	}

	close(release)
	if err := commands.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := watchers.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedExecutorPanicRecoveryReleasesSlot(t *testing.T) {
	recovered := make(chan any, 1)
	executor := newTestExecutor(t, 1, func(value any) { recovered <- value })
	if err := executor.Submit(func(context.Context) { panic("task panic") }); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-recovered:
		if got != "task panic" {
			t.Fatalf("recovered = %v, want task panic", got)
		}
	case <-time.After(time.Second):
		t.Fatal("panic handler was not called")
	}

	deadline := time.Now().Add(time.Second)
	for executor.Active() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if err := executor.Submit(func(context.Context) {}); err != nil {
		t.Fatalf("Submit() after panic = %v, want accepted", err)
	}
	if err := executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedExecutorCloseWaitsAndCancels(t *testing.T) {
	executor := newTestExecutor(t, 1, nil)
	started := make(chan struct{})
	canceled := make(chan struct{})
	if err := executor.Submit(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
	}); err != nil {
		t.Fatal(err)
	}
	<-started

	done := make(chan error, 1)
	go func() { done <- executor.Close(context.Background()) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("task context was not canceled")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for task completion")
	}

	err := executor.Submit(func(context.Context) {})
	var rejection *ExecutorRejectError
	if !errors.As(err, &rejection) || rejection.Reason != ExecutorRejectClosed {
		t.Fatalf("Submit() after Close error = %v, want closed rejection", err)
	}
	if !errors.Is(err, ErrExecutorClosed) {
		t.Fatalf("Submit() after Close error = %v, want ErrExecutorClosed", err)
	}
}

func TestBoundedExecutorCloseTimeoutAndRepeatedClose(t *testing.T) {
	executor := newTestExecutor(t, 1, nil)
	release := make(chan struct{})
	started := make(chan struct{})
	if err := executor.Submit(func(context.Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := executor.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}
	close(release)
	if err := executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	if err := executor.Close(canceled); err != nil {
		t.Fatalf("Close() after drain with canceled context = %v", err)
	}
}

func TestBoundedExecutorSubmitCloseRace(t *testing.T) {
	for range 100 {
		executor := newTestExecutor(t, 4, nil)
		start := make(chan struct{})
		var submitters sync.WaitGroup
		for range 16 {
			submitters.Add(1)
			go func() {
				defer submitters.Done()
				<-start
				err := executor.Submit(func(context.Context) {})
				if err != nil && !errors.Is(err, ErrExecutorClosed) && !errors.Is(err, ErrExecutorCapacity) {
					t.Errorf("Submit() error = %v", err)
				}
			}()
		}
		close(start)
		if err := executor.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		submitters.Wait()
		if executor.Active() != 0 {
			t.Fatalf("active tasks = %d after Close", executor.Active())
		}
	}
}

func TestBoundedExecutorNoGoroutineLeakBestEffort(t *testing.T) {
	before := runtime.NumGoroutine()
	executor := newTestExecutor(t, 32, nil)
	for range 32 {
		if err := executor.Submit(func(ctx context.Context) { <-ctx.Done() }); err != nil {
			t.Fatal(err)
		}
	}
	if err := executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines before = %d, after = %d", before, after)
	}
}

func TestNewBoundedExecutorRejectsInvalidCapacity(t *testing.T) {
	if _, err := NewBoundedExecutor(BoundedExecutorConfig{}); !errors.Is(err, ErrInvalidExecutorCapacity) {
		t.Fatalf("NewBoundedExecutor() error = %v, want ErrInvalidExecutorCapacity", err)
	}
}
