//go:build !windows

package engine

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让取消时能杀掉整棵进程树（Unix）。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// CancelProcessGroup 在 Unix 上终止整个进程组。
func CancelProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}
