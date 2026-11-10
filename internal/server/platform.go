// 与操作系统交互的小工具：打开文件夹 / 文件 / 浏览器，以及“测试连接”。
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

// openFolderWithSystem 打开工作区文件夹：Windows 用 explorer，macOS 用 open，其它用 xdg-open。
func openFolderWithSystem(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openFileWithSystem 用系统编辑器打开文件：Windows 用 notepad（不依赖关联程序）。
func openFileWithSystem(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("notepad.exe", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openWithSystem 用系统里关联的默认应用打开文件：.docx 交给 Word，.md 交给关联的编辑器，等等。
// Windows 用 FileProtocolHandler（路径里有空格、& 等字符都安全，不经过 cmd 的解析）。
func openWithSystem(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// revealWithSystem 在文件夹里显示并选中这个文件。
func revealWithSystem(path string) error {
	cmd := revealCommand(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// OpenBrowser 用系统默认浏览器打开界面；打不开不影响服务端运行。
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// redactAPIKey 抹掉文本里的密钥。
func redactAPIKey(text, apiKey string) string {
	if apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, apiKey, "***")
}

// probeConnection 用该连接发一条极短的对话请求（最多 8 个 token），返回是否可用与延迟。
// 不做重试：测试连接要的是“现在能不能用”，重试会掩盖问题。
func probeConnection(conn contract.ModelConnection, model string) contract.ModelConnectionTest {
	started := time.Now()
	result := contract.ModelConnectionTest{Model: model}
	elapsed := func() int64 { return time.Since(started).Milliseconds() }
	if model == "" {
		result.Error = "该连接没有配置模型"
		return result
	}
	base := strings.TrimRight(conn.BaseURL, "/")
	if base == "" {
		result.LatencyMS = elapsed()
		result.Error = "该连接没有配置 Base URL"
		return result
	}
	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": "请只回复 OK"},
		},
		"max_tokens": 8,
		"stream":     false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		result.LatencyMS = elapsed()
		result.Error = err.Error()
		return result
	}
	timeout := 15 * time.Second
	if conn.TimeoutMS != nil && *conn.TimeoutMS > 0 {
		timeout = time.Duration(*conn.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		result.LatencyMS = elapsed()
		result.Error = redactAPIKey(err.Error(), conn.APIKey)
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	if conn.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+conn.APIKey)
	}
	response, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		result.LatencyMS = elapsed()
		result.Error = redactAPIKey(err.Error(), conn.APIKey)
		return result
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		result.LatencyMS = elapsed()
		message := strings.TrimSpace(string(detail))
		if message == "" {
			message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		result.Error = redactAPIKey(message, conn.APIKey)
		return result
	}
	result.OK = true
	result.LatencyMS = elapsed()
	return result
}
