package web

import (
	"net/http"
)

func (w *Web) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", w.RootHandler)
	mux.HandleFunc("/health", w.HealthHandler)
	mux.HandleFunc("/healthz", w.HealthzHandler)
	mux.HandleFunc("/readyz", w.ReadyzHandler)
	mux.HandleFunc("/set_api", w.checkSessionMiddleware(w.SetTGApiHandler))
	mux.HandleFunc("/send_tg_code", w.checkSessionMiddleware(w.SendTGCodeHandler))
	mux.HandleFunc("/check_session", w.CheckSessionHandler)
	mux.HandleFunc("/web_auth", w.WebAuthHandler)
	mux.HandleFunc("/tg_code", w.checkSessionMiddleware(w.TGCodeHandler))
	mux.HandleFunc("/finish_login", w.checkSessionMiddleware(w.FinishLoginHandler))
	mux.HandleFunc("/custom_bot", w.checkSessionMiddleware(w.CustomBotHandler))
	mux.HandleFunc("/init_qr_login", w.checkSessionMiddleware(w.InitQRLoginHandler))
	mux.HandleFunc("/get_qr_url", w.checkSessionMiddleware(w.GetQRURLHandler))
	mux.HandleFunc("/qr_2fa", w.checkSessionMiddleware(w.QR2FAHandler))
	mux.HandleFunc("/logout", w.checkSessionMiddleware(w.LogoutHandler))
	mux.HandleFunc("/can_add", w.CanAddHandler)
}
