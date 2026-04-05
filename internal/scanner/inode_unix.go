//go:build !windows

package scanner

import (
	"io/fs"
	"syscall"
)

func getInode(info fs.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}
