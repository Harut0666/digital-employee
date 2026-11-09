//go:build !windows || de_stub

package server

import (
	"context"
	"errors"
)

// pickFolderWithSystem 非 Windows 或 de_stub（占位）构建没有系统的选择文件夹对话框：
// 返回明确的提示，界面应让人手动填写目录路径。
func pickFolderWithSystem(ctx context.Context) (string, bool, error) {
	return "", false, errors.New("当前环境不支持弹出选择文件夹对话框，请手动填写目录路径")
}
