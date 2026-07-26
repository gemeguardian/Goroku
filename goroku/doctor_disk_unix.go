//go:build linux || android

package goroku

import (
	"fmt"
	"syscall"
)

func doctorDiskUsage(path string) string {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	all := uint64(stat.Blocks) * uint64(stat.Bsize) //nolint:gosec
	free := uint64(stat.Bfree) * uint64(stat.Bsize) //nolint:gosec
	used := all - free
	if all == 0 {
		return "0/0 B"
	}
	return fmt.Sprintf("%.1f/%.1f GB (%.1f%% free)", float64(used)/1e9, float64(all)/1e9, float64(free)/float64(all)*100)
}
