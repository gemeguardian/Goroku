package web

import (
	"fmt"
	"sort"
)

func (w *Web) RegisterClient(runtime RuntimeClient) error {
	if runtime.ID <= 0 {
		return ErrInvalidClientID
	}
	if runtime.Client == nil {
		return ErrNilRuntimeClient
	}
	if runtime.Client.TGIDValue() != runtime.ID {
		return fmt.Errorf("%w: runtime ID %d does not match client ID %d", ErrInvalidClientID, runtime.ID, runtime.Client.TGIDValue())
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.clientData[runtime.ID]; exists {
		return fmt.Errorf("%w: %d", ErrDuplicateClient, runtime.ID)
	}
	w.nextGeneration++
	runtime.generation = w.nextGeneration
	w.clientData[runtime.ID] = runtime
	return nil
}

// UnregisterClient removes id from the runtime registry and reports whether it existed.
func (w *Web) UnregisterClient(id int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.clientData[id]; !exists {
		return false
	}
	delete(w.clientData, id)
	w.cancelPendingAuthsLocked(id, false)
	return true
}

// ListClients returns a stable snapshot without exposing the registry map.
func (w *Web) ListClients() []RuntimeClient {
	w.mu.RLock()
	clients := make([]RuntimeClient, 0, len(w.clientData))
	for _, runtime := range w.clientData {
		clients = append(clients, runtime)
	}
	w.mu.RUnlock()
	sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
	return clients
}

func (w *Web) clientCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.clientData)
}

// connectedClientCount counts registered clients whose MTProto transport is
// actually up. A registered but disconnected client is exactly the state the
// old static /readyz hid.
func (w *Web) connectedClientCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	connected := 0
	for _, runtime := range w.clientData {
		if runtime.Client != nil && runtime.Client.Connected() {
			connected++
		}
	}
	return connected
}
