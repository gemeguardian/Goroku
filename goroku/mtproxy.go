package goroku

import (
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/gotd/td/mtproxy"
	"github.com/gotd/td/telegram/dcs"
)

// MTProxyConfig is a complete MTProto proxy endpoint (Telegram MTProxy).
type MTProxyConfig struct {
	Host   string
	Port   int
	Secret string // hex-encoded secret (optional 0x prefix)
}

var (
	mtProxyMu     sync.RWMutex
	mtProxyActive *MTProxyConfig
)

// NormalizeProxySecret picks --proxy-secret, falling back to --proxy-pass.
func NormalizeProxySecret(secret, pass string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		s = strings.TrimSpace(pass)
	}
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	return s
}

// ValidateMTProxyConfig requires host+port+secret together when any field is set.
func ValidateMTProxyConfig(host string, port int, secret string) error {
	host = strings.TrimSpace(host)
	secret = strings.TrimSpace(secret)
	anySet := host != "" || port != 0 || secret != ""
	if !anySet {
		return nil
	}
	if host == "" {
		return fmt.Errorf("--proxy-host is required when MTProto proxy is configured")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("--proxy-port must be between 1 and 65535")
	}
	if secret == "" {
		return fmt.Errorf("--proxy-secret (or --proxy-pass) is required when MTProto proxy is configured")
	}
	raw, err := hex.DecodeString(secret)
	if err != nil {
		return fmt.Errorf("--proxy-secret must be hex-encoded: %w", err)
	}
	if _, err := mtproxy.ParseSecret(raw); err != nil {
		return fmt.Errorf("invalid MTProto proxy secret: %w", err)
	}
	return nil
}

// ConfigureMTProxy installs process-wide MTProxy settings used by ConnectContext.
// Pass a zero config to clear.
func ConfigureMTProxy(cfg MTProxyConfig) error {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Secret = NormalizeProxySecret(cfg.Secret, "")
	if err := ValidateMTProxyConfig(cfg.Host, cfg.Port, cfg.Secret); err != nil {
		return err
	}
	mtProxyMu.Lock()
	defer mtProxyMu.Unlock()
	if cfg.Host == "" {
		mtProxyActive = nil
		return nil
	}
	cp := cfg
	mtProxyActive = &cp
	return nil
}

// ActiveMTProxy returns the configured proxy, if any.
func ActiveMTProxy() (MTProxyConfig, bool) {
	mtProxyMu.RLock()
	defer mtProxyMu.RUnlock()
	if mtProxyActive == nil {
		return MTProxyConfig{}, false
	}
	return *mtProxyActive, true
}

func mtProxyResolver() (dcs.Resolver, bool, error) {
	cfg, ok := ActiveMTProxy()
	if !ok {
		return nil, false, nil
	}
	raw, err := hex.DecodeString(cfg.Secret)
	if err != nil {
		return nil, true, fmt.Errorf("decode proxy secret: %w", err)
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	resolver, err := dcs.MTProxy(addr, raw, dcs.MTProxyOptions{})
	if err != nil {
		return nil, true, fmt.Errorf("create MTProxy resolver: %w", err)
	}
	return resolver, true, nil
}
