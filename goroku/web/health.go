package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"goroku/goroku/utils"
)

// healthPayload is the typed /health JSON body (no secrets).
type healthPayload struct {
	Status string `json:"status"`
	// Clients is how many accounts are registered; ClientsConnected is how many
	// of them have a live MTProto transport. A gap between the two is the
	// signal that the bot is up but deaf.
	Clients          int    `json:"clients"`
	ClientsConnected int    `json:"clients_connected"`
	SetupCompleted   bool   `json:"setup_completed"`
	Version          string `json:"version"`
}

// HealthHandler returns a minimal ops snapshot without secrets.
// GET /health
func (w *Web) HealthHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload := healthPayload{
		Status:           "ok",
		Clients:          w.clientCount(),
		ClientsConnected: w.connectedClientCount(),
		SetupCompleted:   SetupCompleted(w.dataRoot),
		Version:          utils.GetVersionRaw(),
	}
	wr.Header().Set("Content-Type", "application/json; charset=utf-8")
	wr.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		wr.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(wr).Encode(payload)
}

// HealthzHandler is a liveness probe (process is serving HTTP).
// GET /healthz
func (w *Web) HealthzHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wr.Header().Set("Content-Type", "text/plain; charset=utf-8")
	wr.Header().Set("Cache-Control", "no-store")
	wr.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		writeString(wr, "ok")
	}
}

// ReadyzHandler is a readiness probe: the bot can actually do its job.
//
// During onboarding there is no Telegram client yet and that is valid
// readiness. Once setup has completed, a registered account with a dead
// MTProto transport is *not* ready — reporting ok there is what let a bot
// stand silently dead behind a green health check.
// GET /readyz
func (w *Web) ReadyzHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wr.Header().Set("Content-Type", "text/plain; charset=utf-8")
	wr.Header().Set("Cache-Control", "no-store")

	status := http.StatusOK
	body := "ok"
	if SetupCompleted(w.dataRoot) && w.connectedClientCount() == 0 {
		status = http.StatusServiceUnavailable
		body = fmt.Sprintf("no telegram client connected (%d registered)", w.clientCount())
	}

	wr.WriteHeader(status)
	if r.Method != http.MethodHead {
		writeString(wr, body)
	}
}
