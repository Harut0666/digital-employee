package desktop

import "testing"

// 页面不能被缩放：触控屏捏合和滑动前进后退都关着，初始缩放是 100%。
func TestPageOptionsDisableZoom(t *testing.T) {
	page := pageOptions()
	if page.PinchZoom {
		t.Error("PinchZoom 应该关闭")
	}
	if page.SwipeNavigation {
		t.Error("SwipeNavigation 应该关闭")
	}
	if page.ZoomFactor != 1 {
		t.Errorf("ZoomFactor = %v，应该是 1", page.ZoomFactor)
	}
}
