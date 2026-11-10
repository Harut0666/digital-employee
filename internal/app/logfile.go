// 滚动日志文件：窗口模式（双击启动）没有控制台，日志写到应用目录的 logs/digital-employee.log。
//
// 按大小滚动：当前文件超过上限就改名为 .1，原来的 .1 变 .2，依此类推，最多保留 keep 份旧文件。
// 所有写入都容错：磁盘满、文件被占用都只丢这一行日志，不影响程序。
package app

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

// 日志文件的默认位置与滚动参数。
const (
	// LogDirName 应用目录下放日志的子目录
	LogDirName = "logs"
	// LogFileName 当前日志文件名
	LogFileName = "digital-employee.log"
	// DefaultLogMaxBytes 单个日志文件上限
	DefaultLogMaxBytes int64 = 5 << 20
	// DefaultLogKeep 保留的旧日志份数（.1 到 .3）
	DefaultLogKeep = 3
	// CrashFileName 运行时崩溃（未捕获的 panic、致命错误）的输出文件
	CrashFileName = "crash.log"
)

// LogFilePath 应用目录里的日志文件路径。
func LogFilePath(appDir string) string {
	return filepath.Join(appDir, LogDirName, LogFileName)
}

// RollingLog 按大小滚动的日志文件，并发安全。
type RollingLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	file     *os.File
	size     int64
	now      func() time.Time
}

// OpenRollingLog 打开（必要时创建）日志文件，追加写入。maxBytes <= 0 用默认上限，keep < 0 视为 0（不留旧文件）。
func OpenRollingLog(path string, maxBytes int64, keep int) (*RollingLog, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultLogMaxBytes
	}
	if keep < 0 {
		keep = 0
	}
	log := &RollingLog{path: path, maxBytes: maxBytes, keep: keep, now: time.Now}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := log.open(); err != nil {
		return nil, err
	}
	// 上次留下的文件已经超限：启动时先滚一次
	if log.size >= log.maxBytes {
		log.rotate()
	}
	return log, nil
}

func (l *RollingLog) open() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	l.file, l.size = file, info.Size()
	return nil
}

// rotate 把当前文件滚成 .1，旧的依次后移，超出 keep 的删掉；失败时尽量继续往原文件写。
func (l *RollingLog) rotate() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if l.keep == 0 {
		_ = os.Remove(l.path)
	} else {
		_ = os.Remove(rotatedName(l.path, l.keep))
		for index := l.keep - 1; index >= 1; index-- {
			_ = os.Rename(rotatedName(l.path, index), rotatedName(l.path, index+1))
		}
		_ = os.Rename(l.path, rotatedName(l.path, 1))
	}
	if err := l.open(); err != nil {
		l.file, l.size = nil, 0
	}
}

func rotatedName(path string, index int) string {
	return path + "." + strconv.Itoa(index)
}

// Write 原样追加（给标准库 log.SetOutput 用）；返回值总是成功，写不进去就丢掉。
func (l *RollingLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.append(data)
	return len(data), nil
}

// WriteLine 加时间戳写一行（行尾换行由这里补）。
func (l *RollingLog) WriteLine(line string) {
	stamp := l.now().Format("2006-01-02 15:04:05.000")
	l.mu.Lock()
	defer l.mu.Unlock()
	l.append([]byte(stamp + " " + line + "\n"))
}

func (l *RollingLog) append(data []byte) {
	if l.file == nil {
		if err := l.open(); err != nil {
			return
		}
	}
	if l.size > 0 && l.size+int64(len(data)) > l.maxBytes {
		l.rotate()
		if l.file == nil {
			return
		}
	}
	written, err := l.file.Write(data)
	l.size += int64(written)
	if err != nil {
		// 文件句柄坏了（被外部删除等）：丢掉句柄，下次写入重开
		_ = l.file.Close()
		l.file = nil
	}
}

// Close 关闭文件；之后再写会重新打开。
func (l *RollingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// RedirectCrashOutput 把运行时的崩溃输出（其它 goroutine 的 panic、致命错误的堆栈）写进 logs/crash.log。
// 没有控制台时这些信息否则会直接丢失。返回的函数在正常退出时调用：恢复默认输出，文件是空的就删掉。
func RedirectCrashOutput(appDir string) func() {
	path := filepath.Join(appDir, LogDirName, CrashFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}
	}
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		_ = file.Close()
		return func() {}
	}
	return func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		info, statErr := file.Stat()
		_ = file.Close()
		if statErr == nil && info.Size() == 0 {
			_ = os.Remove(path)
		}
	}
}

// ResolveAppDirFor 按启动参数和环境算出这次启动用的应用目录（单目录模式下就是那个工作区目录）。
// 规则和 Start 一致：DE_WORKSPACE > 参数里的工作区目录 > 参数里的应用目录 > DE_APP_DIR > 缺省。
func ResolveAppDirFor(options Options) string {
	env := options.Env
	if envValue(env, "DE_WORKSPACE") != "" {
		return ResolveAppDir(env)
	}
	if options.WorkspaceDir != "" {
		return absolutePath(options.WorkspaceDir)
	}
	if options.AppDir != "" {
		return absolutePath(options.AppDir)
	}
	return ResolveAppDir(env)
}
