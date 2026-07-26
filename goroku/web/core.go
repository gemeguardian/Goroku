package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"goroku/goroku/webiface"

	"go.uber.org/zap"
)

var _ = zap.NewNop

// Server owns HTTP listener lifecycle (bind, serve, stop, URL).
type Server struct {
	serverMu        sync.RWMutex
	server          *http.Server
	listen          func(context.Context, string, string) (net.Listener, error)
	port            int
	running         bool
	ready           bool
	proxyPass       bool
	proxypasser     *TunnelManager
	url             string
	state           webLifecycleState
	startCancel     context.CancelFunc
	startDone       chan struct{}
	startReady      chan struct{}
	startGeneration *webStartGeneration
	stopDone        chan struct{}
	lifecycleErr    error
}

type webStartGeneration struct {
	done    chan struct{}
	err     error
	running bool
	waiters int
}

type webLifecycleState uint8

const (
	webStopped webLifecycleState = iota
	webStarting
	webRunning
	webStopping
)

// DefaultFallbackTGID is retained for source compatibility. Typed runtime
// registration rejects unknown IDs and does not use this fallback.
// Deprecated: register a RuntimeClient with its real Telegram ID.
const DefaultFallbackTGID = 123456789

// WebCore composes Web services with Server lifecycle.
type WebCore struct {
	*Web
	Server
}

func NewWebCore(cfg WebConfig) *WebCore {
	return &WebCore{
		Web: NewWeb(cfg),
		Server: Server{
			listen: (&net.ListenConfig{}).Listen,
		},
	}
}

// AddLoader preserves the historical API as a strict typed-registry wrapper.
// Invalid or duplicate runtimes are rejected rather than assigned a fake ID.
// Deprecated: use RegisterClient.
func (wc *WebCore) AddLoader(client webiface.TelegramClient, loader webiface.ModulesRegistry, db webiface.Database) {
	if client == nil || db == nil || client.TGIDValue() <= 0 {
		L().Warn("legacy AddLoader rejected invalid runtime")
		return
	}
	if err := wc.RegisterClient(RuntimeClient{
		ID:       client.TGIDValue(),
		Client:   client,
		Loader:   loader,
		Database: db,
	}); err != nil {
		L().Warn("legacy AddLoader failed", zap.Error(err))
	}
}

func (wc *WebCore) StartIfReady(totalCount int, port int, proxyPass bool) {
	clientCount := wc.clientCount()
	wc.serverMu.Lock()
	start := false
	if totalCount <= clientCount {
		start = wc.state == webStopped
		wc.ready = true
	}
	wc.serverMu.Unlock()
	if start {
		wc.StartAsync(port, proxyPass)
	}
}

func (wc *WebCore) GetURL(proxyPass bool) string {
	if os.Getenv("LAVHOST") != "" && os.Getenv("USER") != "" && os.Getenv("SERVER") != "" {
		return fmt.Sprintf("https://%s.%s.lavhost.ml", os.Getenv("USER"), os.Getenv("SERVER"))
	}

	wc.serverMu.RLock()
	ready := wc.startReady
	if wc.state != webStarting {
		ready = nil
	}
	wc.serverMu.RUnlock()
	if proxyPass && ready != nil {
		<-ready
	}

	wc.serverMu.RLock()
	proxypasser := wc.proxypasser
	tunnelEnabled := wc.state == webRunning && wc.proxyPass
	port := wc.port
	wc.serverMu.RUnlock()
	if proxyPass && tunnelEnabled && proxypasser != nil {
		url := proxypasser.GetURL(10 * time.Second)
		if url != "" {
			wc.serverMu.Lock()
			if wc.state == webRunning && wc.proxypasser == proxypasser {
				wc.url = url
			}
			wc.serverMu.Unlock()
			return url
		}
	}

	ip := "127.0.0.1"
	if os.Getenv("DOCKER") != "" {
		// Try resolving container hostname or interface
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						ip = ipnet.IP.String()
						break
					}
				}
			}
		}
	}

	if envIP := os.Getenv("GOROKU_IP"); envIP != "" {
		ip = envIP
	}

	url := "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
	wc.serverMu.Lock()
	wc.url = url
	wc.serverMu.Unlock()
	return url
}

func (wc *WebCore) SetPort(port int) {
	wc.serverMu.Lock()
	defer wc.serverMu.Unlock()
	if wc.port == 0 {
		wc.port = port
	}
}

func (wc *WebCore) Port() int {
	wc.serverMu.RLock()
	defer wc.serverMu.RUnlock()
	return wc.port
}

func (wc *WebCore) Start(port int, proxyPass bool) {
	start, _ := wc.beginStart(port, proxyPass)
	if start != nil {
		start()
	}
}

// StartContext starts serving and waits until the listener is bound or startup
// fails. Cancellation initiates component-owned cleanup without waiting past ctx.
func (wc *WebCore) StartContext(ctx context.Context, port int, proxyPass bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	start, generation := wc.beginStart(port, proxyPass)
	if generation == nil {
		wc.serverMu.RLock()
		err := wc.lifecycleErr
		running := wc.state == webRunning
		wc.serverMu.RUnlock()
		if running {
			return nil
		}
		if err != nil {
			return err
		}
		return context.Canceled
	}
	wc.serverMu.Lock()
	generation.waiters++
	wc.serverMu.Unlock()
	defer func() {
		wc.serverMu.Lock()
		generation.waiters--
		wc.serverMu.Unlock()
	}()
	if start != nil {
		go start()
	}
	select {
	case <-generation.done:
		if generation.err != nil {
			return generation.err
		}
		if !generation.running {
			return context.Canceled
		}
		return nil
	case <-ctx.Done():
		_ = wc.Close(ctx)
		return ctx.Err()
	}
}

// StartAsync registers startup before launching it, so a concurrent Stop cannot
// miss a Start goroutine that has not been scheduled yet.
func (wc *WebCore) StartAsync(port int, proxyPass bool) {
	start, _ := wc.beginStart(port, proxyPass)
	if start != nil {
		go start()
	}
}

func (wc *WebCore) beginStart(port int, proxyPass bool) (func(), *webStartGeneration) {
	wc.serverMu.Lock()
	if wc.state == webStarting {
		generation := wc.startGeneration
		wc.serverMu.Unlock()
		return nil, generation
	}
	if wc.state == webRunning {
		generation := wc.startGeneration
		wc.serverMu.Unlock()
		return nil, generation
	}
	if wc.state != webStopped {
		wc.serverMu.Unlock()
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	generation := &webStartGeneration{done: make(chan struct{})}
	wc.state = webStarting
	wc.startCancel = cancel
	wc.startDone = done
	wc.startReady = generation.done
	wc.startGeneration = generation
	wc.proxyPass = proxyPass
	wc.lifecycleErr = nil
	wc.startAuth()
	wc.serverMu.Unlock()

	return func() { wc.runStart(ctx, done, generation, port, proxyPass) }, generation
}

func (wc *WebCore) runStart(ctx context.Context, done chan struct{}, generation *webStartGeneration, port int, proxyPass bool) {
	runningPort := port
	if envPort := os.Getenv("PORT"); envPort != "" {
		if _, err := fmt.Sscanf(envPort, "%d", &runningPort); err != nil {
			L().Warn("invalid PORT env variable", zap.String("port", envPort), zap.Error(err))
		}
	}

	mux := http.NewServeMux()
	wc.SetupRoutes(mux)

	// R4.1: favicon is vendored locally (assets/static/favicon.jpeg) instead of
	// redirecting to a raw.githubusercontent.com URL, so the panel is offline.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/favicon.jpeg", http.StatusMovedPermanently)
	})

	// Static resources: disk override first, embedded assets as offline fallback.
	mux.Handle("/static/", wc.staticHandler())

	secureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		mux.ServeHTTP(w, r)
	})

	bindHost := strings.TrimSpace(os.Getenv("GOROKU_WEB_BIND"))
	if bindHost == "" {
		bindHost = strings.TrimSpace(os.Getenv("GOROKU_IP"))
	}
	if bindHost == "" {
		bindHost = "127.0.0.1"
		if os.Getenv("DOCKER") != "" {
			bindHost = "0.0.0.0"
		}
	}
	if !isLoopbackBind(bindHost) {
		L().Warn("web UI is binding to a non-loopback address and will be reachable from other hosts; configure GOROKU_TRUSTED_PROXIES with the CIDRs of your reverse proxy before trusting forwarding headers",
			zap.String("bind", bindHost),
			zap.String("addr", net.JoinHostPort(bindHost, strconv.Itoa(runningPort))))
	}
	server := newHTTPServer(net.JoinHostPort(bindHost, strconv.Itoa(runningPort)), secureHandler)
	var proxypasser *TunnelManager
	if proxyPass {
		proxypasser = NewProxyPasser(runningPort, func(url string) {
			wc.serverMu.Lock()
			if wc.state == webRunning && wc.proxypasser == proxypasser {
				wc.url = url
			}
			wc.serverMu.Unlock()
		}, false)
	}

	listener, err := wc.listen(ctx, "tcp", server.Addr)
	wc.serverMu.Lock()
	if err != nil || wc.state != webStarting || ctx.Err() != nil {
		wc.serverMu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
		if err != nil && ctx.Err() == nil {
			L().Error("Web server error", zap.Error(err))
		}

		wc.serverMu.Lock()
		if wc.state == webStarting {
			if err != nil && ctx.Err() == nil {
				wc.lifecycleErr = fmt.Errorf("listen: %w", err)
				generation.err = wc.lifecycleErr
			} else {
				generation.err = context.Canceled
			}
			wc.stopAuth()
			wc.running = false
			wc.ready = false
			wc.proxyPass = false
			wc.server = nil
			wc.proxypasser = nil
			wc.startCancel = nil
			wc.startDone = nil
			wc.startReady = nil
			wc.startGeneration = nil
			wc.state = webStopped
		} else {
			generation.err = context.Canceled
		}
		close(done)
		close(generation.done)
		wc.serverMu.Unlock()
		return
	}
	wc.server = server
	wc.port = runningPort
	wc.proxypasser = proxypasser
	wc.running = true
	wc.state = webRunning
	generation.running = true
	close(generation.done)
	wc.serverMu.Unlock()

	L().Info("Goroku Userbot Web Interface running on", zap.Any("running_port", runningPort))
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		wc.serverMu.Lock()
		wc.lifecycleErr = fmt.Errorf("serve: %w", err)
		wc.serverMu.Unlock()
		L().Error("Web server error", zap.Error(err))
	}
	wc.finishStart(done)
}

func (wc *WebCore) finishStart(done chan struct{}) {
	close(done)

	wc.serverMu.Lock()
	if wc.state != webRunning {
		wc.serverMu.Unlock()
		return
	}
	wc.state = webStopping
	wc.stopDone = make(chan struct{})
	stopDone := wc.stopDone
	proxypasser := wc.proxypasser
	wc.serverMu.Unlock()

	if proxypasser != nil {
		proxypasser.Stop()
	}
	wc.finishStop(stopDone)
}

func (wc *WebCore) finishStop(done chan struct{}) {
	wc.serverMu.Lock()
	wc.stopAuth()
	wc.running = false
	wc.ready = false
	wc.proxyPass = false
	wc.server = nil
	wc.proxypasser = nil
	wc.startCancel = nil
	wc.startDone = nil
	wc.startReady = nil
	wc.startGeneration = nil
	wc.stopDone = nil
	wc.state = webStopped
	close(done)
	wc.serverMu.Unlock()
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      75 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func isLoopbackBind(host string) bool {
	h := strings.Trim(strings.TrimSpace(host), "[]")
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(h, "localhost")
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// R4.1: CSP is offline-only. All frontend assets are vendored and embedded
	// (goroku/web/assets via //go:embed), so every origin except 'self' is
	// disallowed. 'unsafe-inline' is retained for script/style because the
	// onboarding markup embeds a small inline bootstrap script and inline
	// styles; data: is allowed for images/fonts used by the panel.
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"font-src 'self' data:",
		"img-src 'self' data:",
		"connect-src 'self'",
		"media-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"frame-ancestors 'none'",
	}, "; "))
}

// Stop preserves the historical blocking API with a bounded internal timeout.
func (wc *WebCore) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wc.Close(ctx); err != nil {
		L().Warn("web server shutdown failed", zap.Error(err))
	}
}

// Close stops HTTP/auth intake and waits for startup and shutdown completion.
// A caller timeout only stops waiting; component-owned teardown continues.
func (wc *WebCore) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	wc.serverMu.Lock()
	wc.stopAuth()
	switch wc.state {
	case webStopped:
		err := wc.lifecycleErr
		wc.serverMu.Unlock()
		return err
	case webStopping:
		done := wc.stopDone
		wc.serverMu.Unlock()
		return wc.waitForStop(ctx, done)
	}

	wc.state = webStopping
	wc.stopDone = make(chan struct{})
	stopDone := wc.stopDone
	cancel := wc.startCancel
	startDone := wc.startDone
	server := wc.server
	proxypasser := wc.proxypasser
	wc.serverMu.Unlock()
	if cancel != nil {
		cancel()
	}
	go wc.completeStop(server, proxypasser, startDone, stopDone)
	return wc.waitForStop(ctx, stopDone)
}

func (wc *WebCore) completeStop(server *http.Server, proxypasser *TunnelManager, startDone, stopDone chan struct{}) {
	if proxypasser != nil {
		proxypasser.Stop()
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := server.Shutdown(ctx)
		cancel()
		if err != nil {
			err = errors.Join(err, server.Close())
			wc.serverMu.Lock()
			wc.lifecycleErr = errors.Join(wc.lifecycleErr, fmt.Errorf("shutdown: %w", err))
			wc.serverMu.Unlock()
		}
	}
	if startDone != nil {
		<-startDone
	}
	wc.finishStop(stopDone)
}

func (wc *WebCore) waitForStop(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		wc.serverMu.RLock()
		err := wc.lifecycleErr
		wc.serverMu.RUnlock()
		return err
	default:
	}
	select {
	case <-done:
		wc.serverMu.RLock()
		err := wc.lifecycleErr
		wc.serverMu.RUnlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
