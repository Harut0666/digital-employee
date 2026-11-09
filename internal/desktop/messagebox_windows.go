//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

var procMessageBoxW = user32.NewProc("MessageBoxW")

const (
	mbOK            = 0x00000000
	mbYesNo         = 0x00000004
	mbIconError     = 0x00000010
	mbIconQuestion  = 0x00000020
	mbSetForeground = 0x00010000
	idYes           = 6
)

func messageBox(title, text string, flags uintptr) int {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	result, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), flags|mbSetForeground)
	return int(result)
}

// ShowError 弹一个原生错误消息框（没有控制台时，启动失败的原因只能这样让用户看到）。
func ShowError(title, text string) {
	messageBox(title, text, mbOK|mbIconError)
}

// AskYesNo 弹一个“是 / 否”消息框，选“是”返回 true。
func AskYesNo(title, text string) bool {
	return messageBox(title, text, mbYesNo|mbIconQuestion) == idYes
}
