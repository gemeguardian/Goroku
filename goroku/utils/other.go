package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"time"
)



// Rand returns a random alphanumeric string of the specified size.
func Rand(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz1234567890"
	b := make([]byte, size)
	for i := range b {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			b[i] = charset[0]
		} else {
			b[i] = charset[num.Int64()]
		}
	}
	return string(b)
}

// InviteInlineBot is a stub representing invite_inline_bot logic
func InviteInlineBot(client interface{}, peer interface{}) error {
	// Stub implementation matching Python's invite_inline_bot
	return nil
}

// RunSync is a helper representing python's run_sync
func RunSync(funcObj interface{}, args ...interface{}) interface{} {
	// In Go, concurrency is native. This is a stub/placeholder helper.
	return nil
}

// RunAsync is a helper representing python's run_async
func RunAsync(loop interface{}, coro interface{}) interface{} {
	return nil
}

// Merge merges map a into map b recursively.
func Merge(a, b map[string]interface{}, deep bool) map[string]interface{} {
	if b == nil {
		b = make(map[string]interface{})
	}
	for key, aVal := range a {
		bVal, ok := b[key]
		if !ok {
			b[key] = aVal
			continue
		}

		aMap, aIsMap := aVal.(map[string]interface{})
		bMap, bIsMap := bVal.(map[string]interface{})

		if aIsMap && bIsMap && deep {
			b[key] = Merge(aMap, bMap, deep)
		} else {
			b[key] = aVal
		}
	}
	return b
}

// Chunks splits a slice into chunks of size n.
func Chunks(list []interface{}, n int) [][]interface{} {
	if n <= 0 {
		return [][]interface{}{list}
	}
	var chunks [][]interface{}
	for i := 0; i < len(list); i += n {
		end := i + n
		if end > len(list) {
			end = len(list)
		}
		chunks = append(chunks, list[i:end])
	}
	return chunks
}

// AtExit registers a function to run at exit.
func AtExit(funcObj interface{}, useSignal int, args ...interface{}) {
	// Stub/Placeholder for Python's atexit
}

// CopyTL is a stub for Python's _copy_tl
func CopyTL(o interface{}, kwargs map[string]interface{}) interface{} {
	return o
}

// FormatFileSize formats file size in bytes to a human-readable string.
func FormatFileSize(sizeBytes int64) string {
	if sizeBytes == 0 {
		return "0 B"
	}
	sizeNames := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	val := float64(sizeBytes)
	for val >= 1024 && i < len(sizeNames)-1 {
		val /= 1024.0
		i++
	}
	return fmt.Sprintf("%.1f %s", val, sizeNames[i])
}

// IsURL statically checks if a string is a valid URL.
func IsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && u.Host != ""
}

// GetISOTime returns the current UTC time in ISO 8601 format.
func GetISOTime() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// SafeGetattr safely gets a field from a structure. Stub for python reflection.
func SafeGetattr(obj interface{}, attr string, defaultValue interface{}) interface{} {
	return defaultValue
}

// Helper to compile regex matching python patterns if needed
var urlPattern = regexp.MustCompile(`(?i)^https?://(?:(?:[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?\.)+[A-Z]{2,6}\.?|localhost|\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})(?::\d+)?(?:/?|[/?]\S+)$`)

func IsURLRegex(s string) bool {
	return urlPattern.MatchString(s)
}
