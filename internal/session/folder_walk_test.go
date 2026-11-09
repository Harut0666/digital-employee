// walkFiles / listFolder 的边界：目录链接成环、刚好 200 项、Windows 目录联接（junction）。
//
// 这些是内部回归测试：主接缝（HTTP）测试覆盖“能不能选到文件”，这里补的是递归本身的安全边界。
package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeDirLink 建一个指向目录的链接：Windows 用目录联接（junction，不需要管理员权限），
// 其他系统用符号链接。建不了就跳过并说明原因（CI 容器里可能没有权限或没有 cmd）。
func makeDirLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		command := exec.Command("cmd", "/c", "mklink", "/J", link, target)
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("建不了目录联接（%v）：%s", err, strings.TrimSpace(string(output)))
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("建不了符号链接（%v）", err)
	}
}

// listDeep 在临时目录上跑一次递归列文件（测试里直接用内部函数）。
func listDeep(t *testing.T, root string) FolderListing {
	t.Helper()
	listing, err := listFolderDeep(root)
	if err != nil {
		t.Fatalf("递归列目录失败：%v", err)
	}
	return listing
}

func entryPaths(listing FolderListing) []string {
	paths := make([]string, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

// 目录链接指回上级目录时不能无限递归：进入过的真实目录只进一次。
func TestListFolderDeepDoesNotFollowDirectoryCycle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "资料", "子目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "资料", "汇总.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 在子目录里放一个指回工作区根目录的目录链接：成环
	makeDirLink(t, filepath.Join(root, "资料", "子目录", "指回根目录"), root)

	listing := listDeep(t, root)
	if got := entryPaths(listing); fmt.Sprint(got) != "[资料/汇总.txt]" {
		t.Fatalf("成环时列出的文件 = %v，want 只有 资料/汇总.txt", got)
	}
	if listing.Truncated {
		t.Fatal("成环不应把结果标成截断")
	}
}

// 刚好 200 个文件不算截断：截断只表示“还有没列出来的”。
func TestListFolderDeepExactly200IsNotTruncated(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 200; index++ {
		name := filepath.Join(root, fmt.Sprintf("文件%03d.txt", index))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	listing := listDeep(t, root)
	if len(listing.Entries) != 200 {
		t.Fatalf("200 个文件应全部列出，实际 %d 项", len(listing.Entries))
	}
	if listing.Truncated {
		t.Fatal("刚好 200 项不应标截断")
	}
}

// 201 个文件：列 200 项并标截断（与上一条一起把边界钉死）。
func TestListFolderDeep201IsTruncated(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 201; index++ {
		name := filepath.Join(root, fmt.Sprintf("文件%03d.txt", index))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	listing := listDeep(t, root)
	if len(listing.Entries) != 200 || !listing.Truncated {
		t.Fatalf("201 个文件应列 200 项并标截断，实际 %d 项，truncated=%v", len(listing.Entries), listing.Truncated)
	}
}

// Windows 目录联接（junction）在 Lstat 里既不是目录也不是符号链接，
// 不能被当成文件列出来：它指向工作区里的目录时按目录处理（递归进去），
// 已按真实目录去过的不重复列。
func TestListFolderDeepJunctionIsNotAFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "data")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "汇总.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 名字排在真实目录之后：真实目录先被访问，联接按已访问目录跳过
	makeDirLink(t, filepath.Join(root, "z-link"), target)

	listing := listDeep(t, root)
	got := fmt.Sprint(entryPaths(listing))
	if got != "[data/汇总.txt]" {
		t.Fatalf("联接指向已列过的目录时文件 = %v，want 只有 data/汇总.txt（不要把联接当文件，也不要重复列）", got)
	}
	for _, entry := range listing.Entries {
		if entry.Path == "z-link" {
			t.Fatalf("目录联接被当成了文件：%+v", entry)
		}
	}

	// 单层列目录也一样：联接是目录，不是文件
	listed, err := listFolder(root, "", workspaceHidden)
	if err != nil {
		t.Fatalf("列目录失败：%v", err)
	}
	found := false
	for _, entry := range listed.Entries {
		if entry.Name != "z-link" {
			continue
		}
		found = true
		if !entry.Dir {
			t.Fatalf("目录联接应标成目录：%+v", entry)
		}
	}
	if !found {
		t.Fatalf("单层列目录里没有 z-link：%+v", listed.Entries)
	}
}

// 指向工作区目录之外的目录联接既不能列，也不能进。
func TestListFolderDeepSkipsJunctionOutsideRoot(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "外部.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "内部.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, filepath.Join(root, "外部目录"), outside)

	listing := listDeep(t, root)
	if got := fmt.Sprint(entryPaths(listing)); got != "[内部.txt]" {
		t.Fatalf("指到工作区外的联接不应列：%v", got)
	}
}
