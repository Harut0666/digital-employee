// 工作区与记录（Spec 0006“工作区与记录接口”的 T38 部分）。
//
// 工作区的登记由宿主（internal/app）负责，这里只读服务端拿到的登记资料，把会话按工作区分组：
// 每个工作区带会话摘要（状态点由 internal/session 按读的人的角色算出），取消登记的会话进“未归属”。
package server

import (
	"os"
	"sort"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// unassignedKey 未归属分组的键。会话所属的工作区取消登记后归到这里。
const unassignedKey = ""

// workspaceSummaryView 工作区列表里的一项：登记资料加上会话摘要。
type workspaceSummaryView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	UpdatedAt   string            `json:"updatedAt,omitempty"`
	Missing     bool              `json:"missing"`
	MissingText string            `json:"missingText,omitempty"`
	Sessions    []session.Summary `json:"sessions"`
}

// workspaceListView GET /api/workspaces 的响应。
type workspaceListView struct {
	ReadOnly bool   `json:"readOnly"`
	Reason   string `json:"reason,omitempty"`
	// Workspaces 登记过的工作区，按登记的顺序
	Workspaces []workspaceSummaryView `json:"workspaces"`
	// Unassigned 所属工作区已取消登记的会话，放在最后
	Unassigned []session.Summary `json:"unassigned"`
}

// workspaceView 拼出工作区列表。withSessions 为 false 时只回登记资料（宿主用来回新建、重命名的结果）。
func (s *Server) workspaceView(role contract.Role, includeArchived, withSessions bool) workspaceListView {
	items := s.registeredWorkspaces()
	// 取消登记的工作区不再列出，但名字留着：它们的会话在未归属分组里仍显示原来的工作区名
	s.workspaceMu.RLock()
	names := make(map[string]string, len(s.workspaces.names))
	for id, name := range s.workspaces.names {
		names[id] = name
	}
	s.workspaceMu.RUnlock()
	list := workspaceListView{
		ReadOnly:   !s.opts.WorkspaceWritable,
		Reason:     s.opts.WorkspaceReadOnlyReason,
		Workspaces: make([]workspaceSummaryView, 0, len(items)),
		Unassigned: []session.Summary{},
	}
	for _, item := range items {
		names[item.ID] = item.Name
		view := workspaceSummaryView{
			ID: item.ID, Name: item.Name, Path: item.Path,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Sessions: []session.Summary{},
		}
		if _, err := os.Stat(item.Path); err != nil {
			// 目录不存在：登记保留，会话仍可查看
			view.Missing = true
			view.MissingText = "目录缺失"
		}
		list.Workspaces = append(list.Workspaces, view)
	}
	if !withSessions {
		return list
	}
	summaries, err := s.sessions.Summarize(role, func(id string) string { return names[id] }, includeArchived)
	if err != nil {
		// 读不出会话时仍然列出工作区，只是没有会话
		return list
	}
	index := make(map[string]int, len(list.Workspaces))
	for position, item := range list.Workspaces {
		index[item.ID] = position
	}
	for _, summary := range summaries {
		if position, ok := index[summary.WorkspaceID]; ok {
			list.Workspaces[position].Sessions = append(list.Workspaces[position].Sessions, summary)
			continue
		}
		list.Unassigned = append(list.Unassigned, summary)
	}
	return list
}

// recordGroupView 记录 tab 的一组。
type recordGroupView struct {
	Key   string           `json:"key"`
	Label string           `json:"label"`
	Items []session.Record `json:"items"`
}

// recordsListView GET /api/records 的响应。
type recordsListView struct {
	Groups []recordGroupView `json:"groups"`
}

// recordsView 把记录按状态分组，组的顺序固定，空的组也列出来；每组里按更新时间倒序。
func (s *Server) recordsView(records []session.Record) recordsListView {
	names := map[string]string{}
	for _, item := range s.registeredWorkspaces() {
		names[item.ID] = item.Name
	}
	groups := make(map[string][]session.Record, len(session.RecordGroups))
	for _, record := range records {

		if record.WorkspaceName == "" {
			record.WorkspaceName = names[record.WorkspaceID]
		}
		groups[record.Group] = append(groups[record.Group], record)
	}
	// “运行中”里，前台 Agent 正在回复、等我确认的会话排在最前
	for _, items := range groups {
		sort.SliceStable(items, func(i, j int) bool {
			left, right := items[i].StatusDot == session.StatusDotRunning, items[j].StatusDot == session.StatusDotRunning
			if left != right {
				return left
			}
			return items[i].UpdatedAt > items[j].UpdatedAt
		})
	}
	out := recordsListView{Groups: make([]recordGroupView, 0, len(session.RecordGroups))}
	for _, key := range session.RecordGroups {
		items := groups[key]
		if items == nil {
			items = []session.Record{}
		}
		out.Groups = append(out.Groups, recordGroupView{Key: key, Label: session.RecordGroupLabels[key], Items: items})
	}
	return out
}
