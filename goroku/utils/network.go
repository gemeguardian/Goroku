package utils

import (
	"fmt"
	"net"
	"os"
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
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
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
