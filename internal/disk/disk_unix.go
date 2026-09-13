//go:build !windows

package disk

import (
	"os"
	"path/filepath"
	"syscall"
)

// FreeBytes 返回 path 所在文件系统的可用字节数。
func FreeBytes(path string) (uint64, error) {
	p := path
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		p = filepath.Dir(p)
	}
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err == nil {
			return uint64(st.Bavail) * uint64(st.Bsize), nil
		}
		next := filepath.Dir(p)
		if next == p {
			return 0, os.ErrNotExist
		}
		p = next
	}
}
