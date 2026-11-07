import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App as AntApp, Button, ConfigProvider, theme, Tooltip } from "antd";
import { XProvider } from "@ant-design/x";
import zhCN from "antd/locale/zh_CN";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import type { ApprovalPolicy, TaskCommand, TaskEventType } from "@de/core/contract";
import { createApi, IS_MOCK, type SessionResult, type UiRole, type UploadItem } from "./api";
import { createSettingsApi, type SettingsApi } from "./api/settings";
import { ApiContext, useApi, useEmployees, usePreflight, useSession, useSessionList, useTask } from "./hooks/data";
import { ApprovalPolicyProvider } from "./components/ApprovalPolicy";
import { Sidebar, type Selection } from "./components/Sidebar";
import { NewConversation, SessionChat } from "./components/Chat";
import { LaunchPanel } from "./components/LaunchPanel";
import { TaskPanel } from "./components/TaskPanel";
import { Handoff, TaskActionBar, type CardActions, type SessionActions } from "./components/Cards";
import { UiPreferencesProvider, useUiPreferences } from "./components/Preferences";
import { EmployeeConfigPage } from "./pages/EmployeeConfig";
import { SettingsPage } from "./pages/Settings";
import { actionPlan } from "./view/permissions";
import { deriveChat } from "./view/chat-view";

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
  const { sessions, refresh } = useSessionList();
  const [role, setRoleState] = useState<UiRole>(roleRef.current);
  roleRef.current = role;

  const [selection, setSelection] = useState<Selection>({ kind: "new" });
  const [panelOpen, setPanelOpen] = useState(true);
  const [panelKey, setPanelKey] = useState(0);
  const panelRef = useRef<HTMLDivElement>(null);
  const [submitting, setSubmitting] = useState(false);
  const { preflight, reload: reloadPreflight } = usePreflight();

  const sessionId = selection.kind === "session" ? selection.id : undefined;
  const session = useSession(sessionId);
  const detail = session.detail;
  const taskId = detail?.taskId;
  const task = useTask(taskId);
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
      refresh();
      task.reload();
      session.reload();
    },
    [api, taskId, message, refresh, task, session, fail],
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
    refresh();
    return res;
  };

  const startConversation = async (text: string): Promise<boolean> => {
    try {
      const created = await api.createSession();
      const res = await api.sendMessage(created.id, text);
      if (!res.ok) message.error(res.message);
      refresh();
      setSelection({ kind: "session", id: created.id });
      return true;
    } catch (e) {
      fail(e, "开始对话失败");
      return false;
    }
  };

  const sessionActions: SessionActions = {
    employees,
    async confirmEmployee(employeeId) {
      if (!sessionId) return;
      const res = await settle(api.confirmEmployee(sessionId, employeeId));
      if (!res.ok) message.error(res.message);
    },
    async upload(files) {
      if (!sessionId) return;
      const res = await settle(api.uploadAttachments(sessionId, files));
      if (!res.ok) message.error(res.message);
    },
    submit: async (form) => (sessionId ? settle(api.submitSession(sessionId, form)) : { ok: false, error: "not_found", message: "没有选中会话" }),
  };

  const send = async (text: string): Promise<boolean> => {
    if (!sessionId) return false;
    const res = await settle(api.sendMessage(sessionId, text));
    if (!res.ok) message.error(res.message);
    return res.ok;
  };

  // 欢迎页“载入公开样例”：一键建草稿任务（附件与表单来自工作区 samples，不用手选文件），
  // 它没有会话，以旧任务的只读会话出现
  const loadSample = async () => {
    try {
      const snap = await api.loadPublicSample();
      const listed = await api.listSessions();
      refresh();
      const bound = listed.find((s) => s.taskId === snap.id);
      if (bound) setSelection({ kind: "session", id: bound.id });
      message.success("已载入公开样例，提交后开始生成周报");
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
      setPanelOpen(true);
      panelRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    },
    sections: employee?.reviewSections ?? DEFAULT_SECTIONS,
    downloadUrl: (kind) => (taskId ? api.deliverableUrl(taskId, kind) : undefined),
  };

  const items = useMemo(
    () => (detail ? deriveChat(detail, task.snapshot ? { state: task.snapshot.state, deliverables: task.snapshot.deliverables } : undefined) : []),
    [detail, task.snapshot],
  );

  let center: React.ReactNode;
  let panel: React.ReactNode = null;
  if (selection.kind === "page") {
    // 页面组件各自一个文件，T10/T11 并行填充时互不冲突。
    center = selection.page === "settings" ? <SettingsPage api={settingsApi} /> : <EmployeeConfigPage employees={employees} />;
  } else if (selection.kind === "new") {
    center = (
      <NewConversation
        preflight={preflight}
        role={role}
        openSettings={actions.openSettings}
        onStart={startConversation}
        onSample={() => void loadSample()}
      />
    );
  } else if (!detail) {
    center = <div className="welcome" style={{ paddingTop: 48, color: "var(--muted)" }}>{session.error ?? "正在读取会话…"}</div>;
  } else {
    center = (
      <SessionChat
        detail={detail}
        items={items}
        subtitle={employee ? `由${employee.name}处理` : "前台接待"}
        actions={actions}
        session={sessionActions}
        events={task.events}
        stepDefs={task.snapshot?.steps ?? []}
        elapsedMs={task.view.elapsedMs}
        modelCalls={task.snapshot?.budget.modelCalls ?? { used: 0, limit: 0 }}
        onSend={send}
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
          pause={task.view.pause}
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
        <h4>草稿任务</h4>
        <p className="chat-sub" style={{ marginBottom: 12 }}>
          已载入公开样例的销售明细与周会纪要（第 44 周）。提交后员工开始执行。
        </p>
        <TaskActionBar actions={actions} />
      </div>
    ) : (
      <TaskPanel snapshot={task.snapshot} view={task.view} downloadUrl={actions.downloadUrl} />
    );
  }

  const showPanel = panelOpen && panel !== null;
  return (
    <div className={`shell ${showPanel ? "" : "no-panel"}`}>
      <Sidebar sessions={sessions} role={role} onRole={setRoleState} selection={selection} onSelect={setSelection} />
      <div className="col surface chat">{center}</div>
      {showPanel ? (
        <div className="col panel" ref={panelRef}>
          <div style={{ display: "flex", alignItems: "center", marginBottom: 10 }}>
            <h3 style={{ margin: 0, flex: 1, fontSize: 18 }}>
              {taskState === "draft" ? "草稿任务" : taskState === "awaiting_initiator" ? "补充材料" : "任务面板"}
            </h3>
            <Tooltip title="收起">
              <Button type="text" icon={<RightOutlined />} onClick={() => setPanelOpen(false)} />
            </Tooltip>
          </div>
          {panel}
        </div>
      ) : (
        panel !== null && (
          <Button style={{ position: "fixed", right: 16, top: 16 }} icon={<LeftOutlined />} onClick={() => setPanelOpen(true)}>
            任务面板
          </Button>
        )
      )}
      {IS_MOCK && <div style={{ position: "fixed", left: 18, bottom: 2, fontSize: 11, color: "var(--muted)" }}>模拟数据模式</div>}
    </div>
  );
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
        // 主题按解析后的实际值切换（`system` 已由 provider 跟随系统偏好解析）
        algorithm: resolvedTheme === "dark" ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: {
          colorPrimary: resolvedTheme === "dark" ? "#37A98C" : "#1F8A70",
          borderRadius: 10,
          fontSize: ui.fontSize,
        },
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
    return <div style={{ padding: 24, color: "#888" }}>正在读取默认角色与审批策略…</div>;
  }

  return (
    <ApprovalPolicyProvider initialPolicy={approvalPolicy} onPolicyChange={setApprovalPolicy}>
      <UiPreferencesProvider api={settingsApi}>
        <ThemedShell roleRef={roleRef} api={api} settingsApi={settingsApi} />
      </UiPreferencesProvider>
    </ApprovalPolicyProvider>
  );
}

