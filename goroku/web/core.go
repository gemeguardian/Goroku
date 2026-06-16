package web

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

const DefaultFallbackTGID = 123456789

type WebCore struct {
	*Web
	server      *http.Server
	port        int
	running     bool
	ready       bool
	clientData  map[int64][]interface{}
	proxypasser *ProxyPasser
	url         string
	mu          sync.Mutex
}

var Instance *WebCore

func NewWebCore(cfg WebConfig) *WebCore {
	wc := &WebCore{
		Web:        NewWeb(cfg),
		clientData: make(map[int64][]interface{}),
	}
	Instance = wc
	return wc
}

func (wc *WebCore) StartIfReady(totalCount int, port int, proxyPass bool) {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	if totalCount <= len(wc.clientData) {
		if !wc.running {
			go wc.Start(port, proxyPass)
		}
		wc.ready = true
	}
}

func (wc *WebCore) GetURL(proxyPass bool) string {
	if os.Getenv("LAVHOST") != "" && os.Getenv("USER") != "" && os.Getenv("SERVER") != "" {
		return fmt.Sprintf("https://%s.%s.lavhost.ml", os.Getenv("USER"), os.Getenv("SERVER"))
	}

	if proxyPass && wc.proxypasser != nil {
		url := wc.proxypasser.GetURL(10 * time.Second)
		if url != "" {
			wc.url = url
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

	wc.url = fmt.Sprintf("http://%s:%d", ip, wc.port)
	return wc.url
}

func (wc *WebCore) SetPort(port int) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.port == 0 {
		wc.port = port
	}
}

func (wc *WebCore) Start(port int, proxyPass bool) {
	wc.mu.Lock()
	if wc.running {
		wc.mu.Unlock()
		return
	}
	wc.port = port
	if envPort := os.Getenv("PORT"); envPort != "" {
		fmt.Sscanf(envPort, "%d", &wc.port)
	}

	mux := http.NewServeMux()
	wc.SetupRoutes(mux)

	// Add favicon and static resources
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/IRAiWBo.jpeg", http.StatusMovedPermanently)
	})

	// Setup static files handler
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(wc.dataRoot, "web-resources/static")))))

	wc.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", wc.port),
		Handler: mux,
	}

	wc.proxypasser = NewProxyPasser(wc.port, func(url string) {
		wc.mu.Lock()
		wc.url = url
		wc.mu.Unlock()
	}, false)

	wc.running = true
	wc.mu.Unlock()

	log.Printf("Goroku Userbot Web Interface running on %d\n", wc.port)
	if err := wc.server.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("Web server error: %v\n", err)
	}
}

func (wc *WebCore) Stop() {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	if !wc.running {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if wc.server != nil {
		wc.server.Shutdown(ctx)
	}

	wc.running = false
	wc.ready = false
}

func (wc *WebCore) AddLoader(client interface{}, loader interface{}, db interface{}) {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	var id int64 = DefaultFallbackTGID
	if client != nil {
		v := reflect.ValueOf(client)
		if v.Kind() == reflect.Ptr {
			v = v.Elem()
		}
		if v.Kind() == reflect.Struct {
			f := v.FieldByName("TGID")
			if f.IsValid() && f.Kind() == reflect.Int64 {
				id = f.Int()
			}
		}
	}
	wc.clientData[id] = []interface{}{loader, client, db}
	wc.Web.clientData[id] = []interface{}{loader, client, db}
}
