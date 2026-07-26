//go:build !(linux || android)

package goroku

import (
	"fmt"
	"runtime"
)

func doctorDiskUsage(path string) string {
	return fmt.Sprintf("unsupported on %s", runtime.GOOS)
}
