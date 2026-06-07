//go:build linux || android
// +build linux android

package utils

import (
	"fmt"
	"syscall"
)

func GetDiskUsage() string {
	var stat syscall.Statfs_t
	err := syscall.Statfs("/", &stat)
	if err != nil {
		return "unknown"
	}
	all := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := all - free
	return fmt.Sprintf("%.1f/%.1f GB", float64(used)/1e9, float64(all)/1e9)
}
