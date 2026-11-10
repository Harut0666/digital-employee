import { useEffect, useMemo, useRef, useState } from "react";
import { App as AntApp, Button, Dropdown, Input, Modal, Segmented, Select, Skeleton, Tag, Tooltip } from "antd";
import type { InputRef } from "antd";
import {
  BookOutlined,
  CheckOutlined,
  CloseOutlined,
  ContainerOutlined,
  DownOutlined,
  FolderAddOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  HistoryOutlined,
  LockOutlined,
  MoreOutlined,
  PlusOutlined,
  ReloadOutlined,
  RightOutlined,
  RobotOutlined,
  SearchOutlined,
  SettingOutlined,
  SlidersOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import type { RecordItem, SessionSummary, UiRole } from "../api";
import type { WorkspaceApi, WorkspaceList } from "../api/workspaces";
import { flatSessionList, flattenSessions, groupRecords, groupSessionsByWorkspace, recordSubtitle, runLabel, type WorkspaceGroup } from "../view/sidebar-tree";
import { loadViewOptions, saveViewOptions, searchSessions, VIEW_OPTION_GROUPS, type WorkspaceViewOptions } from "../view/workspace-view";
import { currentWorkspaceOf, newChatEntry, type NewChatEntry } from "../view/new-chat";
import { sessionRowView, sessionTime, sessionTone, type Tone } from "../view/session-row";
import { ROLE_LABELS } from "../view/permissions";
import { NewChatButton } from "./NewChatButton";
import { StateBlock } from "./StateBlock";

export { flattenSessions };

/** 工作区列表，以及会话的重命名和归档 */
export interface SidebarWorkspace {
  list: WorkspaceList | undefined;
  renameSession(id: string, title: string): Promise<unknown>;
  archiveSession(id: string, archived: boolean): Promise<unknown>;
}

export type Selection = { kind: "new"; workspaceId?: string } | { kind: "session"; id: string } | { kind: "page"; page: "config" | "settings" | "knowledge" };

// 事件回放界面已取消（回放保留在命令行 `de replay`）；任务的执行过程改由对话框顶部的“轨迹”展示。
const PAGES = [
  { page: "knowledge", label: "知识库", icon: <BookOutlined /> },
  { page: "config", label: "员工配置", icon: <TeamOutlined /> },
  { page: "settings", label: "设置", icon: <SettingOutlined /> },
] as const;

/** 侧栏 tab：工作区（默认）| 记录 */
export type SidebarTab = "workspaces" | "records";

const TONE_TITLE: Record<Tone, string> = { waiting: "等你处理", running: "运行中", done: "已完成", error: "出错", idle: "空闲" };

function Dot({ tone, title }: { tone: Tone; title?: string }) {
  const label = title ?? TONE_TITLE[tone];
  return <span className={`dot ${tone}`} title={label} role="img" aria-label={label} />;
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
      <Input value={value} onChange={(e) => setValue(e.target.value)} onPressEnter={ok} maxLength={60} autoFocus aria-label={title} />
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
  tab,
  onTab,
  groups,
  error,
  onStartChat,
  onAddWorkspace,
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
  tab: SidebarTab;
  onTab: (tab: SidebarTab) => void;
  /** 记录 tab 的分组（后端给的 group，展示前再按固定顺序排） */
  groups: { key: string; label: string; items: RecordItem[] }[];
  error?: string;
  /** 在某个工作区下开新对话；不给时由服务端取唯一的工作区 */
  onStartChat: (workspaceId?: string) => void;
  /** 打开“添加工作区”弹窗；hint 是弹窗里多说的一句 */
  onAddWorkspace: (hint?: string) => void;
}) {
  const { message } = AntApp.useApp();
  const [collapsed, setCollapsed] = useState<string[]>([]);
  const [renaming, setRenaming] = useState<{ id: string; name: string }>();
  const [renamingSession, setRenamingSession] = useState<{ id: string; title: string }>();
  // 视图选项记在本机；搜索关键字只在这次打开期间有效，不持久化
  const [view, setView] = useState<WorkspaceViewOptions>(() => loadViewOptions());
  const [viewOpen, setViewOpen] = useState(false);
  const [showAll, setShowAll] = useState<string[]>([]);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");
  const searchRef = useRef<InputRef>(null);

  const list = workspace.list;
  const currentId = selection.kind === "session" ? selection.id : undefined;
  // 工作区列表里带每个工作区的会话，未归属单独一组
  const sessions = useMemo(() => flattenSessions(list), [list]);
  const tree = useMemo(
    () => groupSessionsByWorkspace(list?.workspaces ?? [], sessions, collapsed, { filter: view.filter, sort: view.sort, currentId, showAll }),
    [list, sessions, collapsed, view.filter, view.sort, currentId, showAll],
  );
  const flatRows = useMemo(() => flatSessionList(list?.workspaces ?? [], sessions, { filter: view.filter, sort: view.sort }), [list, sessions, view.filter, view.sort]);
  const hits = useMemo(() => searchSessions(sessions, list?.workspaces ?? [], query), [sessions, list, query]);
  const noWorkspace = list !== undefined && list.workspaces.length === 0;
  // 没有工作区时，“未归属”为空就不画一个空组；只看已归档时没有归档会话的组也不画
  const visibleTree = tree.filter((group) => (group.workspace !== undefined && view.filter !== "archived") || group.count > 0);
  const emptyNote = view.filter === "archived" ? "没有已归档的会话" : "还没有会话";

  const openSearch = () => {
    setSearching(true);
    // 输入框此刻还是 inert，等下一帧再聚焦
    requestAnimationFrame(() => searchRef.current?.focus());
  };
  const closeSearch = () => {
    setSearching(false);
    setQuery("");
  };
  // 离开工作区 tab 时收起搜索
  useEffect(() => {
    if (tab !== "workspaces") closeSearch();
  }, [tab]);
  const changeView = (patch: Partial<WorkspaceViewOptions>) => {
    const next = { ...view, ...patch };
    setView(next);
    saveViewOptions(next);
  };
  /** 搜索结果里点一行：打开会话，展开它所属的分组 */
  const openHit = (id: string, workspaceId: string) => {
    const groupKey = list?.workspaces.some((w) => w.id === workspaceId) ? workspaceId : "ungrouped";
    setCollapsed((c) => c.filter((k) => k !== groupKey));
    onSelect({ kind: "session", id });
    closeSearch();
  };
  const allRecords = useMemo(() => groups.flatMap((g) => g.items), [groups]);
  const recordGroups = useMemo(() => groupRecords(allRecords), [allRecords]);
  const entry = useMemo(() => newChatEntry(list, currentWorkspaceOf(list, selection)), [list, selection]);

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

  /** 点“新对话”时没有可用的工作区：切到工作区 tab，打开添加弹窗并说明原因 */
  const guideToAdd = (target: Extract<NewChatEntry, { kind: "add-workspace" }>) => {
    if (!target.canAdd) {
      message.info(target.hint ?? "工作区由环境变量指定，不能添加");
      return;
    }
    onTab("workspaces");
    onAddWorkspace(target.reason === "missing" ? "已登记的工作区目录都找不到了。添加一个可用的目录，才能开始新对话。" : "还没有工作区。先添加一个工作区，对话和文件都放在里面。");
  };

  const toggle = (key: string) => setCollapsed((c) => (c.includes(key) ? c.filter((k) => k !== key) : [...c, key]));

  const renderRow = (s: SessionSummary, extra: { workspaceName?: string; stacked?: boolean; onOpen?: () => void } = {}) => (
    <SessionRow
      key={s.id}
      session={s}
      active={currentId === s.id}
      workspaceName={extra.workspaceName}
      stacked={extra.stacked}
      onOpen={extra.onOpen ?? (() => onSelect({ kind: "session", id: s.id }))}
      onRename={() => setRenamingSession({ id: s.id, title: s.title })}
      onArchive={(archived) => void archiveSession(s.id, archived)}
    />
  );

  const groupHead = (group: WorkspaceGroup) => {
    const workspaceInfo = group.workspace;
    const manageable = workspaceInfo && !list?.readOnly;
    const missing = workspaceInfo?.missing === true;
    const more = manageable
      ? [
          { key: "rename", label: "重命名" },
          { key: "open", label: "在资源管理器中打开", disabled: missing },
          { key: "unregister", label: "取消登记", danger: true },
        ]
      : undefined;
    return (
      <div className={`ws-head ${group.hasCurrent ? "current" : ""}`}>
        <button type="button" className="ws-toggle" aria-expanded={group.expanded} onClick={() => toggle(group.key)} title={workspaceInfo?.path}>
          <span className="ws-fold" aria-hidden>
            {group.expanded ? <DownOutlined /> : <RightOutlined />}
          </span>
          {group.expanded ? <FolderOpenOutlined className="ws-icon" /> : <FolderOutlined className="ws-icon" />}
          <span className="ws-name">{group.label}</span>
          {missing && (
            <Tag className="ws-missing" color="warning" variant="filled">
              目录缺失
            </Tag>
          )}
          <span className="ws-count">{group.count}</span>
        </button>
        {workspaceInfo && (
          <Tooltip title={missing ? "目录缺失，不能开新对话" : "在这里开新对话"}>
            <Button
              size="small"
              type="text"
              className="ws-action"
              icon={<PlusOutlined />}
              disabled={missing}
              aria-label={`在${group.label}开新对话`}
              onClick={() => onStartChat(workspaceInfo.id)}
            />
          </Tooltip>
        )}
        {more && (
          <Dropdown
            trigger={["click"]}
            menu={{
              items: more,
              onClick: ({ key }) => {
                if (!workspaceInfo) return;
                if (key === "rename") setRenaming({ id: workspaceInfo.id, name: group.label });
                if (key === "unregister") unregister(workspaceInfo.id, group.label);
                if (key === "open") void openFolder(workspaceInfo.id);
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
    <aside className="col sidebar" aria-label="导航">
      <div className="side-head">
        <span className="logo" aria-hidden>
          <RobotOutlined />
        </span>
        <span className="brand-name">迷你数字员工</span>
      </div>
      <NewChatButton entry={entry} onStart={onStartChat} onAddWorkspace={guideToAdd} />
      <div className={`side-bar ${searching ? "searching" : ""}`}>
        <div className="side-bar-main" inert={searching}>
          <div className="side-bar-tabs">
            {/* 按钮式切换：工作区 | 记录 */}
            <Segmented<SidebarTab>
              size="small"
              value={tab}
              aria-label="侧栏视图"
              options={[
                { value: "workspaces", label: "工作区" },
                { value: "records", label: "记录" },
              ]}
              onChange={onTab}
            />
          </div>
          {tab === "workspaces" && (
            <div className="side-bar-actions">
              <Tooltip title="搜索会话">
                <Button type="text" className="icon-btn" icon={<SearchOutlined />} aria-label="搜索会话" onClick={openSearch} />
              </Tooltip>
              <Dropdown
                trigger={["click"]}
                placement="bottomRight"
                open={viewOpen}
                // 点菜单项不收起：可以连着改几项，点外面才关
                onOpenChange={(next, info) => {
                  if (info.source !== "menu") setViewOpen(next);
                }}
                classNames={{ root: "ws-view-menu" }}
                menu={{
                  selectable: false,
                  items: VIEW_OPTION_GROUPS.map((group) => ({
                    type: "group" as const,
                    key: group.key,
                    label: group.label,
                    children: group.options.map((option) => ({
                      key: `${group.key}:${option.value}`,
                      label: (
                        <span className="view-opt">
                          <span>{option.label}</span>
                          {view[group.key] === option.value && <CheckOutlined aria-label="已选" />}
                        </span>
                      ),
                    })),
                  })),
                  onClick: ({ key }) => {
                    const [field, value] = key.split(":");
                    if (field && value) changeView({ [field]: value } as Partial<WorkspaceViewOptions>);
                  },
                }}
              >
                <Tooltip title="视图选项">
                  <Button type="text" className="icon-btn" icon={<SlidersOutlined />} aria-label="视图选项" />
                </Tooltip>
              </Dropdown>
              <Tooltip title={list?.readOnly ? (list.reason ?? "工作区由环境变量指定，不能添加") : "添加工作区"}>
                <Button
                  type="text"
                  className="icon-btn"
                  icon={<FolderAddOutlined />}
                  aria-label="添加工作区"
                  disabled={list?.readOnly}
                  onClick={() => onAddWorkspace()}
                />
              </Tooltip>
            </div>
          )}
        </div>
        <div className="side-bar-search" inert={!searching}>
          <Input
            ref={searchRef}
            className="side-search-input"
            prefix={<SearchOutlined />}
            placeholder="搜索会话名称"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") closeSearch();
            }}
            aria-label="搜索会话名称"
          />
          <Button type="text" className="icon-btn" icon={<CloseOutlined />} aria-label="清除搜索并收起" onClick={closeSearch} />
        </div>
      </div>
      {error && (
        <div className="side-error" role="alert">
          <span>{error}</span>
          <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onRefresh} aria-label="重新读取" />
        </div>
      )}
      <div className="sessions">
        {tab === "workspaces" ? (
          <>
            {list === undefined && !error && <Skeleton active title={false} paragraph={{ rows: 5 }} className="side-skeleton" />}
            {noWorkspace && (
              <StateBlock
                compact
                tone="brand"
                icon={<FolderAddOutlined />}
                title="先添加一个工作区"
                description={list?.readOnly ? (list.reason ?? "工作区由环境变量指定，不能添加") : "工作区就是一个本机目录，对话和文件都放在里面。添加之后才能开始新对话。"}
                actions={
                  !list?.readOnly && (
                    <Button type="primary" icon={<FolderAddOutlined />} onClick={() => onAddWorkspace()}>
                      添加工作区
                    </Button>
                  )
                }
              />
            )}
            {searching ? (
              <>
                {query.trim() === "" && <div className="side-note">输入会话标题或工作区名称，立即筛选。</div>}
                {query.trim() !== "" && hits.length === 0 && <div className="side-empty search-empty">没有匹配“{query.trim()}”的会话</div>}
                {hits.map((hit) => renderRow(hit.session, { workspaceName: hit.workspaceName, stacked: true, onOpen: () => openHit(hit.session.id, hit.session.workspaceId) }))}
              </>
            ) : view.group === "flat" ? (
              <>
                {flatRows.length === 0 && list !== undefined && !noWorkspace && <div className="side-empty flat-empty">{view.filter === "archived" ? "没有已归档的会话" : "没有会话"}</div>}
                {flatRows.map((row) => renderRow(row.session, { workspaceName: row.workspaceName }))}
              </>
            ) : (
              visibleTree.map((group) => (
                <div key={group.key} className="ws-group">
                  {groupHead(group)}
                  {group.expanded && group.sessions.length === 0 && <div className="side-empty">{group.workspace ? emptyNote : "没有会话"}</div>}
                  {group.expanded && group.sessions.map((s) => renderRow(s))}
                  {group.expanded && group.hidden > 0 && (
                    <button type="button" className="ws-more" onClick={() => setShowAll((all) => [...all, group.key])}>
                      展开 {group.hidden} 条
                    </button>
                  )}
                  {group.expanded && group.showingAll && (
                    <button type="button" className="ws-more" onClick={() => setShowAll((all) => all.filter((k) => k !== group.key))}>
                      收起
                    </button>
                  )}
                  {!group.workspace && group.expanded && group.count > 0 && <div className="side-note">这些会话所属的工作区已取消登记，仍可查看。</div>}
                </div>
              ))
            )}
            {list?.readOnly && !noWorkspace && !searching && <div className="side-note">{list.reason ?? "工作区由环境变量指定，不能添加"}</div>}
          </>
        ) : (
          <>
            {recordGroups
              .filter((group) => group.items.length > 0)
              .map((group) => (
                <div key={group.key} className="ws-group">
                  <div className="side-label">
                    {group.label}
                    <span className="ws-count">{group.items.length}</span>
                  </div>
                  {group.items.map((item) => (
                    <RecordRow key={item.taskId} item={item} run={runLabel(item, allRecords)} onOpen={item.sessionId ? () => onSelect({ kind: "session", id: item.sessionId! }) : undefined} active={item.sessionId !== undefined && selection.kind === "session" && selection.id === item.sessionId} />
                  ))}
                </div>
              ))}
            {recordGroups.every((g) => g.items.length === 0) && (
              <StateBlock compact icon={<HistoryOutlined />} title="还没有开工的任务" description="员工开始工作后，每次运行都会记在这里，按进行中、待审核、已交付分组。" />
            )}
          </>
        )}
      </div>
      <nav className="side-foot" aria-label="常用入口">
        {PAGES.map((p) => (
          <button
            key={p.page}
            type="button"
            className={`side-link ${selection.kind === "page" && selection.page === p.page ? "active" : ""}`}
            aria-current={selection.kind === "page" && selection.page === p.page ? "page" : undefined}
            onClick={() => onSelect({ kind: "page", page: p.page })}
          >
            {p.icon}
            <span>{p.label}</span>
          </button>
        ))}
        <Select
          className="side-role"
          size="small"
          value={role}
          onChange={onRole}
          aria-label="切换角色"
          prefix={<UserOutlined />}
          options={(Object.keys(ROLE_LABELS) as UiRole[]).map((r) => ({ value: r, label: `角色：${ROLE_LABELS[r]}` }))}
        />
      </nav>
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
    </aside>
  );
}

function SessionRow({
  session,
  active,
  workspaceName,
  stacked,
  onOpen,
  onRename,
  onArchive,
}: {
  session: SessionSummary;
  active: boolean;
  /** 单列表与搜索结果里标出所属工作区 */
  workspaceName?: string;
  /** 搜索结果：标题下面再一行工作区名；否则工作区名放在标题后面 */
  stacked?: boolean;
  onOpen: () => void;
  onRename: () => void;
  onArchive: (archived: boolean) => void;
}) {
  const row = sessionRowView(session);
  const time = sessionTime(session.updatedAt);
  const statusLabel = session.statusText || TONE_TITLE[sessionTone(session.statusDot)];
  const archiveLabel = session.archived ? "取消归档" : "归档";
  return (
    <div
      className={`session-item ${active ? "active" : ""} ${session.archived ? "archived" : ""} ${stacked ? "stacked" : ""}`}
      onContextMenu={(e) => {
        e.preventDefault();
        if (row.canRename) onRename();
      }}
    >
      <button type="button" className="session-main" onClick={onOpen} aria-current={active ? "true" : undefined} title={`${session.title}\n${statusLabel}`}>
        <Dot tone={row.tone} title={TONE_TITLE[sessionTone(session.statusDot)]} />
        <span className="main">
          <span className="title-line">
            <span className={`title ${row.unread ? "unread" : ""}`}>{session.title}</span>
            {row.unread && <span className="unread-mark" role="img" aria-label="已完成未读" />}
            {row.readOnly && (
              <span className="ro-badge" title="由旧任务生成的记录：可以查看和归档，不能重命名或继续对话">
                <LockOutlined /> 只读
              </span>
            )}
            {workspaceName && !stacked && <span className="row-ws">{workspaceName}</span>}
          </span>
          {workspaceName && stacked && <span className="sub">{workspaceName}</span>}
        </span>
      </button>
      <span className="session-aside">
        <span className="time" title={session.updatedAt}>
          {time}
        </span>
        <span className="row-actions">
          <Tooltip title={archiveLabel}>
            <Button size="small" type="text" className="icon-btn sm" icon={<ContainerOutlined />} aria-label={`${archiveLabel}：${session.title}`} onClick={() => onArchive(!session.archived)} />
          </Tooltip>
          <Dropdown
            trigger={["click"]}
            menu={{
              items: row.menu.map((item) => ({ key: item.key, label: item.label })),
              onClick: ({ key }) => {
                if (key === "rename") onRename();
                if (key === "archive") onArchive(!session.archived);
              },
            }}
          >
            <Button size="small" type="text" className="icon-btn sm session-more" icon={<MoreOutlined />} aria-label={`${session.title}的更多操作`} />
          </Dropdown>
        </span>
      </span>
    </div>
  );
}
function RecordRow({ item, run, onOpen, active }: { item: RecordItem; run?: string; onOpen?: () => void; active: boolean }) {
  const tone = sessionTone(item.statusDot);
  const subtitle = [run, recordSubtitle(item)].filter((part) => part).join(" · ");
  return (
    <div className={`session-item record-item ${active ? "active" : ""} ${onOpen ? "" : "readonly"}`}>
      <button type="button" className="session-main" onClick={onOpen} disabled={!onOpen} title={item.statusText} aria-current={active ? "true" : undefined}>
        <Dot tone={tone} />
        <span className="main">
          <span className="title-line">
            <span className="title">{item.title}</span>
          </span>
          <span className="sub">{subtitle || item.statusText}</span>
        </span>
      </button>
      <span className="session-aside">
        <span className="time" title={item.updatedAt}>
          {sessionTime(item.updatedAt)}
        </span>
      </span>
    </div>
  );
}
