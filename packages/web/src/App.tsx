import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App as AntApp, Button, ConfigProvider, Drawer, theme } from "antd";
import { XProvider } from "@ant-design/x";
import zhCN from "antd/locale/zh_CN";
import { ExclamationCircleOutlined, LeftOutlined, ReloadOutlined } from "@ant-design/icons";
import type { ApprovalPolicy, TaskCommand, TaskEventType } from "@de/core/contract";
import { createApi, IS_MOCK, type ModelConnectionOption, type ModelRef, type PermissionMode, type SessionResult, type StepStatusView, type TaskSnapshot, type UiRole, type UploadItem } from "./api";
import { createSettingsApi, type SettingsApi } from "./api/settings";
import { createWorkspaces } from "./api";
import { ApiContext, useApi, useEmployees, useFolderFiles, usePreflight, useSession, useSessionStream, useTask, usePastRuns } from "./hooks/data";
import { useRecords, useWorkspaceList } from "./hooks/sidebar";
import { ApprovalPolicyProvider } from "./components/ApprovalPolicy";
import { Sidebar, type Selection, type SidebarTab, type SidebarWorkspace } from "./components/Sidebar";
import { AddWorkspaceModal } from "./components/AddWorkspaceModal";
import { LoadingBlock, StateBlock } from "./components/StateBlock";
import { NewConversation, SessionChat } from "./components/Chat";
import { LaunchPanel } from "./components/LaunchPanel";
import { ReviewPanel } from "./components/ReviewPanel";
import type { TraceFile } from "./components/Trajectory";
import { Handoff, TaskActionBar, type CardActions, type SessionActions } from "./components/Cards";
import { UiPreferencesProvider, useUiPreferences } from "./components/Preferences";
import { EmployeeConfigPage } from "./pages/EmployeeConfig";
import { SettingsPage } from "./pages/Settings";
import { KnowledgePage } from "./pages/Knowledge";
import { actionPlan } from "./view/permissions";
import { deriveChat } from "./view/chat-view";
import { latestTaskId, pastTaskIds } from "./view/task-runs";
import { DRAWER_BELOW, panelLayout } from "./view/review-panel";
import { antdTheme } from "./view/theme";
import { newChatTarget, startWorkspaceId } from "./view/new-chat";

const DEFAULT_SECTIONS = [
  { id: "summary", heading: "摘要" },
  { id: "body", heading: "正文" },
];

/** 会在会话里投影出卡片的任务事件（见 internal/session/cards.go） */
const CARD_EVENTS = new Set<TaskEventType>(["task_created", "state_changed", "qc_result", "review_decision"]);

function Shell({ roleRef, settingsApi }: { roleRef: { current: UiRole }; settingsApi: SettingsApi }) {
  const api = useApi();
  const { message } = AntApp.useApp();
  const employees = useEmployees();
  const [role, setRoleState] = useState<UiRole>(roleRef.current);
  roleRef.current = role;

  const [selection, setSelection] = useState<Selection>({ kind: "new" });
  /** 窄窗口时右栏是抽屉，这里记它是否打开 */
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [panelKey, setPanelKey] = useState(0);
  const windowWidth = useWindowWidth();
  const drawerMode = windowWidth < DRAWER_BELOW;
  // 侧栏两个 tab（T38）：工作区列表带会话摘要和未归属，记录 tab 按状态分组；状态点都由后端算
  const [sidebarTab, setSidebarTab] = useState<SidebarTab>("workspaces");
  const workspaceApi = useMemo(() => createWorkspaces(api), [api]);
  const workspaceState = useWorkspaceList(workspaceApi);
  const recordsState = useRecords(workspaceApi, sidebarTab === "records");
  const refreshSidebar = useCallback(() => {
    workspaceState.refresh();
    recordsState.refresh();
  }, [workspaceState.refresh, recordsState.refresh]);
  const sidebarWorkspace: SidebarWorkspace = useMemo(
    () => ({
      list: workspaceState.list,
      renameSession: (id, title) => api.updateSession(id, { title }),
      archiveSession: (id, archived) => api.updateSession(id, { archived }),
    }),
    [api, workspaceState.list],
  );
  /** “添加工作区”弹窗：侧栏的按钮和新对话页的空状态都从这里打开 */
  const [addWorkspace, setAddWorkspace] = useState<{ open: boolean; hint?: string }>({ open: false });
  const panelRef = useRef<HTMLDivElement>(null);
  const [submitting, setSubmitting] = useState(false);
  /** 材料有变化（确认目录文件、上传）时让 @ 选择重读目录 */
  const [folderVersion, setFolderVersion] = useState(0);
  const { preflight, reload: reloadPreflight } = usePreflight();
  /** 会话可选的模型（输入框右下角的下拉，T39）：设置里变了就重读 */
  const [modelOptions, setModelOptions] = useState<ModelConnectionOption[]>([]);
  useEffect(() => {
    let alive = true;
    api.modelOptions().then(
      (options) => alive && setModelOptions(options),
      () => {},
    );
    return () => {
      alive = false;
    };
  }, [api, selection.kind]);

  const sessionId = selection.kind === "session" ? selection.id : undefined;
  const session = useSession(sessionId);
  const detail = session.detail;
  // @ 选目录文件：会话可写时都有；每次会话读出来（材料变化时重读）刷新一次
  const folderFiles = useFolderFiles(
    sessionId,
    detail !== undefined && !detail.readOnly,
    `${detail?.status}:${detail?.attachments.length}:${folderVersion}:${detail?.updatedAt}`,
  );
  // 会话先后运行过多个任务时，最新的一个是当前任务；早先的已经结束，只用来在对话里留着它们的步骤行和交付物
  const taskId = detail ? latestTaskId(detail) : undefined;
  const task = useTask(taskId);
  const pastRuns = usePastRuns(detail ? pastTaskIds(detail) : []);
  // 服务端快照里的 state 最权威（草稿任务没有 state_changed 事件，视图里可能还是空）
  const taskState = task.view.state ?? task.snapshot?.state;
  const employee = employees.find((e) => e.id === (detail?.employee ?? task.snapshot?.employee));

  // 浏览器标签页跟随当前会话标题（新对话时回到产品名）
  useEffect(() => {
    document.title = detail?.title ? `${detail.title} · 迷你数字员工` : "迷你数字员工";
  }, [detail?.title]);

  // 回到新对话时重查提交前检查（可能刚在设置里填了 Key）
  useEffect(() => {
    if (selection.kind === "new") reloadPreflight();
  }, [selection.kind, reloadPreflight]);

  // 任务卡片是读取会话时从任务事件投影出来的：出现会产生卡片的事件时重读会话
  const cardEvents = task.events.filter((e) => CARD_EVENTS.has(e.type)).length;
  const reloadSession = session.reload;
  useEffect(() => {
    if (cardEvents > 0) reloadSession();
  }, [cardEvents, reloadSession]);

  const fail = useCallback((e: unknown, fallback: string) => message.error(e instanceof Error ? e.message : fallback), [message]);

  const run = useCallback(
    async (cmd: TaskCommand, uploads?: UploadItem[]) => {
      if (!taskId) return;
      try {
        const res = await api.command(taskId, cmd, uploads);
        if (!res.ok) message.error(res.message);
      } catch (e) {
        fail(e, "操作失败");
      }
      refreshSidebar();
      task.reload();
      session.reload();
    },
    [api, taskId, message, refreshSidebar, task, session, fail],
  );

  /** 会话层操作的统一收尾：失败提示，成功后重读会话和列表 */
  const settle = async (pending: Promise<SessionResult>): Promise<SessionResult> => {
    let res: SessionResult;
    try {
      res = await pending;
    } catch (e) {
      res = { ok: false, error: "internal", message: e instanceof Error ? e.message : "操作失败" };
    }
    session.reload();
    refreshSidebar();
    return res;
  };

  /** 在会话里换模型（T39）：写进会话日志；任务运行中从下一个判断步骤生效。切换说明由视图按前后两次插进对话。 */
  const selectModel = async (connection: string, model: string): Promise<boolean> => {
    if (!sessionId) return false;
    const res = await settle(api.selectModel(sessionId, connection, model));
    if (!res.ok) message.error(res.message);
    return res.ok;
  };

  /** 输入框左下角选权限模式（T48）：每个会话各自记住；Agent 等着确认时也能换 */
  const selectMode = async (mode: PermissionMode): Promise<boolean> => {
    if (!sessionId) return false;
    const res = await settle(api.setPermissionMode(sessionId, mode));
    if (!res.ok) message.error(res.message);
    return res.ok;
  };

  const startConversation = async (text: string, model?: ModelRef): Promise<boolean> => {
    try {
      // 新对话开在哪个工作区：选中的“新对话”带着的，没有就取默认（只有一个可用工作区时就是它）；
      // 还没读到登记时交服务端取唯一的工作区（单目录模式）
      const targetWorkspace = startWorkspaceId(workspaceState.list, selection.kind === "new" ? selection.workspaceId : undefined);
      // 新对话页选的模型随建会话发出（T45）：这个会话从第一句话起就用它
      const created = await api.createSession(targetWorkspace, model);
      // 先切过去再看受理结果：回复在整页的会话事件流里逐字出现
      setSelection({ kind: "session", id: created.id });
      const res = await api.sendMessage(created.id, text);
      if (!res.ok) message.error(res.message);
      refreshSidebar();
      return res.ok;
    } catch (e) {
      fail(e, "开始对话失败");
      return false;
    }
  };

  const sessionActions: SessionActions = {
    employees,
    attachments: detail?.attachments ?? [],
    // 员工表单卡上的“确认开工”“不是这个员工”：失败（清单没备齐、模型没配）交给卡片显示，不弹全局提示
    answerForm: async (requestId, answer) => {
      if (!sessionId) return { ok: false, error: "not_found", message: "没有选中会话" };
      const res = await settle(api.answerEmployeeForm(sessionId, requestId, answer));
      refreshSidebar();
      return res;
    },
    async upload(files) {
      if (!sessionId) return;
      const res = await settle(api.uploadAttachments(sessionId, files));
      if (!res.ok) message.error(res.message);
      setFolderVersion((n) => n + 1);
    },
    // 卡片上的“用这个文件”和输入框里的 @ 选择都走这里：人确认后复制进会话，与上传等效
    async useFolderFiles(paths) {
      if (!sessionId) return;
      const res = await settle(api.useFolderFiles(sessionId, paths));
      if (!res.ok) message.error(res.message);
      setFolderVersion((n) => n + 1);
    },
    async draftFiling(filingTaskId) {
      if (!sessionId) return;
      const res = await settle(api.draftFiling(sessionId, filingTaskId));
      if (!res.ok) message.error(res.message);
    },
    async fileKnowledge(filing) {
      const res = await api.fileKnowledge(filing).catch((e: unknown) => ({ ok: false as const, error: "internal", message: e instanceof Error ? e.message : "入库失败" }));
      if (res.ok) message.success(`已放入知识库：${res.entry.title}`);
      else message.error(res.message);
      session.reload();
    },
    openKnowledge: () => setSelection({ kind: "page", page: "knowledge" }),
    // 权限确认卡上的三个按钮：答复之后这一轮接着往下走，会话由 settle 重读
    async decidePermission(requestId, decision) {
      if (!sessionId) return;
      const res = await settle(api.decidePermission(sessionId, requestId, decision));
      if (!res.ok) message.error(res.message);
    },
  };

  // 正在说的一轮：说的话逐字出现、工具调用逐个列出；一轮结束时重读会话（完整消息和卡片这一刻才写下来）
  const stream = useSessionStream(sessionId, session.reload);

  /** 发一句话：接口立即受理，回复在会话事件流里出现。正在说的时候再发，排进这一轮 */
  const send = async (text: string): Promise<boolean> => {
    if (!sessionId) return false;
    const res = await api.sendMessage(sessionId, text).catch((e: unknown) => ({ ok: false as const, error: "internal", message: e instanceof Error ? e.message : "发送失败" }));
    if (!res.ok) message.error(res.message);
    refreshSidebar();
    return res.ok;
  };

  const stopTurn = async (): Promise<void> => {
    if (!sessionId) return;
    const res = await api.stopTurn(sessionId);
    if (!res.ok) message.error(res.message);
    session.reload();
    refreshSidebar();
  };

  // 欢迎页“载入公开样例”（T49）：开一个会话，把样例的输入（统计周期、样例材料）预填成一张员工表单，
  // 人确认即开工；样例材料从应用目录取，不写进人的工作目录
  const loadSample = async () => {
    try {
      const targetWorkspace = startWorkspaceId(workspaceState.list, selection.kind === "new" ? selection.workspaceId : undefined);
      const created = await api.createSession(targetWorkspace);
      const res = await api.loadSampleForm(created.id);
      setSelection({ kind: "session", id: created.id });
      refreshSidebar();
      if (!res.ok) message.error(res.message);
      else message.success("已载入公开样例，核对后点“确认开工”");
    } catch (e) {
      fail(e, "载入样例失败");
    }
  };

  // 事件比快照先到时，快照里的操作属于上一个状态，不能拿来显示按钮；状态变化后会重新拉取
  const live = task.snapshot?.state === taskState ? task.snapshot : undefined;
  const actions: CardActions = {
    role,
    taskActions: live?.actions ?? [],
    waitingFor: detail?.waitingFor ?? live?.waitingFor ?? [],
    pause: live?.pause,
    run,
    onRole: setRoleState,
    openSettings: () => setSelection({ kind: "page", page: "settings" }),
    openSupplement: () => {
      setDrawerOpen(true);
      panelRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    },
    openPanel: () => setDrawerOpen(true),
    sections: employee?.reviewSections ?? DEFAULT_SECTIONS,
    downloadUrl: (kind, version, forTask) => {
      const target = forTask ?? taskId;
      return target ? api.deliverableUrl(target, kind, version) : undefined;
    },
  };

  const items = useMemo(
    () =>
      detail
        ? deriveChat(detail, task.snapshot ? { state: task.snapshot.state, deliverables: task.snapshot.deliverables } : undefined, task.events, stream.live, pastRuns, role)
        : [],
    [detail, task.snapshot, stream.live, task.events, pastRuns, role],
  );
  /** 新会话沿用的模型：设置的 lastModel（T39） */
  const [lastModel, setLastModel] = useState<ModelRef | undefined>();
  useEffect(() => {
    let alive = true;
    settingsApi.getAppSettings().then(
      (view) => alive && setLastModel(view.lastModel ?? undefined),
      () => {},
    );
    return () => {
      alive = false;
    };
  }, [settingsApi, selection.kind]);

  let center: React.ReactNode;
  let panel: React.ReactNode = null;
  if (selection.kind === "page") {
    // 页面组件各自一个文件，T10/T11 并行填充时互不冲突。
    center =
      selection.page === "settings" ? (
        <SettingsPage api={settingsApi} />
      ) : selection.page === "knowledge" ? (
        <KnowledgePage role={role} employees={employees} />
      ) : (
        <EmployeeConfigPage employees={employees} />
      );
  } else if (selection.kind === "new") {
    center = (
      <NewConversation
        preflight={preflight}
        role={role}
        openSettings={actions.openSettings}
        onStart={startConversation}
        onSample={() => void loadSample()}
        modelOptions={modelOptions}
        defaultModel={lastModel}
        target={newChatTarget(workspaceState.list, selection.kind === "new" ? selection.workspaceId : undefined)}
        onChangeWorkspace={(workspaceId) => setSelection({ kind: "new", workspaceId })}
        onAddWorkspace={() => setAddWorkspace({ open: true })}
      />
    );
  } else if (!detail) {
    center = session.error ? (
      <StateBlock
        tone="error"
        icon={<ExclamationCircleOutlined />}
        title="读不出这个会话"
        description={session.error}
        actions={
          <>
            <Button type="primary" icon={<ReloadOutlined />} onClick={() => session.reload()}>
              重试
            </Button>
            <Button onClick={() => setSelection({ kind: "new" })}>回到新对话</Button>
          </>
        }
      />
    ) : (
      <LoadingBlock label="正在读取会话" />
    );
  } else {
    center = (
      <SessionChat
        detail={detail}
        items={items}
        subtitle={employee ? `由${employee.name}处理` : "前台 Agent"}
        actions={actions}
        session={sessionActions}
        events={task.events}
        steps={task.snapshot?.steps ?? NO_STEPS}
        elapsedMs={task.view.elapsedMs}
        modelCalls={task.snapshot?.budget.modelCalls ?? { used: 0, limit: 0 }}
        attachments={(task.snapshot?.attachments ?? []).map((a) => a.name)}
        folderFiles={folderFiles}
        artifacts={artifactsOf(task.snapshot, actions.downloadUrl)}
        headerExtra={
          drawerMode && taskId ? (
            <Button className="drawer-toggle" icon={<LeftOutlined />} onClick={() => setDrawerOpen(true)}>
              {taskState === "awaiting_initiator" ? "补充材料" : taskState === "draft" ? "待开工" : "交付物"}
            </Button>
          ) : undefined
        }
        modelOptions={modelOptions}
        onSelectModel={selectModel}
        onSelectMode={selectMode}
        openSettings={actions.openSettings}
        onSend={send}
        live={stream.live}
        onStop={stopTurn}
      />
    );
  }
  if (selection.kind === "session" && taskId) {
    const supplement = task.view.state === "awaiting_initiator" && employee && task.snapshot;
    const plan = actionPlan(actions.taskActions, actions.waitingFor, role);
    panel = supplement ? (
      <>
        {plan.handoff && <div className="panel-card"><Handoff handoff={plan.handoff} onRole={setRoleState} /></div>}
        <LaunchPanel
          key={`${task.snapshot!.id}-${panelKey}-${task.view.lastSeq}`}
          employee={employee}
          mode="supplement"
          initialForm={task.snapshot!.form}
          submitting={submitting}
          disabled={!plan.mine.some((a) => a.command === "supplement")}
          onSubmit={async (form, uploads) => {
            setSubmitting(true);
            await run({ type: "supplement", form }, uploads);
            setSubmitting(false);
            setPanelKey((k) => k + 1);
          }}
        />
      </>
    ) : taskState === "draft" ? (
      <div className="panel-card">
        <p className="chat-sub" style={{ marginBottom: 12 }}>
          已备好公开样例的销售明细与周会纪要（第 44 周）。确认开工后员工开始执行。
        </p>
        <TaskActionBar actions={actions} />
      </div>
    ) : (
      <ReviewPanel snapshot={live ?? task.snapshot} actions={actions} />
    );
  }

  // 有交付物要读时右栏加宽到一半左右；窗口较窄时收成抽屉
  const reading = (task.snapshot?.versions?.length ?? 0) > 0 && (taskState === "awaiting_review" || taskState === "delivered");
  const layout = panelLayout(windowWidth, reading && panel !== null);
  const drawer = layout.mode === "drawer";
  const columnPanel = panel !== null && !drawer;
  const supplementing = taskState === "awaiting_initiator";
  const titled = taskState === "draft" || supplementing;
  const panelBody = (
    <>
      {titled && <h3 className="panel-title">{taskState === "draft" ? "待开工" : "补充材料"}</h3>}
      {panel}
    </>
  );
  return (
    <div className={`shell ${columnPanel ? (layout.wide ? "wide" : "") : "no-panel"} ${drawer ? "narrow" : ""}`}>
      <Sidebar
        role={role}
        onRole={setRoleState}
        selection={selection}
        onSelect={setSelection}
        api={workspaceApi}
        workspace={sidebarWorkspace}
        onRefresh={refreshSidebar}
        tab={sidebarTab}
        onTab={setSidebarTab}
        groups={recordsState.groups}
        error={workspaceState.error ?? (sidebarTab === "records" ? recordsState.error : undefined)}
        onStartChat={(workspaceId) => setSelection({ kind: "new", workspaceId })}
        onAddWorkspace={(hint) => setAddWorkspace({ open: true, hint })}
      />
      <AddWorkspaceModal
        open={addWorkspace.open}
        hint={addWorkspace.hint}
        api={workspaceApi}
        onClose={() => setAddWorkspace({ open: false })}
        onCreated={(created) => {
          refreshSidebar();
          // 正停在新对话页（比如“先添加工作区”的引导）：添加后新对话就开在它下面
          if (selection.kind === "new") setSelection({ kind: "new", workspaceId: created.id });
        }}
      />
      <div className="col surface chat">{center}</div>
      {columnPanel && (
        <div className={`col panel ${titled ? "" : "panel-review"}`} ref={panelRef}>
          {panelBody}
        </div>
      )}
      {drawer && panel !== null && (
        <>
          <Drawer
            open={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            placement="right"
            width={Math.min(windowWidth - 24, reading ? 720 : 420)}
            title={null}
            closable={false}
            styles={{ body: { padding: 12, display: "flex", flexDirection: "column", background: "var(--bg)" } }}
          >
            <div className={`col panel drawer-panel ${titled ? "" : "panel-review"}`} ref={panelRef}>
              {panelBody}
            </div>
          </Drawer>
        </>
      )}
      {IS_MOCK && <div style={{ position: "fixed", left: 18, bottom: 2, fontSize: 11, color: "var(--muted)" }}>模拟数据模式</div>}
    </div>
  );
}

const NO_STEPS: StepStatusView[] = [];

/** 轨迹详情里的产物：每一版交付物和它的检验记录 */
function artifactsOf(snapshot: TaskSnapshot | undefined, downloadUrl: CardActions["downloadUrl"]): TraceFile[] {
  const versions = snapshot?.versions ?? [];
  const latest = versions.length;
  return versions.flatMap((v) =>
    v.files.map((f) => ({
      key: `${v.version}:${f.kind}`,
      label: `第 ${v.version} 版 · ${FILE_NAMES[f.kind] ?? f.kind}`,
      url: downloadUrl(f.kind, v.version === latest ? undefined : v.version),
    })),
  );
}

const FILE_NAMES: Record<string, string> = { docx: "Word 文档", md: "Markdown 报告", qc_record: "检验记录" };

function useWindowWidth(): number {
  const [width, setWidth] = useState(() => window.innerWidth);
  useEffect(() => {
    const onResize = () => setWidth(window.innerWidth);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return width;
}
function initialRole(): UiRole | undefined {
  const q = new URLSearchParams(location.search).get("role");
  return q === "initiator" || q === "reviewer" || q === "operator" ? q : undefined;
}

function ThemedShell({ roleRef, api, settingsApi }: { roleRef: { current: UiRole }; api: ReturnType<typeof createApi>; settingsApi: SettingsApi }) {
  const { ui, resolvedTheme } = useUiPreferences();
  return (
    <ConfigProvider
      locale={zhCN}
      theme={{
        // 主题按解析后的实际值切换（`system` 已由 provider 跟随系统偏好解析）；色值与 styles/tokens.css 一致
        algorithm: resolvedTheme === "dark" ? theme.darkAlgorithm : theme.defaultAlgorithm,
        ...antdTheme(resolvedTheme === "dark" ? "dark" : "light", ui.fontSize),
      }}
      button={{ autoInsertSpace: false }}
    >
      <XProvider>
        <AntApp style={{ height: "100%" }}>
          <ApiContext.Provider value={api}>
            <Shell roleRef={roleRef} settingsApi={settingsApi} />
          </ApiContext.Provider>
        </AntApp>
      </XProvider>
    </ConfigProvider>
  );
}

export function App() {
  // 启动身份：显式的 ?role= 优先（演示用）；否则读 permissions.defaultRole。
  // 侧栏手动切换角色只改 roleRef，不写回默认值（要写回由设置页“设为默认”负责）。
  const queryRole = useMemo(initialRole, []);
  const roleRef = useRef<UiRole>(queryRole ?? "initiator");
  const [roleReady, setRoleReady] = useState(queryRole !== undefined);
  /** 审批策略也来自设置文档：确认框按它决定 ask 弹框 / never 直接执行 */
  const [approvalPolicy, setApprovalPolicy] = useState<ApprovalPolicy>("ask");
  const api = useMemo(() => createApi(() => roleRef.current), []);
  // 设置页与外观偏好共用一个设置客户端（模拟模式下也共享同一份内存状态）；
  // 带上角色头发给服务端，审计记录“谁”做了危险操作。
  const settingsApi = useMemo(() => createSettingsApi(undefined, () => roleRef.current), []);

  useEffect(() => {
    let alive = true;
    settingsApi.getAppSettings().then(
      (view) => {
        if (!alive) return;
        setApprovalPolicy(view.permissions.approvalPolicy);
        // 显式 ?role= 优先：只有没带查询参数时才用默认角色
        if (queryRole === undefined) roleRef.current = view.permissions.defaultRole;
        setRoleReady(true);
      },
      () => {
        // 读取失败不阻塞应用：回落到发起人 / 每次询问（最保守的审批策略）
        if (alive) setRoleReady(true);
      },
    );
    return () => {
      alive = false;
    };
  }, [queryRole, settingsApi]);

  if (!roleReady) {
    return <div className="boot-note" role="status">正在读取默认角色与审批策略…</div>;
  }

  return (
    <ApprovalPolicyProvider initialPolicy={approvalPolicy} onPolicyChange={setApprovalPolicy}>
      <UiPreferencesProvider api={settingsApi}>
        <ThemedShell roleRef={roleRef} api={api} settingsApi={settingsApi} />
      </UiPreferencesProvider>
    </ApprovalPolicyProvider>
  );
}

