package utils

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

var initTime = time.Now()

func Uptime() int64 {
	return int64(time.Since(initTime).Seconds())
}

func FormattedUptime() string {
	seconds := Uptime()
	days := seconds / 86400
	remainder := seconds % 86400

	hours := remainder / 3600
	remainder = remainder % 3600
	minutes := remainder / 60
	secs := remainder % 60

	if days > 0 {
		return fmt.Sprintf("%d day(s), %02d:%02d:%02d", days, hours, minutes, secs)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
}

func GetRAMUsage() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// Convert bytes to MB
	mb := float64(m.Alloc) / (1024 * 1024)
	return mb
}

func GetCPUUsage() string {
	// Simple placeholder, in Go CPU tracking is OS dependent
	return "0.00"
}

func GetPlatformName() string {
	if os.Getenv("DOCKER") != "" {
		return "Docker"
	}
	if os.Getenv("LAVHOST") != "" {
		return "LavHost"
	}
	return runtime.GOOS
}

func GetPlatformEmoji() string {
	if runtime.GOOS == "linux" {
		return "🐧"
	}
	return "💎"
}

func GetGoPath() string {
	for _, path := range []string{"/usr/local/go/bin/go", "/usr/bin/go"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "go"
}
