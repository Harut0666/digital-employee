package tools

// 单测共用：样例目录、一次性加载的数据集、临时目录清理。

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"digital-employee/internal/contract"
)

var (
	sharedOnce     sync.Once
	sharedOutDir   string
	sharedLoad     contract.LoadSalesOutput
	sharedLoadErr  error
	sharedCleanDir string
)

// TestMain 跑完清理共享临时目录。
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedCleanDir != "" {
		os.RemoveAll(sharedCleanDir)
	}
	os.Exit(code)
}

func samplesPath() (string, error) {
	return filepath.Abs(filepath.Join("..", "..", "samples", "scenario-a"))
}

// sharedSampleLoad 在整轮测试里只解析一次样例 xlsx。
func sharedSampleLoad(t *testing.T) contract.LoadSalesOutput {
	t.Helper()
	samples := samplesDir(t)
	sharedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "de-tools-test-")
		if err != nil {
			sharedLoadErr = err
			return
		}
		sharedCleanDir = dir
		sharedOutDir = dir
		sharedLoad, sharedLoadErr = LoadSales(filepath.Join(samples, "销售明细.xlsx"), dir)
	})
	if sharedLoadErr != nil {
		t.Fatalf("加载样例数据集失败：%v", sharedLoadErr)
	}
	return sharedLoad
}
