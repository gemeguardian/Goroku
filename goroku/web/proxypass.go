package web

import (
	"log"
	"os"
	"time"
)

type ProxyPasser struct {
	tunnelURL         string
	port              int
	changeURLCallback func(string)
	verbose           bool
	tunnels           []*SSHTunnel
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
	p.tunnelURL = url
	if p.changeURLCallback != nil {
		p.changeURLCallback(url)
	}
}

func (p *ProxyPasser) SetPort(port int) {
	p.port = port
}

func (p *ProxyPasser) GetURL(timeout time.Duration) string {
	if os.Getenv("DOCKER") != "" {
		return ""
	}

	for _, tunnel := range p.tunnels {
		tunnel.Start()
		url := tunnel.WaitForURL(timeout)
		if url != "" {
			return url
		}
		log.Println("Tunnel failed to provide URL.")
	}

	return ""
}
