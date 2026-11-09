// 添加工作区时的“浏览…”：POST /api/workspaces/pick-folder 在本机弹出系统的选择文件夹对话框。
// 对话框是阻塞的、测试里不能真弹，所以从 Options.PickFolder 注入假的选择器，只验证接口的形状和规则。
package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/contract"
)

// pickFolderReply 选择文件夹接口的响应。
type pickFolderReply struct {
	OK        bool   `json:"ok"`
	Path      string `json:"path"`
	Cancelled bool   `json:"cancelled"`
	Message   string `json:"message"`
	Error     string `json:"error"`
}

func (h *harness) pickFolderAs(role contract.Role) (int, pickFolderReply, []byte) {
	h.t.Helper()
	status, data := h.send(http.MethodPost, "/api/workspaces/pick-folder", role, "", nil)
	var reply pickFolderReply
	if len(data) > 0 {
		reply = decode[pickFolderReply](h.t, data)
	}
	return status, reply, data
}

func TestPickFolderReturnsTheChosenDirectory(t *testing.T) {
	chosen := `D:\项目\周报`
	h := newHarness(t, nil, withPickFolder(func(context.Context) (string, bool, error) { return chosen, true, nil }))
	status, reply, data := h.pickFolderAs(contract.RoleInitiator)
	if status != http.StatusOK || !reply.OK || reply.Cancelled || reply.Path != chosen {
		t.Fatalf("选了目录：%d %s", status, data)
	}
}

func TestPickFolderCancelledByTheUser(t *testing.T) {
	h := newHarness(t, nil, withPickFolder(func(context.Context) (string, bool, error) { return "", false, nil }))
	status, reply, data := h.pickFolderAs(contract.RoleInitiator)
	if status != http.StatusOK || !reply.OK || !reply.Cancelled || reply.Path != "" {
		t.Fatalf("取消：%d %s", status, data)
	}
}

func TestPickFolderFailureIsReportedAsOkFalse(t *testing.T) {
	h := newHarness(t, nil, withPickFolder(func(context.Context) (string, bool, error) {
		return "", false, errors.New("无法打开系统的选择文件夹对话框")
	}))
	status, reply, data := h.pickFolderAs(contract.RoleInitiator)
	// 业务失败：200 + ok:false + message（和其它工作区接口的 ok:false 约定一致）
	if status != http.StatusOK || reply.OK || !strings.Contains(reply.Message, "无法打开系统的选择文件夹对话框") {
		t.Fatalf("失败：%d %s", status, data)
	}
}

// 选了但没拿到路径也是失败，不能当成选中。
func TestPickFolderWithoutAPathIsAFailure(t *testing.T) {
	h := newHarness(t, nil, withPickFolder(func(context.Context) (string, bool, error) { return "  ", true, nil }))
	if status, reply, data := h.pickFolderAs(contract.RoleInitiator); status != http.StatusOK || reply.OK || reply.Message == "" {
		t.Fatalf("空路径：%d %s", status, data)
	}
}

func TestPickFolderIsInitiatorOnly(t *testing.T) {
	called := false
	h := newHarness(t, nil, withPickFolder(func(context.Context) (string, bool, error) {
		called = true
		return `D:\x`, true, nil
	}))
	for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator} {
		if status, reply, data := h.pickFolderAs(role); status != http.StatusForbidden || reply.Error != contract.ErrForbiddenRole {
			t.Fatalf("%s 不该能弹出对话框：%d %s", role, status, data)
		}
	}
	// 没带角色头：和其它接口一样要求角色
	if status, _, data := h.pickFolderAs(""); status != http.StatusForbidden {
		t.Fatalf("缺少角色：%d %s", status, data)
	}
	if called {
		t.Fatal("被拒绝的请求不该弹出对话框")
	}
}

func TestPickFolderRefusedWhenWorkspacesAreReadOnly(t *testing.T) {
	called := false
	h := newHarness(t, nil, withReadOnlyWorkspaces(func(context.Context) (string, bool, error) {
		called = true
		return `D:\x`, true, nil
	}))
	// 单目录模式（DE_WORKSPACE 指定）：沿用“不能添加工作区”的错误
	status, reply, data := h.pickFolderAs(contract.RoleInitiator)
	if status != http.StatusConflict || reply.Error != "workspace_read_only" || reply.Message == "" {
		t.Fatalf("单目录模式应拒绝：%d %s", status, data)
	}
	if called {
		t.Fatal("只读时不该弹出对话框")
	}
}

func TestPickFolderOnlyOneDialogAtATime(t *testing.T) {
	var mu sync.Mutex
	opened := 0
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	h := newHarness(t, nil, withPickFolder(func(ctx context.Context) (string, bool, error) {
		mu.Lock()
		opened++
		mu.Unlock()
		entered <- struct{}{}
		select {
		case <-release:
			return `D:\first`, true, nil
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}))
	first := make(chan pickFolderReply, 1)
	go func() {
		_, reply, _ := h.pickFolderAs(contract.RoleInitiator)
		first <- reply
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("第一个对话框没有打开")
	}
	status, reply, data := h.pickFolderAs(contract.RoleInitiator)
	if status != http.StatusOK || reply.OK || reply.Message == "" {
		t.Fatalf("已有对话框时再请求应返回 ok:false：%d %s", status, data)
	}
	close(release)
	select {
	case got := <-first:
		if !got.OK || got.Path != `D:\first` {
			t.Fatalf("第一个对话框的结果 = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("第一个请求没有返回")
	}
	mu.Lock()
	defer mu.Unlock()
	if opened != 1 {
		t.Fatalf("只该打开一个对话框，实际 %d 个", opened)
	}
}
