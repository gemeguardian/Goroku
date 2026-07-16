package goroku

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
)

// ModuleNameResolver supplies registered module names for owner normalization.
// Implemented by *Modules (ModuleNames).
type ModuleNameResolver interface {
	ModuleNames() []string
}

// ConfigReloader reloads a module config after a successful document mutation.
// Implemented by *Modules (ReloadModuleConfig).
type ConfigReloader interface {
	ReloadModuleConfig(name string)
}

var (
	_ ModuleNameResolver = (*Modules)(nil)
	_ ConfigReloader     = (*Modules)(nil)
)

// DocumentStore is the KV/JSON/Redis persistence body of Database.
// Database embeds DocumentStore and remains the public façade.
type DocumentStore struct {
	mu          sync.RWMutex
	persistMu   sync.Mutex
	redisClient *redis.Client
	dbFile      string
	tgID        int64
	data        map[string]map[string]any
	revisions   []map[string]map[string]any
	// Redis batching: mirrors Python's asyncio.sleep(5) before redis save
	redisDirty    bool
	generation    uint64
	lastRedisSave int64
	flushCancel   context.CancelFunc
	flushDone     chan struct{}
	closing       bool
	closed        bool
	closeDone     chan struct{}
	closeErr      error
	initialized   bool
	lastRedisErr  error
	durabilityErr error
	durabilityGen uint64
	writeLocal    func(string, []byte) error

	moduleNames    ModuleNameResolver
	configReloader ConfigReloader
}

func (s *DocumentStore) setModuleNameResolver(r ModuleNameResolver) {
	s.mu.Lock()
	s.moduleNames = r
	s.mu.Unlock()
}

func (s *DocumentStore) setConfigReloader(r ConfigReloader) {
	s.mu.Lock()
	s.configReloader = r
	s.mu.Unlock()
}

func (s *DocumentStore) moduleNameResolver() ModuleNameResolver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.moduleNames
}

func (s *DocumentStore) configReloaderHook() ConfigReloader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configReloader
}

// scheduleConfigReload invokes ConfigReloader outside any DocumentStore lock.
func (s *DocumentStore) scheduleConfigReload(owner string) {
	if reloader := s.configReloaderHook(); reloader != nil {
		go reloader.ReloadModuleConfig(owner)
	}
}
