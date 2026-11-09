//go:build !windows

package main

// ensureConsole 非 Windows 平台进程自带终端，什么都不用做。
func ensureConsole() bool { return true }

// detectConsole 非 Windows 平台总有终端（或被重定向的标准输出）。
func detectConsole() bool { return true }
