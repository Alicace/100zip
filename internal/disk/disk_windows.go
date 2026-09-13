//go:build windows

package disk

import "errors"

// Windows 构建不依赖平台专用 API；NAS 目标为 Linux，桌面开发时返回未知。
func FreeBytes(path string) (uint64, error) { return 0, errors.New("disk free space unavailable") }
