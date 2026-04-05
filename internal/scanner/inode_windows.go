//go:build windows

package scanner

import "io/fs"

func getInode(info fs.FileInfo) uint64 {
	return 0
}
