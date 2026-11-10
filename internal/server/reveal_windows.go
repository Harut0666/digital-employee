//go:build windows

package server

import (
	"os/exec"
	"syscall"
)

// revealCommand 在资源管理器里显示并选中文件。explorer 的 /select, 参数不能被 Go 默认的引号规则包起来
// （路径里有空格时会退回到“文档”文件夹），所以直接给命令行。
func revealCommand(path string) *exec.Cmd {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return cmd
}
