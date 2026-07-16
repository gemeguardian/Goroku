package web

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"
)

var _ = zap.NewNop

// TunnelProvider describes a single SSH-based reverse-tunnel backend.
type TunnelProvider struct {
	Name    string
	Args    []string // Arguments for ssh; {port} is replaced with the local port.
	Pattern string   // Regex with one capture group that extracts the public URL.
}

// DefaultTunnelProviders are the built-in reverse-tunnel services.
// They can be overridden by callers to avoid hard-coding third-party hosts.
var DefaultTunnelProviders = []TunnelProvider{
	{
		Name: "serveo",
		Args: []string{
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "UserKnownHostsFile=/dev/null",
			"-R", "80:127.0.0.1:{port}",
			"serveo.net",
			"-T", "-n",
		},
		Pattern: `https://(\S*serveo\.net\S*)`,
	},
	{
		Name: "localhostrun",
		Args: []string{
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "UserKnownHostsFile=/dev/null",
			"-R", "80:127.0.0.1:{port}",
			"nokey@localhost.run",
			"-T", "-n",
		},
		Pattern: `https://(\S*lhr\.life\S*)`,
	},
}

type SSHTunnel struct {
	port              int
	changeURLCallback func(string)
	tunnelURL         string
	urlOnce           sync.Once
	urlAvailable      chan struct{}
	process           *exec.Cmd
	providers         []TunnelProvider
	allCommandsFailed bool
	mu                sync.Mutex
	cancel            context.CancelFunc
	done              chan struct{}
}

// NewSSHTunnel creates a tunnel that tries the built-in providers in order.
func NewSSHTunnel(port int, changeURLCallback func(string)) *SSHTunnel {
	return NewSSHTunnelWithProviders(port, changeURLCallback, DefaultTunnelProviders)
}

// NewSSHTunnelWithProviders allows callers to supply their own tunnel providers.
func NewSSHTunnelWithProviders(port int, changeURLCallback func(string), providers []TunnelProvider) *SSHTunnel {
	return &SSHTunnel{
		port:              port,
		changeURLCallback: changeURLCallback,
		urlAvailable:      make(chan struct{}),
		providers:         providers,
	}
}

func (s *SSHTunnel) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	go func() {
		s.runSSHTunnel(ctx)
		s.mu.Lock()
		if s.done == done {
			s.cancel = nil
			s.done = nil
			s.process = nil
		}
		s.mu.Unlock()
		close(done)
	}()
}

func (s *SSHTunnel) Stop() {
	s.mu.Lock()
	proc := s.process
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if proc != nil && proc.Process != nil {
		L().Info("Stopping SSH tunnel process", zap.Int("pid", proc.Process.Pid))
		_ = proc.Process.Kill()

	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			L().Warn("SSH tunnel process did not exit within 5s")
		}
	}
}

func (s *SSHTunnel) WaitForURL(timeout time.Duration) string {
	select {
	case <-s.urlAvailable:
		s.mu.Lock()
		url := s.tunnelURL
		s.mu.Unlock()
		return url
	case <-time.After(timeout):
		L().Info("Timeout waiting for tunnel URL")
		return ""
	}
}

func (s *SSHTunnel) markURLAvailable() {
	s.urlOnce.Do(func() { close(s.urlAvailable) })
}

func (s *SSHTunnel) runSSHTunnel(ctx context.Context) {
	for cmdIndex := 0; cmdIndex < len(s.providers); cmdIndex++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		provider := s.providers[cmdIndex]
		rx, err := regexp.Compile(provider.Pattern)
		if err != nil {
			L().Error("Invalid tunnel URL regex", zap.String("provider", provider.Name), zap.Error(err))
			continue
		}

		args := make([]string, len(provider.Args))
		// Rebuild args safely, replacing the {port} placeholder.
		for i, a := range provider.Args {
			args[i] = regexp.MustCompile(`\{port\}`).ReplaceAllString(a, fmt.Sprintf("%d", s.port))
		}

		L().Info("Attempting SSH tunnel", zap.String("provider", provider.Name), zap.Strings("args", args))

		cmd := exec.CommandContext(ctx, "ssh", args...) //nolint:gosec
		s.mu.Lock()
		s.process = cmd
		s.mu.Unlock()

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			L().Error("Failed to get stdout pipe", zap.String("provider", provider.Name), zap.Error(err))
			continue
		}

		if err := cmd.Start(); err != nil {
			L().Error("Failed to start SSH tunnel process", zap.String("provider", provider.Name), zap.Error(err))
			continue
		}

		scanner := bufio.NewScanner(stdout)
		go func() {
			for scanner.Scan() {
				line := scanner.Text()
				matches := rx.FindStringSubmatch(line)
				if len(matches) > 0 {
					s.mu.Lock()
					s.tunnelURL = matches[0]
					if s.changeURLCallback != nil {
						s.changeURLCallback(s.tunnelURL)
					}
					s.mu.Unlock()
					s.markURLAvailable()
				}
			}
		}()

		_ = cmd.Wait()

		s.mu.Lock()
		urlObtained := s.tunnelURL != ""
		s.mu.Unlock()

		if urlObtained {
			L().Info("SSH tunnel disconnected, but URL was obtained. Exiting SSH Tunnel attempts.")
			return
		}

		L().Info("SSH tunnel attempt failed, trying next provider", zap.String("provider", provider.Name))
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}

	s.mu.Lock()
	s.allCommandsFailed = true
	if s.tunnelURL == "" {
		L().Info("All SSH tunnel providers failed")
		s.markURLAvailable()
	}
	s.mu.Unlock()
}
