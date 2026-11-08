package engine

// 交付物写进工作区“交付”文件夹（T42）：Markdown 交付物用 .md，写不进去不挡流程。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

// markdownDemoEngine 把演示员工的渲染步骤改成产出 Markdown（场景 B 的形状）。
func markdownDemoEngine(t *testing.T) *demoEngine {
	t.Helper()
	demo := newDemoEngine(t, demoEmployeeOptions{})
	demo.engine.toolClient = &mdToolClient{fakeToolClient: demo.tools}
	return demo
}

// mdToolClient 渲染报告时产出 .md 而不是 .docx。
type mdToolClient struct {
	*fakeToolClient
}

func (c *mdToolClient) Call(name string, args any) (contract.ToolCallResult, error) {
	if name != contract.ToolRenderReportDocx {
		return c.fakeToolClient.Call(name, args)
	}
	c.mu.Lock()
	c.calls = append(c.calls, name)
	c.mu.Unlock()
	mapped, _ := args.(map[string]any)
	outDir, _ := mapped["outDir"].(string)
	path := filepath.Join(outDir, "deliverables", "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return contract.ToolCallResult{OK: false, Error: err.Error()}, nil
	}
	if err := os.WriteFile(path, []byte("# 研判简报\n"), 0o644); err != nil {
		return contract.ToolCallResult{OK: false, Error: err.Error()}, nil
	}
	return contract.ToolCallResult{OK: true, Data: map[string]any{"mdPath": path, "sha256": "md"}}, nil
}

func TestMarkdownDeliverableIsFiledAsMarkdown(t *testing.T) {
	demo := markdownDemoEngine(t)
	demo.engine.SetWorkspaces(func(id string) (string, bool) {
		if id != "ws-a" {
			return "", false
		}
		return demo.dir, true
	})
	created, err := demo.engine.Create(contract.CreateTaskInput{
		Employee:    "demo",
		Form:        map[string]any{"period": map[string]any{"year": float64(2026), "week": float64(44)}},
		Actor:       contract.RoleInitiator,
		WorkspaceID: "ws-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := demo.submit(t, created.ID)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("状态 = %s：%+v", snapshot.State, snapshot.Pause)
	}
	want := filepath.Join(demo.dir, "交付", "演示员工_2026年第44周_v1.md")
	if _, err := os.Stat(want); err != nil {
		entries, _ := os.ReadDir(filepath.Join(demo.dir, "交付"))
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("交付文件夹里没有 %s：%v", filepath.Base(want), names)
	}
	events, err := demo.engine.Events(created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	filed := 0
	for _, event := range events {
		if event.Type == contract.EventDeliverableFiled {
			filed++
			if event.FiledPath != want {
				t.Fatalf("交付文件事件路径 = %q", event.FiledPath)
			}
			if !strings.Contains(event.FiledReason, ".md") {
				t.Fatalf("说明里应写文件名：%q", event.FiledReason)
			}
		}
	}
	if filed != 1 {
		t.Fatalf("应有 1 条交付文件事件，实际 %d", filed)
	}
}

// 任务没记下工作区（旧任务）时无从知道目录，不写也不挡流程。
func TestUnknownWorkspaceIsLeftAlone(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	demo.engine.SetWorkspaces(func(id string) (string, bool) { return "", false })
	task := demo.create(t)
	snapshot := demo.submit(t, task.ID)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("状态 = %s：%+v", snapshot.State, snapshot.Pause)
	}
	if entries, err := os.ReadDir(filepath.Join(demo.engine.dir)); err == nil {
		for _, entry := range entries {
			if entry.Name() == "交付" {
				t.Fatal("不知道工作区目录时不该写交付文件夹")
			}
		}
	}
	events, _ := demo.engine.Events(task.ID, 0)
	for _, event := range events {
		if event.Type == contract.EventDeliverableFiled {
			t.Fatalf("不知道工作区目录时不写说明：%+v", event)
		}
	}
}
