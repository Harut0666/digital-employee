package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	return string(raw)
}

func TestRollingLogWritesTimestampedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "a.log")
	log, err := OpenRollingLog(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	log.now = func() time.Time { return time.Date(2026, 11, 9, 8, 30, 15, 123_000_000, time.Local) }
	log.WriteLine("hello")
	log.WriteLine("[warn] 中文")
	_ = log.Close()
	got := readText(t, path)
	want := "2026-11-09 08:30:15.123 hello\n2026-11-09 08:30:15.123 [warn] 中文\n"
	if got != want {
		t.Fatalf("日志内容 = %q，想要 %q", got, want)
	}
}

func TestRollingLogRotatesBySizeAndKeepsLimitedBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	log, err := OpenRollingLog(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	line := strings.Repeat("x", 40) // 每次写 41 字节
	for index := 0; index < 10; index++ {
		_, _ = log.Write([]byte(line + string(rune('0'+index)) + "\n"))
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("缺少 %s：%v", name, err)
		}
		if info.Size() > 100 {
			t.Fatalf("%s 有 %d 字节，超过上限 100", name, info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("keep=2 不该留下 .3：%v", err)
	}
	// 最新的内容在当前文件里，最老的在 .2 里
	if !strings.Contains(readText(t, path), "9\n") {
		t.Fatalf("当前文件没有最新一行：%q", readText(t, path))
	}
	if strings.Contains(readText(t, path+".2"), "0\n") {
		t.Fatalf("最老的一行应该已经被丢掉")
	}
}

func TestRollingLogRotatesOversizedFileOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := OpenRollingLog(path, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	log.WriteLine("fresh")
	_ = log.Close()
	if got := readText(t, path); strings.Contains(got, "yyy") || !strings.Contains(got, "fresh") {
		t.Fatalf("启动时应先滚掉超限的旧文件：%q", got)
	}
	if got := readText(t, path+".1"); !strings.HasPrefix(got, "yyy") {
		t.Fatalf(".1 应是旧内容：%q", got)
	}
}

func TestRollingLogKeepZeroDiscardsOldFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	log, err := OpenRollingLog(path, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for index := 0; index < 6; index++ {
		_, _ = log.Write([]byte(strings.Repeat("z", 30)))
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("keep=0 不该留旧文件：%v", err)
	}
}

func TestRollingLogReopensAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	log, err := OpenRollingLog(path, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	log.WriteLine("one")
	_ = log.Close()
	// Close 之后再写会重新打开
	log.WriteLine("two")
	_ = log.Close()
	if got := readText(t, path); !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Fatalf("重开后应追加：%q", got)
	}
}

func TestResolveAppDirForFollowsStartRules(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name    string
		options Options
		want    string
	}{
		{"缺省走 APPDATA", Options{Env: map[string]string{"APPDATA": base}}, filepath.Join(base, "digital-employee-app")},
		{"DE_APP_DIR", Options{Env: map[string]string{"DE_APP_DIR": filepath.Join(base, "iso")}}, filepath.Join(base, "iso")},
		{"参数里的应用目录", Options{AppDir: filepath.Join(base, "arg"), Env: map[string]string{}}, filepath.Join(base, "arg")},
		{"单目录参数", Options{WorkspaceDir: filepath.Join(base, "ws"), Env: map[string]string{"DE_APP_DIR": filepath.Join(base, "iso")}}, filepath.Join(base, "ws")},
		{"DE_WORKSPACE 优先", Options{WorkspaceDir: filepath.Join(base, "ws"), Env: map[string]string{"DE_WORKSPACE": filepath.Join(base, "envws")}}, filepath.Join(base, "envws")},
	}
	for _, item := range cases {
		if got := ResolveAppDirFor(item.options); got != item.want {
			t.Errorf("%s：得到 %s，想要 %s", item.name, got, item.want)
		}
	}
}

func TestLogFilePathIsUnderLogsDir(t *testing.T) {
	got := LogFilePath(filepath.Join("a", "b"))
	if got != filepath.Join("a", "b", "logs", "digital-employee.log") {
		t.Fatalf("日志路径 = %s", got)
	}
}
