import { useState } from "react";
import { Select } from "antd";
import {
  DownOutlined,
  PlusOutlined,
  RightOutlined,
  RobotOutlined,
  SettingOutlined,
  ToolOutlined,
  UserOutlined,
  BookOutlined,
} from "@ant-design/icons";
import { Button } from "antd";
import type { Role, TaskSummary } from "@de/core/contract";
import type { EmployeeInfo, UiRole } from "../api";
import { ROLE_LABELS } from "../view/permissions";
import { groupSessions } from "../view/session-groups";

export type Selection =
  | { kind: "new"; employee: string }
  | { kind: "task"; id: string }
  | { kind: "page"; page: "config" | "settings" };

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
  employees,
  tasks,
  role,
  onRole,
  selection,
  onSelect,
}: {
  employees: EmployeeInfo[];
  tasks: TaskSummary[];
  role: UiRole;
  onRole: (r: UiRole) => void;
  selection: Selection;
  onSelect: (s: Selection) => void;
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const groups = groupSessions(tasks, role as Role);
  return (
    <div className="col surface sidebar">
      <div className="brand">
        <div className="logo"><RobotOutlined /></div>
        迷你数字员工
      </div>
      <div className="side-title">员工</div>
      {employees.map((e) => {
        const active = selection.kind === "new" && selection.employee === e.id;
        return (
          <div key={e.id} className={`emp-card ${active ? "active" : ""}`} onClick={() => onSelect({ kind: "new", employee: e.id })}>
            <div className="emp-head">
              <div className={`emp-avatar ${e.id === "weekly-report" ? "" : "b"}`}>{e.id === "weekly-report" ? <RobotOutlined /> : <BookOutlined />}</div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="emp-name">{e.name}</div>
                <div className="emp-desc">{e.description}</div>
              </div>
              <RightOutlined style={{ color: "var(--muted)" }} />
            </div>
            <Button block icon={<PlusOutlined />} style={{ marginTop: 8, color: "var(--primary)" }} onClick={(ev) => { ev.stopPropagation(); onSelect({ kind: "new", employee: e.id }); }}>
              新对话
            </Button>
          </div>
        );
      })}
      <div className="side-title">会话</div>
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
                <div key={t.id} className={`session-item ${selection.kind === "task" && selection.id === t.id ? "active" : ""}`} onClick={() => onSelect({ kind: "task", id: t.id })}>
                  <span className={`dot ${t.dot}`} />
                  <span className="title">{t.title}</span>
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
