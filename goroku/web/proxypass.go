package web

import (
	"log"
	"os"
	"sync"
	"time"
)

type ProxyPasser struct {
	mu                sync.RWMutex
	tunnelURL         string
	port              int
	changeURLCallback func(string)
	verbose           bool
	tunnels           []*SSHTunnel
	stopped           bool
	startWG           sync.WaitGroup
}

func NewProxyPasser(port int, changeURLCallback func(string), verbose bool) *ProxyPasser {
	p := &ProxyPasser{
		port:              port,
		changeURLCallback: changeURLCallback,
		verbose:           verbose,
	}
	p.tunnels = []*SSHTunnel{
		NewSSHTunnel(port, p.onURLChange),
	}
	return p
}

func (p *ProxyPasser) onURLChange(url string) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.tunnelURL = url
	p.mu.Unlock()
	if p.changeURLCallback != nil {
		p.changeURLCallback(url)
	}
}

func (p *ProxyPasser) SetPort(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.port = port
}

func (p *ProxyPasser) GetURL(timeout time.Duration) string {
	if os.Getenv("DOCKER") != "" {
		return ""
	}

	for _, tunnel := range p.tunnels {
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return ""
		}
		p.startWG.Add(1)
		p.mu.Unlock()
		tunnel.Start()
		p.startWG.Done()
		url := tunnel.WaitForURL(timeout)
		if url != "" {
			return url
		}
		log.Println("Tunnel failed to provide URL.")
	}

	return ""
}

func (p *ProxyPasser) Stop() {
	p.mu.Lock()
	p.stopped = true
	tunnels := append([]*SSHTunnel(nil), p.tunnels...)
	p.mu.Unlock()
	p.startWG.Wait()
	for _, tunnel := range tunnels {
		tunnel.Stop()
	}
}
