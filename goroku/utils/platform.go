package utils

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
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
	if rss, err := processRSSFromStatus(mustReadFile("/proc/self/status")); err == nil {
		return float64(rss) / 1024
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Sys) / (1024 * 1024)
}

func GetCPUUsage() string {
	startTicks, err := processCPUTicksFromStat(mustReadFile("/proc/self/stat"))
	if err != nil {
		return "n/a"
	}
	start := time.Now()
	time.Sleep(200 * time.Millisecond)
	endTicks, err := processCPUTicksFromStat(mustReadFile("/proc/self/stat"))
	if err != nil || endTicks < startTicks {
		return "n/a"
	}
	// Linux exposes /proc process time in USER_HZ, fixed at 100 ticks per second.
	percent := float64(endTicks-startTicks) / 100 / time.Since(start).Seconds() * 100
	return fmt.Sprintf("%.2f", percent)
}

func mustReadFile(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

func processRSSFromStatus(data []byte) (uint64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "VmRSS:" || fields[2] != "kB" {
			continue
		}
		return strconv.ParseUint(fields[1], 10, 64)
	}
	return 0, fmt.Errorf("VmRSS is unavailable")
}

func processCPUTicksFromStat(data []byte) (uint64, error) {
	closeName := strings.LastIndex(string(data), ")")
	if closeName == -1 {
		return 0, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data[closeName+1:]))
	if len(fields) < 13 {
		return 0, fmt.Errorf("process stat is incomplete")
	}
	userTicks, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, err
	}
	systemTicks, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, err
	}
	return userTicks + systemTicks, nil
}

func GetPlatformName() string {
	if hostVar := os.Getenv("HOST_VAR"); hostVar != "" {
		return hostVar
	}

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
	if os.Getenv("TERMUX_VERSION") != "" {
		return "Termux"
	}
	if os.Getenv("RAILWAY_ENVIRONMENT") != "" {
		return "Railway"
	}
	if os.Getenv("RENDER") != "" {
		return "Render"
	}
	if os.Getenv("FLY_ALLOC_ID") != "" {
		return "Fly.io"
	}
	if os.Getenv("PTERODACTYL") != "" {
		return "Pterodactyl"
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
	if runtime.GOOS == "freebsd" {
		return "FreeBSD"
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
	if strings.Contains(os.Getenv("USER"), "userland") || os.Getenv("TERMUX_VERSION") != "" {
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
	if os.Getenv("TERMUX_VERSION") != "" {
		return "📱 "
	}
	if os.Getenv("RAILWAY_ENVIRONMENT") != "" {
		return "🚂 "
	}
	if os.Getenv("RENDER") != "" {
		return "🎨 "
	}
	if os.Getenv("FLY_ALLOC_ID") != "" {
		return "🎈 "
	}
	if os.Getenv("PTERODACTYL") != "" {
		return "🦖 "
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
	if runtime.GOOS == "freebsd" {
		return "😈 "
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
