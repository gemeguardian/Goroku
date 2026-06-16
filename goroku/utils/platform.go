package utils

import (
	"fmt"
	"os"
	"runtime"
	"strings"
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
	// 1. Check for Raspberry / Orange Pi
	if content, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		model := strings.TrimSpace(string(content))
		if strings.Contains(model, "Orange") || strings.Contains(model, "Raspberry") {
			return model
		}
	}

	// 2. Check env / runtime
	if os.Getenv("DOCKER") != "" {
		return "Docker"
	}
	if strings.Contains(os.Getenv("USER"), "userland") {
		return "UserLand"
	}
	if strings.Contains(os.Getenv("PATH"), "com.apple") {
		return "MacOS"
	}

	if runtime.GOOS == "windows" {
		return "Windows"
	}
	if runtime.GOOS == "darwin" {
		return "MacOS"
	}

	// Check WSL
	if content, err := os.ReadFile("/proc/version"); err == nil {
		if strings.Contains(strings.ToLower(string(content)), "microsoft") {
			return "WSL"
		}
	}

	if runtime.GOOS == "linux" {
		return "VDS"
	}

	return runtime.GOOS
}

func GetPlatformEmoji() string {
	baseTemplate := `<tg-emoji emoji-id="%d">🪐</tg-emoji><tg-emoji emoji-id="5352934134618549768">🪐</tg-emoji><tg-emoji emoji-id="5352663371290271790">🪐</tg-emoji><tg-emoji emoji-id="5350822883314655367">🪐</tg-emoji>`

	var emojiID int64
	if strings.Contains(os.Getenv("USER"), "userland") {
		emojiID = 5458877818031077824
	} else if os.Getenv("DOCKER") != "" {
		emojiID = 5352678227582152630
	} else {
		emojiID = 5393588431026674882
	}

	return fmt.Sprintf(baseTemplate, emojiID)
}

func GetNamedPlatformEmoji() string {
	// 1. Check for Raspberry / Orange Pi
	if content, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		model := string(content)
		if strings.Contains(model, "Orange") {
			return "🍊 "
		}
		if strings.Contains(model, "Raspberry") {
			return "🍇 "
		}
		return "?"
	}

	if content, err := os.ReadFile("/proc/version"); err == nil {
		if strings.Contains(strings.ToLower(string(content)), "microsoft") {
			return "🍀 "
		}
	}

	if os.Getenv("DOCKER") != "" {
		return "🐳 "
	}
	if strings.Contains(os.Getenv("USER"), "userland") {
		return "🐧 "
	}
	if strings.Contains(os.Getenv("PATH"), "com.apple") {
		return "🍏 "
	}

	if runtime.GOOS == "windows" {
		return "💻 "
	}
	if runtime.GOOS == "darwin" {
		return "🍏 "
	}

	if runtime.GOOS == "linux" {
		return "💎 "
	}

	return "? "
}

func GetGoPath() string {
	for _, path := range []string{"/usr/local/go/bin/go", "/usr/bin/go"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "go"
}
