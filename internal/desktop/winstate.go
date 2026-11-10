package desktop

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// 窗口状态（大小、位置、是否最大化）存在应用目录的 window-state.json，下次启动恢复。
// 读写都容错：文件不存在、损坏、内容离谱都当作没有记录，用默认大小居中；写失败只丢这一次记录。

// StateFileName 应用目录里的窗口状态文件。
const StateFileName = "window-state.json"

// stateKey 主窗口在状态文件里的键。
const stateKey = "main"

// 窗口状态的合理范围：超出就当文件被改坏了。
const (
	maxStateSize  = 20000
	maxStateCoord = 100000
)

// Size 窗口尺寸（DIP）。
type Size struct{ Width, Height int }

// StatePath 应用目录里的窗口状态文件路径。
func StatePath(appDir string) string {
	return filepath.Join(appDir, StateFileName)
}

// NormalizeState 把记住的状态整理成一个能放心用的窗口位置：
//   - 宽高不是正数或大得离谱：丢弃（ok=false），用默认大小；
//   - 小于最小尺寸的抬到最小尺寸，大于最大工作区的缩到工作区大小；
//   - 窗口（几乎）不在任何一块屏幕的工作区里（拔掉了外接显示器、分辨率变了）：移到主屏幕居中。
//
// workAreas 是各显示器的工作区，主显示器第一个；为空时只做尺寸检查，位置交给 MyGo 自己处理。
func NormalizeState(state mygo.WindowState, workAreas []mygo.Rectangle, minimum Size) (mygo.WindowState, bool) {
	if state.Width <= 0 || state.Height <= 0 || state.Width > maxStateSize || state.Height > maxStateSize {
		return mygo.WindowState{}, false
	}
	if abs(state.X) > maxStateCoord || abs(state.Y) > maxStateCoord {
		return mygo.WindowState{}, false
	}
	state.FullScreen = false // 全屏没有意义（页面没有退出全屏的入口），最大化才记
	state.Width = max(state.Width, minimum.Width)
	state.Height = max(state.Height, minimum.Height)
	if len(workAreas) == 0 {
		return state, true
	}

	primary := workAreas[0]
	// 比整块屏幕还大：缩到主屏幕工作区（不小于最小尺寸）
	largest := primary
	for _, area := range workAreas {
		if area.Width*area.Height > largest.Width*largest.Height {
			largest = area
		}
	}
	state.Width = max(min(state.Width, largest.Width), min(minimum.Width, largest.Width))
	state.Height = max(min(state.Height, largest.Height), min(minimum.Height, largest.Height))

	if visibleOnAny(mygo.Rectangle{X: state.X, Y: state.Y, Width: state.Width, Height: state.Height}, workAreas) {
		return state, true
	}
	state.Width = min(state.Width, primary.Width)
	state.Height = min(state.Height, primary.Height)
	state.X = primary.X + (primary.Width-state.Width)/2
	state.Y = primary.Y + (primary.Height-state.Height)/2
	return state, true
}

// visibleOnAny 窗口有没有足够多的部分落在某块屏幕的工作区里（标题栏能抓到、能拖回来）。
func visibleOnAny(window mygo.Rectangle, workAreas []mygo.Rectangle) bool {
	for _, area := range workAreas {
		overlapWidth := min(area.X+area.Width, window.X+window.Width) - max(area.X, window.X)
		overlapHeight := min(area.Y+area.Height, window.Y+window.Height) - max(area.Y, window.Y)
		if overlapWidth >= min(120, window.Width) && overlapHeight >= min(60, window.Height) {
			return true
		}
	}
	return false
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// fileStateStore 把 MyGo 的窗口状态存进应用目录的 window-state.json。
type fileStateStore struct {
	path    string
	minimum Size
	// workAreas 当前各显示器的工作区；nil 时不检查位置
	workAreas func() []mygo.Rectangle
}

// LoadWindowStates 读状态文件；文件不存在、损坏或内容离谱都返回空（窗口用默认选项）。
func (s *fileStateStore) LoadWindowStates() (map[string]mygo.WindowState, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, nil // 读不了（权限、被占用）也不拦着窗口打开
	}
	var states map[string]mygo.WindowState
	if err := json.Unmarshal(raw, &states); err != nil {
		return nil, nil
	}
	var areas []mygo.Rectangle
	if s.workAreas != nil {
		areas = s.workAreas()
	}
	cleaned := map[string]mygo.WindowState{}
	for key, state := range states {
		if normalized, ok := NormalizeState(state, areas, s.minimum); ok {
			cleaned[key] = normalized
		}
	}
	return cleaned, nil
}

// SaveWindowStates 先写临时文件再改名，写到一半崩溃也不会留下半个文件。
func (s *fileStateStore) SaveWindowStates(states map[string]mygo.WindowState) error {
	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, s.path)
}

// currentWorkAreas 当前各显示器的工作区（主显示器第一个）。
func currentWorkAreas() (areas []mygo.Rectangle) {
	defer func() {
		if recover() != nil {
			areas = nil
		}
	}()
	for _, display := range mygo.Screen.Displays() {
		areas = append(areas, display.WorkArea)
	}
	return areas
}
