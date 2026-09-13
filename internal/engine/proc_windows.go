//go:build windows

package engine

import (
	"os"
	"os/exec"
)

// setProcessGroup Windows 下无进程组语义，直接依赖 Kill。
func setProcessGroup(cmd *exec.Cmd) {}

// CancelProcessGroup Windows 下终止单进程。
func CancelProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
