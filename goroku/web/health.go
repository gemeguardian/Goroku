package web

import (
	"encoding/json"
	"net/http"
)

// healthPayload is the typed /health JSON body (no secrets).
type healthPayload struct {
	Status         string `json:"status"`
	Clients        int    `json:"clients"`
	SetupCompleted bool   `json:"setup_completed"`
}

// HealthHandler returns a minimal ops snapshot without secrets.
// GET /health
func (w *Web) HealthHandler(wr http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload := healthPayload{
		Status:         "ok",
		Clients:        w.clientCount(),
		SetupCompleted: SetupCompleted(w.dataRoot),
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

// ReadyzHandler is a readiness probe: web is accepting traffic.
// Does not require a logged-in Telegram client (onboarding is valid readiness).
// GET /readyz
func (w *Web) ReadyzHandler(wr http.ResponseWriter, r *http.Request) {
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
