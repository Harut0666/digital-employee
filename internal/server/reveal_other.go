//go:build !windows

package server

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// revealCommand 在文件管理器里显示文件：macOS 能选中，其它系统只能打开所在文件夹。
func revealCommand(path string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path)
	}
	return exec.Command("xdg-open", filepath.Dir(path))
}
