package goroku

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// InfiniteLoop runs a function repeatedly on a fixed interval.
// Attached to a module - stops automatically when module is unloaded.
type InfiniteLoop struct {
	mu         sync.RWMutex
	fn         func() error
	interval   time.Duration
	stopCh     chan struct{}
	stopOnce   sync.Once
	stoppedCh  chan struct{}
	running    bool
	ModuleName string
	autostart  bool
}

func NewInfiniteLoop(fn func() error, interval time.Duration, moduleName string, autostart bool) *InfiniteLoop {
	return &InfiniteLoop{
		fn:         fn,
		interval:   interval,
		stopCh:     make(chan struct{}),
		stoppedCh:  make(chan struct{}),
		ModuleName: moduleName,
		autostart:  autostart,
	}
}

// Stopped returns a channel that is closed when the loop goroutine exits.
func (l *InfiniteLoop) Stopped() <-chan struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.stoppedCh
}

func (l *InfiniteLoop) Start() {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	// Always use a fresh stop channel for a new lifecycle so that an old close
	// or a stop that raced ahead of Start cannot affect this run.
	l.stopCh = make(chan struct{})
	l.stopOnce = sync.Once{}
	// If the loop was previously stopped, recreate the stopped channel so that
	// new waiters can observe the next stop event.
	select {
	case <-l.stoppedCh:
		l.stoppedCh = make(chan struct{})
	default:
	}
	stop := l.stopCh
	stopped := l.stoppedCh
	l.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				L().Info("InfiniteLoop panic in module {0}: {1}", zap.Any("arg0", l.ModuleName), zap.Any("arg1", r))
			}
			l.mu.Lock()
			l.running = false
			close(stopped)
			l.mu.Unlock()
		}()
		for {
			select {
			case <-stop:
				return
			case <-time.After(l.interval):
				if err := l.fn(); err != nil {
					L().Info("InfiniteLoop error in module {0}: {1}", zap.Any("arg0", l.ModuleName), zap.Any("arg1", err))
				}
			}
		}
	}()
}

func (l *InfiniteLoop) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running {
		return
	}
	l.stopOnce.Do(func() { close(l.stopCh) })
}

func (l *InfiniteLoop) IsRunning() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.running
}
