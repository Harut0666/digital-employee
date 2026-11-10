//go:build !windows

package desktop

// Instance 非 Windows 平台没有单实例保护（目前只支持 Windows 桌面包）。
type Instance struct{}

// AcquireInstance 非 Windows：总是第一个实例。
func AcquireInstance(appDir string) (*Instance, bool) { return &Instance{}, true }

// PublishWindow 非 Windows：空操作。
func (*Instance) PublishWindow(appDir string, hwnd uintptr) {}

// Release 非 Windows：空操作。
func (*Instance) Release() {}

// ActivateExisting 非 Windows：没有可激活的实例。
func ActivateExisting(appDir string) bool { return false }
