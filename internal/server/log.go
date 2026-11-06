// 控制台日志的级别过滤（Spec 0004「诊断与关于」的日志级别）。
// 阈值越高输出的行越多：error < warn < info < debug。
package server

import (
	"fmt"
	"os"
	"sync"
)

// 日志级别。
const (
	LevelError = "error"
	LevelWarn  = "warn"
	LevelInfo  = "info"
	LevelDebug = "debug"
)

var logSeverity = map[string]int{LevelError: 0, LevelWarn: 1, LevelInfo: 2, LevelDebug: 3}

// ShouldLog 阈值 threshold 下，level 的消息是否应该输出。
func ShouldLog(threshold, level string) bool {
	return logSeverity[level] <= logSeverity[threshold]
}

// LogSink 一行日志的去处。
type LogSink func(level, line string)

// Logger 带级别的日志器。
type Logger struct {
	mu        sync.Mutex
	threshold string
	sink      LogSink
}

// NewLogger 创建日志器；sink 为 nil 时写标准输出/错误。
func NewLogger(level string, sink LogSink) *Logger {
	if _, ok := logSeverity[level]; !ok {
		level = LevelInfo
	}
	if sink == nil {
		sink = consoleSink
	}
	return &Logger{threshold: level, sink: sink}
}

func consoleSink(level, line string) {
	switch level {
	case LevelError:
		fmt.Fprintln(os.Stderr, line)
	case LevelWarn:
		fmt.Fprintln(os.Stderr, line)
	default:
		fmt.Fprintln(os.Stdout, line)
	}
}

// Level 当前阈值。
func (l *Logger) Level() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.threshold
}

// SetLevel 就地调整阈值（设置页保存 diagnostics.logLevel 后生效）。
func (l *Logger) SetLevel(level string) {
	if _, ok := logSeverity[level]; !ok {
		return
	}
	l.mu.Lock()
	l.threshold = level
	l.mu.Unlock()
}

func (l *Logger) emit(level, format string, args ...any) {
	l.mu.Lock()
	threshold := l.threshold
	sink := l.sink
	l.mu.Unlock()
	if !ShouldLog(threshold, level) {
		return
	}
	line := fmt.Sprintf(format, args...)
	if level != LevelInfo {
		line = "[" + level + "] " + line
	}
	sink(level, line)
}

// Errorf 错误日志。
func (l *Logger) Errorf(format string, args ...any) { l.emit(LevelError, format, args...) }

// Warnf 警告日志。
func (l *Logger) Warnf(format string, args ...any) { l.emit(LevelWarn, format, args...) }

// Infof 信息日志（不带前缀）。
func (l *Logger) Infof(format string, args ...any) { l.emit(LevelInfo, format, args...) }

// Debugf 调试日志。
func (l *Logger) Debugf(format string, args ...any) { l.emit(LevelDebug, format, args...) }
