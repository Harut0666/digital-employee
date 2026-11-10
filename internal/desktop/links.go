package desktop

import (
	"net"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
)

// IsExternalURL 一个导航目标是不是“外部链接”：http / https 且主机不是本机（localhost、127.0.0.0/8、::1）。
// 外部链接交给系统默认浏览器，窗口里只留本应用自己的页面。
// 其它协议（about:、blob:、data: 等）和解析不了的地址不算外部链接，保持原样。
func IsExternalURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

// contextMenuKeep 右键菜单里保留的系统项：编辑（撤销、重做、剪切、复制、粘贴、删除、全选）和复制链接 / 图片。
// 其余（重新加载、后退、前进、另存为、打印、检查元素、分享、翻译等）对普通用户没用，一律去掉。
var contextMenuKeep = map[string]bool{
	mygo.ContextMenuUndo:               true,
	mygo.ContextMenuRedo:               true,
	mygo.ContextMenuCut:                true,
	mygo.ContextMenuCopy:               true,
	mygo.ContextMenuPaste:              true,
	mygo.ContextMenuPasteAndMatchStyle: true,
	mygo.ContextMenuDelete:             true,
	mygo.ContextMenuSelectAll:          true,
	mygo.ContextMenuCopyLink:           true,
	mygo.ContextMenuCopyImage:          true,
	mygo.ContextMenuSpellingGuess:      true,
}

// menuEntry 菜单项的最小描述（只为了能脱离 MyGo 测试保留 / 隐藏的决定）。
type menuEntry struct {
	ID        string
	Separator bool
}

// planContextMenu 决定每一项是否显示：不在保留名单里的隐藏；分隔线只在两个可见项之间留一条
// （开头、结尾和连续的分隔线都隐藏）。
func planContextMenu(entries []menuEntry) []bool {
	visible := make([]bool, len(entries))
	for index, entry := range entries {
		if !entry.Separator {
			visible[index] = contextMenuKeep[entry.ID]
		}
	}
	lastVisibleIsItem := false
	pendingSeparator := -1
	for index, entry := range entries {
		if !entry.Separator {
			if visible[index] {
				if pendingSeparator >= 0 && lastVisibleIsItem {
					visible[pendingSeparator] = true
				}
				pendingSeparator = -1
				lastVisibleIsItem = true
			}
			continue
		}
		if lastVisibleIsItem {
			pendingSeparator = index
		}
	}
	return visible
}

// trimContextMenu 按 planContextMenu 的决定整理页面的右键菜单；一项都不剩时不弹菜单。
func trimContextMenu(event *mygo.ContextMenuEvent) {
	if event.Menu == nil {
		return
	}
	items := event.Menu.Items()
	entries := make([]menuEntry, len(items))
	for index, item := range items {
		entries[index] = menuEntry{ID: item.ID, Separator: item.Type == mygo.MenuItemSeparator}
	}
	visible := planContextMenu(entries)
	shown := 0
	for index, item := range items {
		if visible[index] {
			if !entries[index].Separator {
				shown++
			}
			continue
		}
		// 直接改字段：这个菜单只显示一次，不需要通知后端刷新（SetVisible 会往后端发更新）
		item.Hidden = true
	}
	if shown == 0 {
		event.PreventDefault()
	}
}
