//go:build windows && !de_stub

package server

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

// pickFolderScript 在子进程里弹出对话框的 PowerShell 脚本。
//
// 为什么用子进程而不是在本进程里直接调 COM：对话框要在 STA 线程上跑自己的消息循环，
// 接口方法表写错一位就是整个服务进程的访问冲突；放进 powershell.exe -STA 里，最坏也只是这一次选择失败。
// 脚本优先用现代的 IFileOpenDialog（FOS_PICKFOLDERS），出错退回 FolderBrowserDialog。
//
//go:embed pickfolder_windows.ps1
var pickFolderScript string

// pickFolderTimeout 对话框最长开多久；超过就关掉（用户走开了，别让进程一直挂着）。
const pickFolderTimeout = 15 * time.Minute

// pickFolderWithSystem 弹出系统的“选择文件夹”对话框。选中返回 (目录, true, nil)，取消返回 ("", false, nil)。
func pickFolderWithSystem(ctx context.Context) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, pickFolderTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, powershellPath(),
		"-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden",
		"-EncodedCommand", encodePowerShell(pickFolderScript))
	// 不闪出控制台窗口
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, fmt.Errorf("对话框已关闭：%w", ctx.Err())
		}
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return "", false, fmt.Errorf("系统的选择文件夹对话框出错：%s", firstLine(text))
		}
		return "", false, fmt.Errorf("无法打开系统的选择文件夹对话框：%w", err)
	}
	return parsePickFolderOutput(stdout.String())
}

// parsePickFolderOutput 脚本的输出：PATH:<UTF-8 路径的 base64> 或 CANCEL。
func parsePickFolderOutput(output string) (string, bool, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "CANCEL":
			return "", false, nil
		case strings.HasPrefix(line, "PATH:"):
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "PATH:"))
			if err != nil {
				return "", false, fmt.Errorf("选择文件夹对话框返回了无法识别的路径：%w", err)
			}
			path := strings.TrimSpace(string(raw))
			if path == "" {
				return "", false, errors.New("选择文件夹对话框没有返回路径")
			}
			return path, true, nil
		}
	}
	return "", false, errors.New("选择文件夹对话框没有返回结果")
}

// powershellPath Windows PowerShell 5.1（随系统自带）；找不到固定路径时交给 PATH。
func powershellPath() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		path := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "powershell.exe"
}

// encodePowerShell -EncodedCommand 要的格式：UTF-16LE 再 base64。
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		raw = append(raw, byte(unit), byte(unit>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func firstLine(text string) string {
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		return text[:index]
	}
	return text
}
