package goroku

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInfiniteLoopLifecycle(t *testing.T) {
	var count int32
	done := make(chan struct{}, 10)

	loopFn := func() error {
		atomic.AddInt32(&count, 1)
		done <- struct{}{}
		return nil
	}

	l := NewInfiniteLoop(loopFn, 5*time.Millisecond, "TestMod", false)
	if l.IsRunning() {
		t.Error("Loop should not be running initially")
	}

	l.Start()

	// Wait for at least one tick via channel.
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Loop did not tick within timeout")
	}

	if !l.IsRunning() {
		t.Error("Loop should be running after Start")
	}

	l.Stop()

	// Wait for the goroutine to signal that it has exited.
	select {
	case <-l.Stopped():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Loop did not stop within timeout")
	}

	if l.IsRunning() {
		t.Error("Loop should not be running after Stop")
	}

	finalCount := atomic.LoadInt32(&count)
	if finalCount < 1 {
		t.Errorf("Expected loop to tick at least once, got %d ticks", finalCount)
	}
}

func TestInfiniteLoopPanicRecovery(t *testing.T) {
	panickingFn := func() error {
		panic("intentional panic")
	}

	l := NewInfiniteLoop(panickingFn, 2*time.Millisecond, "PanicMod", false)
	l.Start()

	select {
	case <-l.Stopped():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Loop did not stop after panic within timeout")
	}

	if l.IsRunning() {
		t.Error("Loop should have stopped running due to panic")
	}
}

func TestInfiniteLoopErrorLogging(t *testing.T) {
	var mu sync.Mutex
	var errCount int
	errDone := make(chan struct{}, 1)

	errorFn := func() error {
		mu.Lock()
		errCount++
		mu.Unlock()
		errDone <- struct{}{}
		return errors.New("intentional error")
	}

	l := NewInfiniteLoop(errorFn, 2*time.Millisecond, "ErrorMod", false)
	l.Start()

	select {
	case <-errDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Loop did not error within timeout")
	}

	l.Stop()
	<-l.Stopped()

	mu.Lock()
	if errCount < 1 {
		t.Errorf("Expected at least 1 error, got %d", errCount)
	}
	mu.Unlock()
}
