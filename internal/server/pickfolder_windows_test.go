//go:build windows && !de_stub

package server

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestParsePickFolderOutput(t *testing.T) {
	encode := func(path string) string { return base64.StdEncoding.EncodeToString([]byte(path)) }
	t.Run("选中（含中文路径和 CRLF）", func(t *testing.T) {
		path, picked, err := parsePickFolderOutput("PATH:" + encode(`D:\项目\周报`) + "\r\n")
		if err != nil || !picked || path != `D:\项目\周报` {
			t.Fatalf("得到 %q %v %v", path, picked, err)
		}
	})
	t.Run("取消", func(t *testing.T) {
		if path, picked, err := parsePickFolderOutput("CANCEL\r\n"); err != nil || picked || path != "" {
			t.Fatalf("得到 %q %v %v", path, picked, err)
		}
	})
	t.Run("没有结果、坏数据、空路径", func(t *testing.T) {
		for _, output := range []string{"", "随便什么\n", "PATH:!!!不是base64\n", "PATH:" + encode("  ") + "\n"} {
			if _, picked, err := parsePickFolderOutput(output); err == nil || picked {
				t.Fatalf("%q 应是失败", output)
			}
		}
	})
}

func TestEncodePowerShellIsUTF16LE(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(encodePowerShell("选A"))
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	if got := string(utf16.Decode(units)); got != "选A" {
		t.Fatalf("解出 %q", got)
	}
}

func TestPickFolderScriptIsEmbedded(t *testing.T) {
	// 对话框本身没法在测试里点；至少保证脚本被嵌进来，并且用了文件夹选择模式
	if !strings.Contains(pickFolderScript, "FOS_PICKFOLDERS") || !strings.Contains(pickFolderScript, "TopMost") {
		t.Fatal("嵌入的脚本不完整")
	}
}
