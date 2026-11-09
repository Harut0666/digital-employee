//go:build !windows

package desktop

import (
	"fmt"
	"os"
)

// ShowError 非 Windows 平台没有原生消息框：写到标准错误。
func ShowError(title, text string) {
	fmt.Fprintf(os.Stderr, "%s\n%s\n", title, text)
}

// AskYesNo 非 Windows 平台没有原生消息框：不询问，视为“否”。
func AskYesNo(title, text string) bool { return false }
