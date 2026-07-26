package modules

import (
	"context"
	"errors"
	"testing"
	"time"
)

// One global slot meant a 15-minute `.terminal tail -f` blocked every plugin
// build and every eval. The pools are independent now.
func TestBusyInteractivePoolDoesNotBlockBuildPool(t *testing.T) {
	if err := interactiveExecutor.TryAcquire(); err != nil {
		t.Fatalf("interactive pool was already busy: %v", err)
	}
	t.Cleanup(interactiveExecutor.Release)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd, err := buildExecutor.Command(ctx, ProcessSpec{Name: "true"})
	if err != nil {
		t.Fatalf("build pool refused work while the interactive pool was busy: %v", err)
	}
	buildExecutor.Release()
	if cmd == nil {
		t.Fatal("build pool returned no command")
	}
}

// A saturated pool must answer immediately rather than queue the caller until
// its own deadline expires.
func TestSaturatedPoolFailsFastInsteadOfHanging(t *testing.T) {
	executor := NewProcessExecutor(1, externalOutputLimit)
	if err := executor.TryAcquire(); err != nil {
		t.Fatalf("fresh pool reported busy: %v", err)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := executor.CommandNoWait(ctx, ProcessSpec{Name: "true"})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrExecutorBusy) {
		t.Fatalf("CommandNoWait error = %v, want ErrExecutorBusy", err)
	}
	if elapsed > time.Second {
		t.Fatalf("CommandNoWait waited %v on a saturated pool", elapsed)
	}

	executor.Release()
	if _, err := executor.CommandNoWait(ctx, ProcessSpec{Name: "true"}); err != nil {
		t.Fatalf("pool stayed busy after release: %v", err)
	}
	executor.Release()
}

// Each pool still serializes its own work: that is what the original single
// slot was protecting.
func TestPoolStillSerializesItsOwnWork(t *testing.T) {
	executor := NewProcessExecutor(1, externalOutputLimit)
	ctx := context.Background()

	if _, err := executor.CommandNoWait(ctx, ProcessSpec{Name: "true"}); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := executor.CommandNoWait(ctx, ProcessSpec{Name: "true"}); !errors.Is(err, ErrExecutorBusy) {
		t.Fatalf("second acquire error = %v, want ErrExecutorBusy", err)
	}
	executor.Release()
}

func TestPoolsAreDistinct(t *testing.T) {
	pools := map[string]*ProcessExecutor{
		"interactive": interactiveExecutor,
		"build":       buildExecutor,
		"eval":        evalExecutor,
	}
	seen := make(map[*ProcessExecutor]string, len(pools))
	for name, pool := range pools {
		if other, dup := seen[pool]; dup {
			t.Fatalf("pools %q and %q are the same executor", name, other)
		}
		seen[pool] = name
	}
	if cap(evalExecutor.slots) < 1 || cap(buildExecutor.slots) < 1 || cap(interactiveExecutor.slots) < 1 {
		t.Fatal("a pool has no slots")
	}
}
