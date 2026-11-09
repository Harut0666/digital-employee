package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestInstanceNameIsStablePerAppDirAndDiffersAcrossDirs(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "app-a")
	b := filepath.Join(base, "app-b")
	nameA := InstanceName(a)
	if nameA != InstanceName(a) {
		t.Fatalf("同一个目录两次得到不同的名字")
	}
	if nameA == InstanceName(b) {
		t.Fatalf("不同的应用目录（DE_APP_DIR）必须是不同的实例名")
	}
	if !strings.HasPrefix(nameA, `Local\DigitalEmployee-`) {
		t.Fatalf("互斥体名应在 Local\\ 命名空间下：%s", nameA)
	}
	// 路径写法不同但指向同一个目录：末尾分隔符、. 和 ..
	if InstanceName(a+string(filepath.Separator)) != nameA {
		t.Fatalf("末尾分隔符不该改变实例名")
	}
	if InstanceName(filepath.Join(a, "x", "..")) != nameA {
		t.Fatalf("路径里的 .. 应先规范化")
	}
	if windowPropName(a) == windowPropName(b) || windowPropName(a) == "" {
		t.Fatalf("窗口属性名也要按应用目录区分")
	}
}

func TestInstanceNameIgnoresCaseOnWindows(t *testing.T) {
	if os.PathSeparator != '\\' {
		t.Skip("只有 Windows 的路径不区分大小写")
	}
	if InstanceName(`D:\Data\App`) != InstanceName(`d:\data\app`) {
		t.Fatalf("Windows 路径不区分大小写，实例名也不该区分")
	}
}

func TestAcquireInstanceIsExclusivePerAppDir(t *testing.T) {
	if os.PathSeparator != '\\' {
		t.Skip("单实例互斥体只在 Windows 实现")
	}
	dir := filepath.Join(t.TempDir(), "single-instance-test")
	first, ok := AcquireInstance(dir)
	if !ok {
		t.Fatalf("第一个实例应该拿到互斥体")
	}
	if _, ok := AcquireInstance(dir); ok {
		t.Fatalf("同一个应用目录的第二个实例不应该拿到互斥体")
	}
	other, ok := AcquireInstance(filepath.Join(t.TempDir(), "other"))
	if !ok {
		t.Fatalf("不同应用目录的实例互不影响")
	}
	other.Release()
	first.Release()
	again, ok := AcquireInstance(dir)
	if !ok {
		t.Fatalf("第一个实例释放后应该能再次拿到")
	}
	again.Release()
}

func TestIsExternalURL(t *testing.T) {
	cases := map[string]bool{
		"https://example.com/docs":    true,
		"http://example.com":          true,
		"HTTPS://EXAMPLE.COM":         true,
		"https://127.0.0.1.evil.com/": true,
		"http://localhost:8787/":      false,
		"http://127.0.0.1:8787/x":     false,
		"http://127.8.8.8/":           false,
		"http://[::1]:8787/":          false,
		"http://app.localhost:3000/":  false,
		"about:blank":                 false,
		"data:text/html,hi":           false,
		"blob:http://localhost/abc":   false,
		"mailto:a@b.c":                false,
		"file:///C:/x.txt":            false,
		"":                            false,
		"http://":                     false,
		"://bad":                      false,
	}
	for raw, want := range cases {
		if got := IsExternalURL(raw); got != want {
			t.Errorf("IsExternalURL(%q) = %v，想要 %v", raw, got, want)
		}
	}
}

func TestPlanContextMenuKeepsEditingItemsAndTidiesSeparators(t *testing.T) {
	sep := menuEntry{Separator: true}
	entries := []menuEntry{
		sep,
		{ID: mygo.ContextMenuBack},
		{ID: mygo.ContextMenuReload},
		sep,
		{ID: mygo.ContextMenuCut},
		{ID: mygo.ContextMenuCopy},
		{ID: mygo.ContextMenuPaste},
		sep,
		sep,
		{ID: "saveAs"},
		{ID: ""},
		sep,
		{ID: mygo.ContextMenuSelectAll},
		sep,
		{ID: mygo.ContextMenuInspect},
	}
	visible := planContextMenu(entries)
	want := []bool{
		false,        // 开头的分隔线
		false, false, // 后退、重新加载
		false, // 它们后面的分隔线（前面没有保留项）
		true, true, true,
		false, false, // 后面的项被去掉，两条连续分隔线都隐藏
		false, false,
		true, // 选中全部之前的分隔线：前面有保留项、后面有保留项
		true,
		false, // 结尾的分隔线
		false,
	}
	for index := range want {
		if visible[index] != want[index] {
			t.Errorf("第 %d 项（%+v）可见=%v，想要 %v", index, entries[index], visible[index], want[index])
		}
	}
}

func TestPlanContextMenuRemovesEverythingForUselessMenu(t *testing.T) {
	entries := []menuEntry{{ID: mygo.ContextMenuBack}, {ID: mygo.ContextMenuForward}, {Separator: true}, {ID: mygo.ContextMenuReload}, {ID: mygo.ContextMenuInspect}}
	for index, shown := range planContextMenu(entries) {
		if shown {
			t.Errorf("第 %d 项不该显示", index)
		}
	}
}

func newTestStore(t *testing.T, areas []mygo.Rectangle) (*fileStateStore, string) {
	t.Helper()
	dir := t.TempDir()
	return &fileStateStore{
		path:      StatePath(dir),
		minimum:   Size{Width: 960, Height: 640},
		workAreas: func() []mygo.Rectangle { return areas },
	}, dir
}

var primaryScreen = []mygo.Rectangle{{X: 0, Y: 0, Width: 1920, Height: 1040}}

func TestWindowStateRoundTrip(t *testing.T) {
	store, dir := newTestStore(t, primaryScreen)
	saved := map[string]mygo.WindowState{"main": {X: 100, Y: 80, Width: 1300, Height: 900, Maximized: true}}
	if err := store.SaveWindowStates(saved); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "window-state.json")); err != nil {
		t.Fatalf("状态应该存在应用目录的 window-state.json：%v", err)
	}
	if _, err := os.Stat(StatePath(dir) + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应该已改名：%v", err)
	}
	loaded, err := store.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["main"] != saved["main"] {
		t.Fatalf("读回 %+v，写入 %+v", loaded["main"], saved["main"])
	}
}

func TestWindowStateLoadIsTolerant(t *testing.T) {
	store, _ := newTestStore(t, primaryScreen)
	if states, err := store.LoadWindowStates(); err != nil || len(states) != 0 {
		t.Fatalf("文件不存在应返回空：%v %v", states, err)
	}
	for name, content := range map[string]string{
		"损坏的 JSON": "{not json",
		"空文件":      "",
		"类型不对":     `{"main": "oops"}`,
		"根是数组":     `[1,2,3]`,
		"零尺寸":      `{"main":{"x":0,"y":0,"width":0,"height":0}}`,
		"负尺寸":      `{"main":{"x":0,"y":0,"width":-5,"height":600}}`,
		"离谱的大尺寸":   `{"main":{"x":0,"y":0,"width":99999999,"height":800}}`,
		"离谱的坐标":    `{"main":{"x":999999999,"y":0,"width":1000,"height":800}}`,
		"二进制垃圾":    "\x00\x01\x02",
	} {
		if err := os.WriteFile(store.path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		states, err := store.LoadWindowStates()
		if err != nil {
			t.Errorf("%s：不该返回错误（会拦着窗口打开）：%v", name, err)
		}
		if _, ok := states["main"]; ok {
			t.Errorf("%s：应当被当作没有记录，得到 %+v", name, states["main"])
		}
	}
}

func TestWindowStateSaveToUnwritableLocationReturnsErrorWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &fileStateStore{path: filepath.Join(blocker, "window-state.json")}
	if err := store.SaveWindowStates(map[string]mygo.WindowState{"main": {Width: 1000, Height: 700}}); err == nil {
		t.Fatalf("目录建不出来时应返回错误（MyGo 下次再试），而不是静默成功")
	}
}

func TestNormalizeStateRaisesToMinimumSize(t *testing.T) {
	got, ok := NormalizeState(mygo.WindowState{X: 50, Y: 50, Width: 400, Height: 300}, primaryScreen, Size{960, 640})
	if !ok || got.Width != 960 || got.Height != 640 || got.X != 50 || got.Y != 50 {
		t.Fatalf("小于最小尺寸应抬到最小尺寸并保持位置：%+v %v", got, ok)
	}
}

func TestNormalizeStateShrinksToWorkArea(t *testing.T) {
	got, ok := NormalizeState(mygo.WindowState{X: 0, Y: 0, Width: 5000, Height: 3000}, primaryScreen, Size{960, 640})
	if !ok || got.Width != 1920 || got.Height != 1040 {
		t.Fatalf("比屏幕还大应缩到工作区：%+v", got)
	}
}

func TestNormalizeStateCentersWhenOffScreen(t *testing.T) {
	// 拔掉了右边的外接显示器：上次的位置在 3000 之外
	got, ok := NormalizeState(mygo.WindowState{X: 3200, Y: 100, Width: 1280, Height: 860, Maximized: true}, primaryScreen, Size{960, 640})
	if !ok {
		t.Fatal("尺寸合法，状态应保留")
	}
	if got.X != (1920-1280)/2 || got.Y != (1040-860)/2 {
		t.Fatalf("屏幕外应回退到主屏幕居中：%+v", got)
	}
	if got.Width != 1280 || got.Height != 860 || !got.Maximized {
		t.Fatalf("尺寸和最大化状态应保留：%+v", got)
	}
	// 完全在左上角之外（负坐标，没有左边的显示器）
	got, _ = NormalizeState(mygo.WindowState{X: -4000, Y: -3000, Width: 1280, Height: 860}, primaryScreen, Size{960, 640})
	if got.X != 320 || got.Y != 90 {
		t.Fatalf("负坐标的屏幕外也应居中：%+v", got)
	}
}

func TestNormalizeStateKeepsPositionOnSecondaryMonitor(t *testing.T) {
	areas := []mygo.Rectangle{{X: 0, Y: 0, Width: 1920, Height: 1040}, {X: -1280, Y: 0, Width: 1280, Height: 1024}}
	state := mygo.WindowState{X: -1200, Y: 40, Width: 1000, Height: 700}
	got, ok := NormalizeState(state, areas, Size{960, 640})
	if !ok || got != state {
		t.Fatalf("在副屏上的窗口位置应保留：%+v", got)
	}
}

func TestNormalizeStateCentersWhenOnlyTitlebarSliverRemains(t *testing.T) {
	// 只剩 20 像素在屏幕里：抓不到标题栏，当作屏幕外
	got, _ := NormalizeState(mygo.WindowState{X: 1900, Y: 100, Width: 1280, Height: 860}, primaryScreen, Size{960, 640})
	if got.X == 1900 {
		t.Fatalf("只露出一条边应回退居中：%+v", got)
	}
}

func TestNormalizeStateWithoutDisplaysOnlyChecksSize(t *testing.T) {
	state := mygo.WindowState{X: 5000, Y: 5000, Width: 1200, Height: 800, FullScreen: true}
	got, ok := NormalizeState(state, nil, Size{960, 640})
	if !ok || got.X != 5000 || got.FullScreen {
		t.Fatalf("没有显示器信息时不动位置，但全屏状态要清掉：%+v", got)
	}
	if _, ok := NormalizeState(mygo.WindowState{Width: 0, Height: 0}, nil, Size{960, 640}); ok {
		t.Fatalf("零尺寸必须丢弃")
	}
}

func TestStateFileIsReadableJSONKeyedByMain(t *testing.T) {
	store, dir := newTestStore(t, primaryScreen)
	if err := store.SaveWindowStates(map[string]mygo.WindowState{stateKey: {X: 1, Y: 2, Width: 1000, Height: 700}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(StatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["main"]["width"].(float64) != 1000 {
		t.Fatalf("文件内容不对：%s", raw)
	}
}
