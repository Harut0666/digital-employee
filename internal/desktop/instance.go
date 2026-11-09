package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

// 单实例：同一个应用目录同时只允许一个窗口实例（两个进程写同一份设置和会话日志会互相覆盖）。
// 用 Windows 命名互斥体检测；名字里带应用目录的哈希，所以 DE_APP_DIR 不同的实例（隔离测试数据）互不影响。

// instanceKey 应用目录的规范形式：绝对路径、去掉末尾分隔符，Windows 下不区分大小写。
func instanceKey(appDir string) string {
	if absolute, err := filepath.Abs(appDir); err == nil {
		appDir = absolute
	}
	appDir = filepath.Clean(appDir)
	if runtime.GOOS == "windows" {
		appDir = strings.ToLower(appDir)
	}
	return appDir
}

// instanceHash 应用目录的短哈希（16 个十六进制字符）。
func instanceHash(appDir string) string {
	sum := sha256.Sum256([]byte(instanceKey(appDir)))
	return hex.EncodeToString(sum[:8])
}

// InstanceName 这个应用目录对应的命名互斥体名（Local\ 前缀：只在当前登录会话内可见）。
func InstanceName(appDir string) string {
	return `Local\DigitalEmployee-` + instanceHash(appDir)
}

// windowPropName 第一个实例主窗口上的窗口属性名（和互斥体同样按应用目录区分）。
func windowPropName(appDir string) string {
	return "DigitalEmployee-" + instanceHash(appDir)
}
