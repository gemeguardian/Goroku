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
	//nolint:gosec // syscall.Statfs fields are unsigned on Linux; cast is safe
	all := uint64(stat.Blocks) * uint64(stat.Bsize)
	free := uint64(stat.Bfree) * uint64(stat.Bsize) //nolint:gosec
	used := all - free
	return fmt.Sprintf("%.1f/%.1f GB", float64(used)/1e9, float64(all)/1e9)
}
