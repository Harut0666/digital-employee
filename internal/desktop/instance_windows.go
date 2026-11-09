//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	user32                  = syscall.NewLazyDLL("user32.dll")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procReleaseMutex        = kernel32.NewProc("ReleaseMutex")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procGetPropW            = user32.NewProc("GetPropW")
	procSetPropW            = user32.NewProc("SetPropW")
	procRemovePropW         = user32.NewProc("RemovePropW")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
)

const (
	errorAlreadyExists = 183
	swShow             = 5
	swRestore          = 9
)

// Instance 持有本应用目录的单实例互斥体，进程退出时系统自动释放。
type Instance struct {
	mutex uintptr
	// marked 已打上标记的窗口和标记名
	marked     uintptr
	markedName *uint16
}

// AcquireInstance 抢单实例互斥体。first=false 表示同一个应用目录已经有实例在运行。
// 创建互斥体本身失败（极少见）时当作第一个实例继续，不拦着用户。
func AcquireInstance(appDir string) (instance *Instance, first bool) {
	name, err := syscall.UTF16PtrFromString(InstanceName(appDir))
	if err != nil {
		return &Instance{}, true
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return &Instance{}, true
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		_, _, _ = procCloseHandle.Call(handle)
		return nil, false
	}
	return &Instance{mutex: handle}, true
}

// PublishWindow 给主窗口打上“属于这个应用目录”的窗口属性，后启动的实例靠它找到窗口
// （按标题找会把另一个 DE_APP_DIR 的实例的窗口也认成目标）。
func (i *Instance) PublishWindow(appDir string, hwnd uintptr) {
	if i == nil || hwnd == 0 {
		return
	}
	name, err := syscall.UTF16PtrFromString(windowPropName(appDir))
	if err != nil {
		return
	}
	if result, _, _ := procSetPropW.Call(hwnd, uintptr(unsafe.Pointer(name)), 1); result != 0 {
		i.marked, i.markedName = hwnd, name
	}
}

// Release 释放互斥体（正常退出时调用；崩溃时系统会回收）。
func (i *Instance) Release() {
	if i == nil {
		return
	}
	if i.marked != 0 {
		_, _, _ = procRemovePropW.Call(i.marked, uintptr(unsafe.Pointer(i.markedName)))
		i.marked = 0
	}
	if i.mutex != 0 {
		_, _, _ = procReleaseMutex.Call(i.mutex)
		_, _, _ = procCloseHandle.Call(i.mutex)
		i.mutex = 0
	}
}

// ActivateExisting 把已在运行的实例的窗口带到前台（最小化的先还原）。
// 第一个实例还没建好窗口（正在启动）或窗口已经没了时返回 false，调用方静默退出即可。
func ActivateExisting(appDir string) bool {
	name, err := syscall.UTF16PtrFromString(windowPropName(appDir))
	if err != nil {
		return false
	}
	var found uintptr
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if value, _, _ := procGetPropW.Call(hwnd, uintptr(unsafe.Pointer(name))); value != 0 {
			found = hwnd
			return 0 // 停止枚举
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(callback, 0)
	if found == 0 {
		// 窗口属性读不到（理论上不会）：退而按标题找，宁可多唤起一个窗口也不要什么都不发生
		title, titleErr := syscall.UTF16PtrFromString(WindowTitle)
		if titleErr != nil {
			return false
		}
		found, _, _ = procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	}
	if found == 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(found); iconic != 0 {
		_, _, _ = procShowWindow.Call(found, swRestore)
	} else {
		_, _, _ = procShowWindow.Call(found, swShow)
	}
	_, _, _ = procBringWindowToTop.Call(found)
	_, _, _ = procSetForegroundWindow.Call(found)
	return true
}
