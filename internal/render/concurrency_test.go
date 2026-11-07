package render

import (
	"fmt"
	"sync"
	"testing"
)

// 多个任务同时渲染：字体缓存是进程级的，必须能被并发使用。配合 go test -race 运行。
func TestConcurrentRendersShareFontCache(t *testing.T) {
	if resolveChartFont(nil) == nil {
		t.Skip("本机没有可用的中文字体，图表不走矢量字体路径")
	}
	const renders = 8
	dir := t.TempDir()
	failures := make(chan error, renders)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range renders {
		input := loadFixture(t)
		input.OutDir = dir
		input.IdempotencyKey = fmt.Sprintf("concurrent-%d", index)
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if _, err := RenderReportDocx(input, Options{}); err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	group.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("并发渲染失败：%v", err)
	}
}
