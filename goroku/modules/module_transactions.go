package modules

import (
	"context"
	"errors"
	"fmt"
	"goroku/goroku"
	"path/filepath"
	"sync"
)

var moduleTransactionLocks sync.Map

// SelfModuleTransactionError reports a transaction rejected because its
// caller holds the target module's handler lease.
type SelfModuleTransactionError struct {
	Module string
	Action string
}

func (e *SelfModuleTransactionError) Error() string {
	return fmt.Sprintf("cannot %s module %s from its own active handler", e.Action, e.Module)
}

func rejectSelfModuleTransaction(msg *goroku.Message, loader *goroku.Modules, name, action string) error {
	if msg != nil && loader.ContextHoldsModuleLease(msg.Context(), name) {
		return &SelfModuleTransactionError{Module: name, Action: action}
	}
	return nil
}

// withModuleTransaction serializes changes to one runtime's module sources and manifest.
func withModuleTransaction(run func() error) error {
	key := filepath.Clean(runtimeModuleSourceDir())
	value, _ := moduleTransactionLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	return run()
}

func waitForModuleUnload(ctx context.Context, loader *goroku.Modules, name string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	unload := func() <-chan error {
		result := make(chan error, 1)
		go func() { result <- loader.UnloadModule(name) }()
		return result
	}
	wait := func(result <-chan error) (error, bool) {
		select {
		case err := <-result:
			return err, true
		case <-ctx.Done():
			return ctx.Err(), false
		}
	}

	err, completed := wait(unload())
	if !completed {
		return false, err
	}
	if !errors.Is(err, goroku.ErrModuleUnloadInProgress) {
		return loader.LookupByName(name) == nil, err
	}

	// The first call detached registrations. A repeated unload observes the
	// persistent teardown result after all active handler leases drain.
	err, completed = wait(unload())
	if !completed {
		return true, err
	}
	return true, err
}

func unloadModuleForTransaction(loader *goroku.Modules, name string) (bool, error) {
	return waitForModuleUnload(context.Background(), loader, name)
}

func restoreDetachedModule(loader *goroku.Modules, mod goroku.Module, cause error) error {
	if err := loader.RegisterModule(mod); err != nil {
		return errors.Join(cause, fmt.Errorf("restore module %s: %w", mod.Name(), err))
	}
	return cause
}

func moduleTransactionReport(action string, err error) string {
	if errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		return fmt.Sprintf("⚠️ <b>%s completed, but manifest durability is uncertain:</b> %v", action, err)
	}
	return fmt.Sprintf("❌ <b>%s failed:</b> %v", action, err)
}
