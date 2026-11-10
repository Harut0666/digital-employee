//go:build windows

package session

import (
	"os/exec"
	"strconv"
	"syscall"
)

// configureProcess 不给命令弹出控制台窗口。
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// killProcessTree 杀掉命令和它的全部子进程（taskkill /T 按进程树杀）。
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	_ = kill.Run()
	_ = cmd.Process.Kill()
}
