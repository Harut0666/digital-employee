//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
)

// exe 用 GUI 子系统构建（-H=windowsgui），双击没有黑色控制台窗口；
// 代价是从终端运行时，系统不会给它控制台，标准输出会丢。所以命令行用途（子命令、--version、-h、
// --browser、--serve-only、参数写错）启动时把自己接到父进程的控制台上，再把 os.Stdout / os.Stderr / os.Stdin
// 指向控制台：
//
//   - 已经被重定向到文件或管道的句柄不动（`> out.txt`、`| more`、脚本里捕获输出都照常）；
//   - 父进程没有控制台（资源管理器、快捷方式带参数启动）时什么也不做，此时没有地方可显示。
//
// 限制：cmd.exe / PowerShell 不会等 GUI 子系统的程序结束，提示符会先回来，输出稍后才出现，退出码也拿不到。
// 需要等待时用 `start /wait digital-employee.exe ...`（cmd）或 `Start-Process -Wait -NoNewWindow`（PowerShell）。
const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS = (DWORD)-1

const (
	fileTypeDisk = 0x0001
	fileTypePipe = 0x0003
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procGetFileType   = kernel32.NewProc("GetFileType")

	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procSetStdHandle     = kernel32.NewProc("SetStdHandle")
)

// detectConsole 进程现在有没有能看到输出的地方：有控制台窗口，或者标准输出是文件 / 管道。
// 双击启动的 GUI 子系统进程两者都没有。
func detectConsole() bool {
	if window, _, _ := procGetConsoleWindow.Call(); window != 0 {
		return true
	}
	return isRedirected(syscall.STD_OUTPUT_HANDLE) || isRedirected(syscall.STD_ERROR_HANDLE)
}

// isRedirected 句柄是不是有效的文件或管道（用户做了重定向，或父进程用管道捕获）。
func isRedirected(kind int) bool {
	handle, err := syscall.GetStdHandle(kind)
	if err != nil || handle == 0 || handle == syscall.InvalidHandle {
		return false
	}
	fileType, _, _ := procGetFileType.Call(uintptr(handle))
	return fileType == fileTypeDisk || fileType == fileTypePipe
}

// ensureConsole 见文件头注释；返回终端里能不能看到输出。
func ensureConsole() bool {
	needOut := !isRedirected(syscall.STD_OUTPUT_HANDLE)
	needErr := !isRedirected(syscall.STD_ERROR_HANDLE)
	needIn := !isRedirected(syscall.STD_INPUT_HANDLE)
	if !needOut && !needErr && !needIn {
		return true
	}

	result, _, errno := procAttachConsole.Call(attachParentProcess)
	// ERROR_ACCESS_DENIED：进程已经有控制台（go run 的控制台版本），照样重新指一遍
	if result == 0 && errno != syscall.ERROR_ACCESS_DENIED {
		return !needOut || !needErr
	}

	available := !needOut || !needErr
	if needOut || needErr {
		if console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
			if needOut {
				os.Stdout = console
				setStdHandle(syscall.STD_OUTPUT_HANDLE, console)
			}
			if needErr {
				os.Stderr = console
				setStdHandle(syscall.STD_ERROR_HANDLE, console)
			}
			available = true
		}
	}
	if needIn {
		if console, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
			os.Stdin = console
			setStdHandle(syscall.STD_INPUT_HANDLE, console)
		}
	}
	// 标准库 log 在包初始化时就绑定了当时的 os.Stderr
	log.SetOutput(os.Stderr)
	return available
}

// setStdHandle 让进程的标准句柄也指向控制台（之后启动的子进程会继承它）。
func setStdHandle(kind int, console *os.File) {
	_, _, _ = procSetStdHandle.Call(uintptr(kind), console.Fd())
}
