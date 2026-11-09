import { useEffect, useMemo, useState } from "react";
import { App as AntApp, Button, Dropdown, Input, Modal, Segmented, Select, Switch, Tooltip } from "antd";
import {
  DownOutlined,
  FolderOpenOutlined,
  MoreOutlined,
  PlusOutlined,
  RightOutlined,
  SettingOutlined,
  UserOutlined,
} from "@ant-design/icons";
import type { RecordItem, SessionSummary, UiRole } from "../api";
import type { WorkspaceApi, WorkspaceList } from "../api/workspaces";
import { groupRecords, groupSessionsByWorkspace, recordSubtitle, runLabel, unreadOf, type WorkspaceGroup } from "../view/sidebar-tree";
import { ROLE_LABELS } from "../view/permissions";

/** 工作区列表，以及会话的重命名和归档 */
export interface SidebarWorkspace {
  list: WorkspaceList | undefined;
  renameSession(id: string, title: string): Promise<unknown>;
  archiveSession(id: string, archived: boolean): Promise<unknown>;
}

export type Selection = { kind: "new"; workspaceId?: string } | { kind: "session"; id: string } | { kind: "page"; page: "config" | "settings" | "knowledge" };

// 事件回放界面已取消（回放保留在命令行 `de replay`）；任务的执行过程改由对话框顶部的“轨迹”展示。
const PAGES = [
  { page: "knowledge", label: "知识库", icon: <FolderOpenOutlined /> },
  { page: "config", label: "员工配置", icon: <UserOutlined /> },
] as const;

/** 侧栏 tab：工作区（默认）| 记录 */
export type SidebarTab = "workspaces" | "records";

const clock = (iso: string) => {
  const d = new Date(iso);
  return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`;
};

const DOT_TITLE: Record<string, string> = { waiting: "等你处理", running: "运行中", done: "已完成未读", idle: "空闲" };

function Dot({ dot, title }: { dot: string; title?: string }) {
  return <span className={`dot ${dot}`} title={title ?? DOT_TITLE[dot] ?? "空闲"} aria-label={title ?? DOT_TITLE[dot] ?? "空闲"} />;
}

/** 工作区列表 → 全部会话：各工作区自带的加上末尾的未归属。 */
export function flattenSessions(list: WorkspaceList | undefined): SessionSummary[] {
  if (!list) return [];
  const known = new Set(list.workspaces.map((w) => w.id));
  const owned = list.workspaces.flatMap((w) => w.sessions ?? []);
  return [...owned, ...(list.unassigned ?? []).filter((s) => !known.has(s.workspaceId))];
}

/** 一个人改名的输入框：回车或确定后交给 onOk。 */
function NameEditor({ open, title, initial, onOk, onCancel }: { open: boolean; title: string; initial: string; onOk: (name: string) => void; onCancel: () => void }) {
  const [value, setValue] = useState(initial);
  useEffect(() => {
    if (open) setValue(initial);
  }, [open, initial]);
  const ok = () => {
    if (value.trim()) onOk(value.trim());
  };
  return (
    <Modal open={open} title={title} onCancel={onCancel} onOk={ok} okText="确定" cancelText="取消" destroyOnHidden>
      <Input value={value} onChange={(e) => setValue(e.target.value)} onPressEnter={ok} maxLength={60} autoFocus />
    </Modal>
  );
}

export function Sidebar({
  role,
  onRole,
  selection,
  onSelect,
  api,
  workspace,
  onRefresh,
  includeArchived,
  onIncludeArchived,
  tab,
  onTab,
  groups,
  error,
  onStartChat,
}: {
  role: UiRole;
  onRole: (r: UiRole) => void;
  selection: Selection;
  onSelect: (s: Selection) => void;
  /** 工作区与记录接口 */
  api: WorkspaceApi;
  /** 工作区列表（带会话摘要和未归属）和会话的重命名、归档 */
  workspace: SidebarWorkspace;
  onRefresh: () => void;
  includeArchived: boolean;
  onIncludeArchived: (value: boolean) => void;
  tab: SidebarTab;
  onTab: (tab: SidebarTab) => void;
  /** 记录 tab 的分组（后端给的 group，展示前再按固定顺序排） */
  groups: { key: string; label: string; items: RecordItem[] }[];
  error?: string;
  /** 在某个工作区下开新对话；不给时在第一个工作区（还没有工作区时由服务端取唯一的工作区） */
  onStartChat: (workspaceId?: string) => void;
}) {
  const { message } = AntApp.useApp();
  const [collapsed, setCollapsed] = useState<string[]>([]);
  const [renaming, setRenaming] = useState<{ id: string; name: string }>();
  const [creating, setCreating] = useState(false);
  const [newWorkspace, setNewWorkspace] = useState({ name: "", path: "" });
  const [renamingSession, setRenamingSession] = useState<{ id: string; title: string }>();

  const list = workspace.list;
  // 工作区列表里带每个工作区的会话，未归属单独一组
  const sessions = useMemo(() => flattenSessions(list), [list]);
  const tree = useMemo(
    () => groupSessionsByWorkspace(list?.workspaces ?? [], sessions, collapsed, { includeArchived, currentId: selection.kind === "session" ? selection.id : undefined }),
    [list, sessions, collapsed, includeArchived, selection],
  );
  const allRecords = useMemo(() => groups.flatMap((g) => g.items), [groups]);
  const recordGroups = useMemo(() => groupRecords(allRecords), [allRecords]);

  const fail = (e: unknown, fallback: string) => message.error(e instanceof Error ? e.message : fallback);

  const renameWorkspace = async (id: string, name: string) => {
    const res = await api.rename(id, name);
    if (!res.ok) return message.error(res.message);
    setRenaming(undefined);
    onRefresh();
  };
  const unregister = (id: string, name: string) => {
    Modal.confirm({
      title: `取消登记“${name}”？`,
      content: "只取消登记，目录里的文件、会话和记录都保留；原来的会话会归到“未归属”。",
      okText: "取消登记",
      okButtonProps: { danger: true },
      cancelText: "再想想",
      onOk: async () => {
        const res = await api.unregister(id);
        if (!res.ok) return message.error(res.message);
        onRefresh();
      },
    });
  };
  const openFolder = async (id: string) => {
    const res = await api.open(id);
    if (!res.ok) message.error(res.message);
  };
  const renameSession = async (id: string, title: string) => {
    try {
      await workspace.renameSession(id, title);
      setRenamingSession(undefined);
      onRefresh();
    } catch (e) {
      fail(e, "重命名失败");
    }
  };
  const archiveSession = async (id: string, archived: boolean) => {
    try {
      await workspace.archiveSession(id, archived);
      onRefresh();
    } catch (e) {
      fail(e, archived ? "归档失败" : "取消归档失败");
    }
  };

  const createWorkspace = async () => {
    const res = await api.create(newWorkspace.name, newWorkspace.path.trim() || undefined);
    if (!res.ok) return message.error(res.message);
    setCreating(false);
    setNewWorkspace({ name: "", path: "" });
    onRefresh();
  };

  const groupHead = (group: WorkspaceGroup) => {
    const isUnassigned = group.workspace === undefined;
    const more = group.workspace && !list?.readOnly && [
      { key: "rename", label: "重命名" },
      { key: "unregister", label: "取消登记" },
      { key: "open", label: "在资源管理器中打开" },
    ];
    return (
      <div className={`group-head ws-head ${group.hasCurrent ? "current" : ""}`}>
        <span
          className="ws-fold"
          onClick={() => setCollapsed((c) => (c.includes(group.key) ? c.filter((k) => k !== group.key) : [...c, group.key]))}
        >
          {group.expanded ? <DownOutlined style={{ fontSize: 11 }} /> : <RightOutlined style={{ fontSize: 11 }} />}
        </span>
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={group.workspace?.path}>
          {group.label}
          {group.workspace?.missing ? <span className="sub"> · 目录缺失</span> : null}
          <span className="sub"> （{group.count}）</span>
        </span>
        {!isUnassigned && (
          <Tooltip title="在这里开新对话">
            <Button size="small" type="text" className="ws-action" icon={<PlusOutlined />} aria-label={`在${group.label}开新对话`} onClick={() => onStartChat(group.workspace?.id)} />
          </Tooltip>
        )}
        {more && (
          <Dropdown
            trigger={["click"]}
            menu={{
              items: more,
              onClick: ({ key }) => {
                if (!group.workspace) return;
                if (key === "rename") setRenaming({ id: group.workspace.id, name: group.label });
                if (key === "unregister") unregister(group.workspace.id, group.label);
                if (key === "open") void openFolder(group.workspace.id);
              },
            }}
          >
            <Button size="small" type="text" className="ws-action" icon={<MoreOutlined />} aria-label={`${group.label}的更多操作`} />
          </Dropdown>
        )}
      </div>
    );
  };

  return (
    <div className="col surface sidebar">
      {/* Spec 0006：左上角只有设置入口 */}
      <div className="side-top">
        <Tooltip title="设置">
          <Button
            aria-label="设置"
            icon={<SettingOutlined />}
            onClick={() => onSelect({ kind: "page", page: "settings" })}
            type={selection.kind === "page" && selection.page === "settings" ? "primary" : "default"}
          />
        </Tooltip>
        <span style={{ flex: 1 }} />
        {tab === "workspaces" && (
          <label className="side-switch" title="显示已归档的会话">
            <Switch size="small" checked={includeArchived} onChange={onIncludeArchived} aria-label="显示已归档" />
            显示已归档
          </label>
        )}
      </div>
      <Segmented
        block
        size="small"
        value={tab}
        onChange={(value) => onTab(value as SidebarTab)}
        options={[
          { label: "工作区", value: "workspaces" },
          { label: "记录", value: "records" },
        ]}
      />
      {error && <div className="side-error">{error}</div>}
      <div className="sessions">
        {tab === "workspaces" ? (
          <>
            {tree.map((group) => (
              <div key={group.key}>
                <div className="side-label">{group.label}</div>
                {group.expanded &&
                  group.sessions.map((s) => (
                    <SessionRow
                      key={s.id}
                      session={s}
                      active={selection.kind === "session" && selection.id === s.id}
                      onOpen={() => onSelect({ kind: "session", id: s.id })}
                      onRename={() => setRenamingSession({ id: s.id, title: s.title })}
                      onArchive={(archived) => void archiveSession(s.id, archived)}
                    />
                  ))}
              </div>
            ))}
            {tree.length === 0 && <div className="side-empty">还没有工作区</div>}
            {!list?.readOnly ? (
              <Button type="dashed" block icon={<PlusOutlined />} style={{ marginTop: 8 }} onClick={() => setCreating(true)}>
                添加工作区
              </Button>
            ) : (
              <div className="side-empty">{list?.reason ?? "工作区由环境变量指定，不能添加"}</div>
            )}
          </>
        ) : (
          <>
            {recordGroups.map((group) => (
              <div key={group.key}>
                <div className="side-label">
                  {group.label}（{group.items.length}）
                </div>
                {group.items.map((item) => (
                  <RecordRow key={item.taskId} item={item} run={runLabel(item, allRecords)} onOpen={item.sessionId ? () => onSelect({ kind: "session", id: item.sessionId! }) : undefined} />
                ))}
              </div>
            ))}
            {recordGroups.every((g) => g.items.length === 0) && <div className="side-empty">还没有开工的任务</div>}
          </>
        )}
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
          prefix={
            <>
              <UserOutlined /> 角色：
            </>
          }
          options={(Object.keys(ROLE_LABELS) as UiRole[]).map((r) => ({ value: r, label: ROLE_LABELS[r] }))}
        />
      </div>
      <NameEditor
        open={renaming !== undefined}
        title="重命名工作区"
        initial={renaming?.name ?? ""}
        onOk={(name) => renaming && void renameWorkspace(renaming.id, name)}
        onCancel={() => setRenaming(undefined)}
      />
      <NameEditor
        open={renamingSession !== undefined}
        title="重命名会话"
        initial={renamingSession?.title ?? ""}
        onOk={(title) => renamingSession && void renameSession(renamingSession.id, title)}
        onCancel={() => setRenamingSession(undefined)}
      />
      <Modal
        open={creating}
        title="添加工作区"
        okText="添加"
        cancelText="取消"
        okButtonProps={{ disabled: !newWorkspace.name.trim() }}
        onOk={() => void createWorkspace()}
        onCancel={() => setCreating(false)}
        destroyOnHidden
      >
        <p className="chat-sub">工作区就是一个本机目录：选一个目录，起个名字。设置、员工和知识库是所有工作区共用的。</p>
        <Input placeholder="名称（缺省取目录名）" value={newWorkspace.name} onChange={(e) => setNewWorkspace({ ...newWorkspace, name: e.target.value })} maxLength={40} style={{ marginBottom: 8 }} />
        <Input placeholder="目录，如 D:\\项目\\华东区（留空时在应用目录下新建）" value={newWorkspace.path} onChange={(e) => setNewWorkspace({ ...newWorkspace, path: e.target.value })} />
      </Modal>
    </div>
  );
}

function SessionRow({
  session,
  active,
  onOpen,
  onRename,
  onArchive,
}: {
  session: SessionSummary;
  active: boolean;
  onOpen: () => void;
  onRename: () => void;
  onArchive: (archived: boolean) => void;
}) {
  const unread = unreadOf(session);
  return (
    <div
      className={`session-item ${active ? "active" : ""} ${session.archived ? "archived" : ""}`}
      onClick={onOpen}
      onContextMenu={(e) => {
        e.preventDefault();
        onRename();
      }}
    >
      <Dot dot={session.archived ? "default" : session.statusDot === "done" && !unread ? "idle" : session.statusDot} title={DOT_TITLE[session.statusDot]} />
      <span className="main">
        <span className={`title ${unread ? "unread" : ""}`}>
          {session.title}
          {unread && <span className="unread-mark" aria-label="已完成未读" />}
        </span>
        <span className="sub">{session.statusText ?? ""}</span>
      </span>
      <span className="time">{clock(session.updatedAt)}</span>
      <Dropdown
        trigger={["click"]}
        menu={{
          items: [
            { key: "rename", label: "重命名" },
            { key: "archive", label: session.archived ? "取消归档" : "归档" },
          ],
          onClick: ({ key, domEvent }) => {
            domEvent.stopPropagation();
            if (key === "rename") onRename();
            if (key === "archive") onArchive(!session.archived);
          },
        }}
      >
        <Button size="small" type="text" className="session-more" icon={<MoreOutlined />} aria-label="会话操作" onClick={(e) => e.stopPropagation()} />
      </Dropdown>
    </div>
  );
}

function RecordRow({ item, run, onOpen }: { item: RecordItem; run?: string; onOpen?: () => void }) {
  return (
    <div className={`session-item record-item ${onOpen ? "" : "readonly"}`} onClick={onOpen} title={item.statusText}>
      <Dot dot={item.statusDot} />
      <span className="main">
        <span className="title">{item.title}</span>
        <span className="sub">{[run, recordSubtitle(item)].filter((part) => part).join(" · ")}</span>
      </span>
      <span className="time">{clock(item.updatedAt)}</span>
    </div>
  );
}
