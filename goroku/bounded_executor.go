package goroku

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrExecutorClosed          = errors.New("bounded executor closed")
	ErrExecutorCapacity        = errors.New("bounded executor capacity exceeded")
	ErrInvalidExecutorCapacity = errors.New("bounded executor capacity must be positive")
	ErrNilExecutorTask         = errors.New("bounded executor task is nil")
)

// ExecutorRejectReason identifies why a task was not accepted.
type ExecutorRejectReason uint8

const (
	ExecutorRejectClosed ExecutorRejectReason = iota + 1
	ExecutorRejectCapacity
)

// ExecutorRejectError reports a non-blocking submission rejection.
type ExecutorRejectError struct {
	Reason ExecutorRejectReason
}

func (e *ExecutorRejectError) Error() string {
	switch e.Reason {
	case ExecutorRejectClosed:
		return ErrExecutorClosed.Error()
	case ExecutorRejectCapacity:
		return ErrExecutorCapacity.Error()
	default:
		return "bounded executor submission rejected"
	}
}

func (e *ExecutorRejectError) Unwrap() error {
	switch e.Reason {
	case ExecutorRejectClosed:
		return ErrExecutorClosed
	case ExecutorRejectCapacity:
		return ErrExecutorCapacity
	default:
		return nil
	}
}

// ExecutorPanicHandler is called when a task panics. A panic in the handler is
// also recovered so task accounting and the process remain intact.
type ExecutorPanicHandler func(recovered any)

type BoundedExecutorConfig struct {
	Capacity     int
	PanicHandler ExecutorPanicHandler
	Context      context.Context
}

// BoundedExecutor starts accepted tasks immediately and rejects submissions
// once Capacity tasks are active. It has no task queue.
type BoundedExecutor struct {
	mu           sync.Mutex
	capacity     int
	active       int
	closed       bool
	drained      bool
	done         chan struct{}
	taskCtx      context.Context
	cancel       context.CancelFunc
	panicHandler ExecutorPanicHandler
}

type executorReservation struct {
	executor *BoundedExecutor
	once     sync.Once
}

func NewBoundedExecutor(config BoundedExecutorConfig) (*BoundedExecutor, error) {
	if config.Capacity <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidExecutorCapacity, config.Capacity)
	}

	parent := config.Context
	if parent == nil {
		parent = context.Background()
	}
	taskCtx, cancel := context.WithCancel(parent)
	return &BoundedExecutor{
		capacity:     config.Capacity,
		done:         make(chan struct{}),
		taskCtx:      taskCtx,
		cancel:       cancel,
		panicHandler: config.PanicHandler,
	}, nil
}

// CloseIntake atomically rejects future submissions and cancels active task
// contexts without waiting for handlers that do not observe cancellation.
func (e *BoundedExecutor) CloseIntake() {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		e.cancel()
		if e.active == 0 {
			e.drained = true
			close(e.done)
		}
	}
	e.mu.Unlock()
}

// Submit accepts task only when a slot is immediately available.
func (e *BoundedExecutor) Submit(task func(context.Context)) error {
	if task == nil {
		return ErrNilExecutorTask
	}
	reservation, err := e.reserve()
	if err != nil {
		return err
	}
	reservation.start(task)
	return nil
}

// reserve claims capacity without starting a goroutine. Callers must either
// start the reservation or release it.
func (e *BoundedExecutor) reserve() (*executorReservation, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, &ExecutorRejectError{Reason: ExecutorRejectClosed}
	}
	if e.active == e.capacity {
		e.mu.Unlock()
		return nil, &ExecutorRejectError{Reason: ExecutorRejectCapacity}
	}
	e.active++
	e.mu.Unlock()
	return &executorReservation{executor: e}, nil
}

func (r *executorReservation) start(task func(context.Context)) {
	r.once.Do(func() { go r.executor.run(task) })
}

func (r *executorReservation) release() {
	r.once.Do(r.executor.release)
}

func (e *BoundedExecutor) run(task func(context.Context)) {
	defer e.release()
	defer func() {
		if recovered := recover(); recovered != nil && e.panicHandler != nil {
			func() {
				defer func() { _ = recover() }()
				e.panicHandler(recovered)
			}()
		}
	}()
	task(e.taskCtx)
}

func (e *BoundedExecutor) release() {
	e.mu.Lock()
	e.active--
	if e.closed && e.active == 0 && !e.drained {
		e.drained = true
		close(e.done)
	}
	e.mu.Unlock()
}

// Close rejects future submissions, cancels active task contexts, and waits
// for every accepted task to return. A context timeout does not interrupt the
// shutdown; a later Close call may continue waiting.
func (e *BoundedExecutor) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.CloseIntake()
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()

	select {
	case <-done:
		return nil
	default:
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop is an alias for Close.
func (e *BoundedExecutor) Stop(ctx context.Context) error {
	return e.Close(ctx)
}

func (e *BoundedExecutor) Capacity() int {
	return e.capacity
}

func (e *BoundedExecutor) Active() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}
