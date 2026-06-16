package goroku

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestInfiniteLoopLifecycle(t *testing.T) {
	var count int32

	loopFn := func() error {
		atomic.AddInt32(&count, 1)
		return nil
	}

	l := NewInfiniteLoop(loopFn, 5*time.Millisecond, "TestMod", false)
	if l.IsRunning() {
		t.Error("Loop should not be running initially")
	}

	l.Start()
	// Wait a bit to ensure it ticked
	time.Sleep(15 * time.Millisecond)

	if !l.IsRunning() {
		t.Error("Loop should be running after Start")
	}

	l.Stop()
	time.Sleep(10 * time.Millisecond)

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
	
	// Wait for loop to run and panic
	time.Sleep(10 * time.Millisecond)

	if l.IsRunning() {
		t.Error("Loop should have stopped running due to panic")
	}
}

func TestInfiniteLoopErrorLogging(t *testing.T) {
	errorFn := func() error {
		return errors.New("intentional error")
	}

	l := NewInfiniteLoop(errorFn, 2*time.Millisecond, "ErrorMod", false)
	l.Start()
	time.Sleep(10 * time.Millisecond)
	l.Stop()
}
