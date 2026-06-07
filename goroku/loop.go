package goroku

import (
	"log"
	"sync"
	"time"
)

// InfiniteLoop runs a function repeatedly on a fixed interval.
// Attached to a module - stops automatically when module is unloaded.
type InfiniteLoop struct {
	mu         sync.RWMutex
	fn         func() error
	interval   time.Duration
	stopCh     chan struct{}
	running    bool
	ModuleName string
	autostart  bool
}

func NewInfiniteLoop(fn func() error, interval time.Duration, moduleName string, autostart bool) *InfiniteLoop {
	return &InfiniteLoop{
		fn:         fn,
		interval:   interval,
		stopCh:     make(chan struct{}, 1),
		ModuleName: moduleName,
		autostart:  autostart,
	}
}

func (l *InfiniteLoop) Start() {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.stopCh = make(chan struct{}, 1)
	l.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("InfiniteLoop panic in module %s: %v\n", l.ModuleName, r)
			}
			l.mu.Lock()
			l.running = false
			l.mu.Unlock()
		}()
		for {
			select {
			case <-l.stopCh:
				return
			case <-time.After(l.interval):
				if err := l.fn(); err != nil {
					log.Printf("InfiniteLoop error in module %s: %v\n", l.ModuleName, err)
				}
			}
		}
	}()
}

func (l *InfiniteLoop) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		select {
		case l.stopCh <- struct{}{}:
		default:
		}
	}
}

func (l *InfiniteLoop) IsRunning() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.running
}
