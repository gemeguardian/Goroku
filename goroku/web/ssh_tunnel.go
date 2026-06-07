package web

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type SSHTunnel struct {
	port               int
	changeURLCallback  func(string)
	tunnelURL          string
	urlAvailable       chan struct{}
	process            *exec.Cmd
	currentCmdIndex    int
	sshCommands        [][]string
	allCommandsFailed  bool
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
}

func NewSSHTunnel(port int, changeURLCallback func(string)) *SSHTunnel {
	commands := [][]string{
		{
			fmt.Sprintf("ssh -o StrictHostKeyChecking=no -R 80:127.0.0.1:%d serveo.net -T -n", port),
			`https://(\S*serveo\.net\S*)`,
		},
		{
			fmt.Sprintf("ssh -o StrictHostKeyChecking=no -R 80:127.0.0.1:%d nokey@localhost.run", port),
			`https://(\S*lhr\.life\S*)`,
		},
	}

	return &SSHTunnel{
		port:              port,
		changeURLCallback: changeURLCallback,
		urlAvailable:      make(chan struct{}),
		sshCommands:       commands,
	}
}

func (s *SSHTunnel) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.runSSHTunnel()
}

func (s *SSHTunnel) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}

	if s.process != nil && s.process.Process != nil {
		log.Println("Stopping SSH tunnel...")
		s.process.Process.Kill()
		s.process = nil
	}
}

func (s *SSHTunnel) WaitForURL(timeout time.Duration) string {
	select {
	case <-s.urlAvailable:
		return s.tunnelURL
	case <-time.After(timeout):
		log.Println("Timeout waiting for tunnel URL.")
		return ""
	}
}

func (s *SSHTunnel) runSSHTunnel() {
	for s.currentCmdIndex < len(s.sshCommands) {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		cmdInfo := s.sshCommands[s.currentCmdIndex]
		sshCmdStr := cmdInfo[0]
		pattern := cmdInfo[1]

		log.Printf("Attempting SSH command: %s with pattern: %s\n", sshCmdStr, pattern)

		parts := strings.Fields(sshCmdStr)
		cmd := exec.CommandContext(s.ctx, parts[0], parts[1:]...)
		s.mu.Lock()
		s.process = cmd
		s.mu.Unlock()

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("Failed to get stdout pipe: %v\n", err)
			s.currentCmdIndex++
			continue
		}

		if err := cmd.Start(); err != nil {
			log.Printf("Failed to start SSH tunnel process: %v\n", err)
			s.currentCmdIndex++
			continue
		}

		// Regex matching output
		rx := regexp.MustCompile(pattern)
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

					// Signal URL available (non-blocking if already closed)
					select {
					case <-s.urlAvailable:
					default:
						close(s.urlAvailable)
					}
				}
			}
		}()

		cmd.Wait()

		s.mu.Lock()
		urlObtained := s.tunnelURL != ""
		s.mu.Unlock()

		if urlObtained {
			log.Println("SSH tunnel disconnected, but URL was obtained. Exiting SSH Tunnel attempts.")
			return
		}

		log.Println("Reconnecting SSH tunnel after failure...")
		s.currentCmdIndex++
		time.Sleep(2 * time.Second)
	}

	s.mu.Lock()
	s.allCommandsFailed = true
	if s.tunnelURL == "" {
		log.Println("All SSH commands failed.")
		select {
		case <-s.urlAvailable:
		default:
			close(s.urlAvailable)
		}
	}
	s.mu.Unlock()
}
