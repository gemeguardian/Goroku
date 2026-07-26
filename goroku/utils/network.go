package utils

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

func GetHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "Unknown"
	}
	return name
}

func ResolveDomain(domain string) string {
	ips, err := net.LookupIP(domain)
	if err != nil || len(ips) == 0 {
		return "Unable to resolve"
	}
	return ips[0].String()
}

func IsPortOpen(host string, port int) bool {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", address, 1*time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	return true
}

func GetNetworkInterfaces() map[string]string {
	res := make(map[string]string)
	interfaces, err := net.Interfaces()
	if err != nil {
		return res
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil {
					res[iface.Name] = ipNet.IP.String()
					break
				}
			}
		}
	}
	return res
}

// GetIPAddress returns the first non-loopback IPv4 address of the machine.
func GetIPAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "Unknown"
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil {
					return ipNet.IP.String()
				}
			}
		}
	}
	return "Unknown"
}

func DownloadURLLimited(client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsed.Scheme)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %d", resp.StatusCode)
	}
	if maxBytes > 0 && resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("remote file too large: %d bytes", resp.ContentLength)
	}

	return ReadResponseBodyLimited(resp, maxBytes)
}

func ReadResponseBodyLimited(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("empty HTTP response")
	}
	if maxBytes > 0 && resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("remote file too large: %d bytes", resp.ContentLength)
	}

	reader := resp.Body
	if maxBytes > 0 {
		reader = io.NopCloser(io.LimitReader(resp.Body, maxBytes+1))
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("remote file exceeds %d bytes", maxBytes)
	}
	return body, nil
}
