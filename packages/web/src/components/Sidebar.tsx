import { useState } from "react";
import { Segmented, Select } from "antd";
import { DownOutlined, PlusOutlined, RightOutlined, RobotOutlined, SettingOutlined, ToolOutlined, UserOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { SessionSummary, UiRole } from "../api";
import { ROLE_LABELS } from "../view/permissions";
import { groupSessions, groupTodos } from "../view/session-groups";

export type Selection = { kind: "new" } | { kind: "session"; id: string } | { kind: "page"; page: "config" | "settings" };

// 事件回放界面已取消（回放保留在命令行 `de replay`）；任务的执行过程改由对话框顶部的“轨迹”展示。
const PAGES = [
  { page: "config", label: "员工配置", icon: <ToolOutlined /> },
  { page: "settings", label: "设置", icon: <SettingOutlined /> },
] as const;

const clock = (iso: string) => {
  const d = new Date(iso);
  return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`;
};

export function Sidebar({
  sessions,
  role,
  onRole,
  selection,
  onSelect,
}: {
  sessions: SessionSummary[];
  role: UiRole;
  onRole: (r: UiRole) => void;
  selection: Selection;
  onSelect: (s: Selection) => void;
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [listView, setListView] = useState<"sessions" | "todos">("sessions");
  const groups = listView === "todos" ? groupTodos(sessions) : groupSessions(sessions, role);
  return (
    <div className="col surface sidebar">
      <div className="brand">
        <div className="logo"><RobotOutlined /></div>
        迷你数字员工
      </div>
      {/* 不再按员工开对话：先说要做什么，由前台 Agent 提议员工、人确认 */}
      <Button type="primary" size="large" block icon={<PlusOutlined />} onClick={() => onSelect({ kind: "new" })} style={{ marginBottom: 8 }}>
        新对话
      </Button>
      <div className="side-title">会话</div>
      <Segmented
        block
        size="small"
        value={listView}
        onChange={setListView}
        options={[
          { label: "我的", value: "sessions" },
          { label: "全部待办", value: "todos" },
        ]}
      />
      <div className="sessions">
        {groups.map((g) => (
          <div key={g.key}>
            <div className="group-head" onClick={() => setCollapsed((c) => ({ ...c, [g.key]: !c[g.key] }))}>
              <span className={`dot ${g.dot}`} />
              <span style={{ flex: 1 }}>{g.label}（{g.items.length}）</span>
              {collapsed[g.key] ? <RightOutlined style={{ fontSize: 11 }} /> : <DownOutlined style={{ fontSize: 11 }} />}
            </div>
            {!collapsed[g.key] &&
              g.items.map((t) => (
                <div key={t.id} className={`session-item ${selection.kind === "session" && selection.id === t.id ? "active" : ""}`} onClick={() => onSelect({ kind: "session", id: t.id })}>
                  <span className={`dot ${t.dot}`} />
                  <span className="main">
                    <span className="title">{t.title}</span>
                    <span className="sub">{t.sub}</span>
                  </span>
                  <span className="time">{clock(t.updatedAt)}</span>
                </div>
              ))}
          </div>
        ))}
      </div>
      <div className="side-foot">
        {PAGES.map((p) => (
          <div key={p.page} className={`side-link ${selection.kind === "page" && selection.page === p.page ? "active" : ""}`} onClick={() => onSelect({ kind: "page", page: p.page })}>
            {p.icon} {p.label}
          </div>
        ))}
        <Select
          style={{ width: "100%", marginTop: 8 }}
          value={role}
          onChange={onRole}
          prefix={<><UserOutlined /> 角色：</>}
          options={(Object.keys(ROLE_LABELS) as UiRole[]).map((r) => ({ value: r, label: ROLE_LABELS[r] }))}
        />
      </div>
    </div>
  );
}
