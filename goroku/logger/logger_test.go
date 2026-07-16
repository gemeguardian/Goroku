package logger

import (
	"os"
	"sync"
	"testing"
)

func TestInit(t *testing.T) {
	// Default (info) level.
	_ = os.Unsetenv("GOROKU_DEBUG")
	Init()
	if L() == nil {
		t.Fatal("Logger should not be nil after Init")
	}

	// Debug level.
	_ = os.Setenv("GOROKU_DEBUG", "1")
	defer func() { _ = os.Unsetenv("GOROKU_DEBUG") }()
	Init()
	if L() == nil {
		t.Fatal("Logger should not be nil after Init with GOROKU_DEBUG=1")
	}
}

func TestConcurrentInitAndL(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if (i+j)%3 == 0 {
					Init()
				}
				if L() == nil {
					t.Error("L returned nil")
				}
			}
		}(i)
	}
	wg.Wait()
}
