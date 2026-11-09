//go:build !windows

package session

import (
	"os/exec"
	"syscall"
)

// configureProcess 让命令自成一个进程组，超时或停止时整组一起杀。
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree 杀掉命令和它的全部子进程。
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
