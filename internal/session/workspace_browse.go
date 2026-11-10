// 右栏“工作区文件”：人在界面里浏览会话所在工作区目录的文件夹和文件、预览、用本机应用打开。
// 路径一律经过 resolveInWorkspace（逐段解开链接和 junction，出了工作区目录就拒绝），和前台 Agent 读目录是同一道边界。
// 和 @ 选择不同的是：这里是给人看的，所以“交付”“材料”这两个程序写的文件夹也列出来；隐藏文件和隐藏文件夹仍不列。
package session

import (
	"fmt"
	"os"
	"strings"

	"digital-employee/internal/contract"
)

// MaxBrowseFileBytes 预览一个文件最多读多大；再大的请用本机应用打开。
const MaxBrowseFileBytes = 50 << 20

// browseRoot 会话所在工作区的目录。
func (m *Manager) browseRoot(id string) (string, error) {
	current, err := m.load(id)
	if err != nil {
		return "", err
	}
	root := m.workspaceDir(current.workspaceID)
	if root == "" {
		return "", failure(ErrWorkspaceUnknown, "工作区目录不存在：%s", current.workspaceID)
	}
	return root, nil
}

// hiddenSegment 路径里有没有隐藏的一段（点开头）。
func hiddenSegment(rel string) bool {
	if rel == "." {
		return false
	}
	for _, segment := range strings.Split(rel, "/") {
		if hiddenName(segment) {
			return true
		}
	}
	return false
}

// BrowseFolder 列出工作区目录（或其子目录 sub，相对路径）这一层的文件和文件夹：文件夹在前，最多 200 项。
func (m *Manager) BrowseFolder(id, sub string) (FolderListing, error) {
	root, err := m.browseRoot(id)
	if err != nil {
		return FolderListing{}, err
	}
	if _, rel, resolveErr := resolveInWorkspace(root, sub); resolveErr != nil {
		return FolderListing{}, failure(contract.ErrInvalidInput, "这个路径不在工作区目录里：%s", sub)
	} else if hiddenSegment(rel) {
		return FolderListing{}, failure(contract.ErrInvalidInput, "不能查看隐藏文件夹：%s", rel)
	}
	listing, err := listFolder(root, sub, nil)
	if err != nil {
		return FolderListing{}, failure(contract.ErrInvalidInput, "%s", err.Error())
	}
	return listing, nil
}

// WorkspaceFilePath 工作区目录里一个文件的真实路径（预览、打开用）：必须是目录里的普通文件、不能是隐藏文件。
func (m *Manager) WorkspaceFilePath(id, rel string) (string, error) {
	root, err := m.browseRoot(id)
	if err != nil {
		return "", err
	}
	target, clean, resolveErr := resolveInWorkspace(root, rel)
	if resolveErr != nil {
		return "", failure(contract.ErrInvalidInput, "这个路径不在工作区目录里：%s", rel)
	}
	if clean == "." || hiddenSegment(clean) {
		return "", failure(contract.ErrInvalidInput, "不能查看这个路径：%s", rel)
	}
	info, statErr := os.Stat(target)
	if statErr != nil || !info.Mode().IsRegular() {
		return "", failure(contract.ErrNotFound, "没有这个文件：%s", clean)
	}
	if info.Size() > MaxBrowseFileBytes {
		return "", failure(contract.ErrInvalidInput, "文件太大（%s），不在这里预览，请用本机应用打开", fmt.Sprintf("%.1f MB", float64(info.Size())/1024/1024))
	}
	return target, nil
}

// WorkspaceFolderPath 工作区目录（或其子目录）的真实路径：在文件管理器里打开用。
func (m *Manager) WorkspaceFolderPath(id, sub string) (string, error) {
	root, err := m.browseRoot(id)
	if err != nil {
		return "", err
	}
	target, clean, resolveErr := resolveInWorkspace(root, sub)
	if resolveErr != nil || hiddenSegment(clean) {
		return "", failure(contract.ErrInvalidInput, "这个路径不在工作区目录里：%s", sub)
	}
	info, statErr := os.Stat(target)
	if statErr != nil || !info.IsDir() {
		return "", failure(contract.ErrNotFound, "没有这个文件夹：%s", displayPath(clean))
	}
	return target, nil
}
