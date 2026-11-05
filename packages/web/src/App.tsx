import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App as AntApp, Button, ConfigProvider, theme, Tooltip } from "antd";
import { XProvider } from "@ant-design/x";
import zhCN from "antd/locale/zh_CN";
import { LeftOutlined, RightOutlined, SendOutlined } from "@ant-design/icons";
import type { ApprovalPolicy, TaskCommand } from "@de/core/contract";
import { createApi, IS_MOCK, type UiRole, type UploadItem } from "./api";
import { createSettingsApi, type SettingsApi } from "./api/settings";
import { ApiContext, useApi, useEmployees, useTask, useTaskList } from "./hooks/data";
import { ApprovalPolicyProvider } from "./components/ApprovalPolicy";
import { Sidebar, type Selection } from "./components/Sidebar";
import { NewConversation, TaskChat } from "./components/Chat";
import { LaunchPanel } from "./components/LaunchPanel";
import { TaskPanel } from "./components/TaskPanel";
import type { CardActions } from "./components/Cards";
import { UiPreferencesProvider, useUiPreferences } from "./components/Preferences";
import { EmployeeConfigPage } from "./pages/EmployeeConfig";
import { SettingsPage } from "./pages/Settings";
import { ROLE_LABELS } from "./view/permissions";

const DEFAULT_SECTIONS = [
  { id: "summary", heading: "摘要" },
  { id: "body", heading: "正文" },
];

function Shell({ roleRef, settingsApi }: { roleRef: { current: UiRole }; settingsApi: SettingsApi }) {
  const api = useApi();
  const { message } = AntApp.useApp();
  const employees = useEmployees();
  const { tasks, refresh } = useTaskList();
  const [role, setRoleState] = useState<UiRole>(roleRef.current);
  roleRef.current = role;

  const [selection, setSelection] = useState<Selection>({ kind: "new", employee: "weekly-report" });
  const [panelOpen, setPanelOpen] = useState(true);
  const [panelKey, setPanelKey] = useState(0);
  const panelRef = useRef<HTMLDivElement>(null);
  const [submitting, setSubmitting] = useState(false);

  const taskId = selection.kind === "task" ? selection.id : undefined;
  const task = useTask(taskId);
  // 服务端快照里的 state 最权威（草稿任务没有 state_changed 事件，视图里可能还是空）
  const taskState = task.view.state ?? task.snapshot?.state;
  const employee = employees.find((e) => e.id === (selection.kind === "new" ? selection.employee : task.snapshot?.employee));
  const sessionTitle =
    selection.kind === "task" ? (tasks.find((t) => t.id === selection.id)?.title ?? task.snapshot?.id ?? "任务") : undefined;

  // 浏览器标签页跟随当前会话标题（新对话时回到产品名）
  useEffect(() => {
    document.title = sessionTitle ? `${sessionTitle} · 迷你数字员工` : "迷你数字员工";
  }, [sessionTitle]);

  const run = useCallback(
    async (cmd: TaskCommand, uploads?: UploadItem[]) => {
      if (!taskId) return;
      try {
        const res = await api.command(taskId, cmd, uploads);
        if (!res.ok) message.error(res.message);
      } catch (e) {
        message.error(e instanceof Error ? e.message : "操作失败");
      }
      refresh();
      task.reload();
    },
    [api, taskId, message, refresh, task],
  );

  const launch = async (form: Record<string, unknown>, uploads: UploadItem[]) => {
    if (selection.kind !== "new") return;
    setSubmitting(true);
    try {
      const snap = await api.createTask({ employee: selection.employee, form, uploads });
      const res = await api.command(snap.id, { type: "submit" });
      if (!res.ok) message.error(res.message);
      refresh();
      setSelection({ kind: "task", id: snap.id });
    } catch (e) {
      message.error(e instanceof Error ? e.message : "提交失败");
    } finally {
      setSubmitting(false);
    }
  };

  // 欢迎页“载入公开样例”：一键建草稿任务（附件与表单来自工作区 samples，不用手选文件）
  const loadSample = async () => {
    try {
      const snap = await api.loadPublicSample();
      refresh();
      setSelection({ kind: "task", id: snap.id });
      message.success("已载入公开样例，提交后开始生成周报");
    } catch (e) {
      message.error(e instanceof Error ? e.message : "载入样例失败");
    }
  };

  const submitDraft = async () => {
    if (!taskId) return;
    setSubmitting(true);
    try {
      await run({ type: "submit" });
    } finally {
      setSubmitting(false);
    }
  };

  const actions: CardActions = {
    role,
    state: taskState,
    run,
    openSupplement: () => {
      setPanelOpen(true);
      panelRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    },
    sections: employee?.reviewSections ?? DEFAULT_SECTIONS,
    downloadUrl: (kind) => (taskId ? api.deliverableUrl(taskId, kind) : undefined),
  };

  const initiatorOnly = role !== "initiator";
  const launchDisabledReason = initiatorOnly ? `当前角色（${ROLE_LABELS[role]}）不能发起任务，请切换为发起人` : undefined;

  let center: React.ReactNode;
  let panel: React.ReactNode;
  if (selection.kind === "page") {
    // 页面组件各自一个文件，T10/T11 并行填充时互不冲突。
    center = selection.page === "settings" ? <SettingsPage api={settingsApi} /> : <EmployeeConfigPage employees={employees} />;
    panel = null;
  } else if (selection.kind === "new") {
    center = (
      <NewConversation
        employee={employee}
        onPrompt={(key) => {
          if (key === "sample") void loadSample();
          else message.info("请在右侧填写表单并上传材料后提交");
        }}
      />
    );
    panel = employee && (
      <>
        {initiatorOnly && <div className="panel-card" style={{ color: "var(--warn)" }}>{launchDisabledReason}</div>}
        <LaunchPanel key={employee.id} employee={employee} mode="launch" submitting={submitting} disabled={initiatorOnly} onSubmit={launch} />
      </>
    );
  } else {
    center = (
      <TaskChat
        sessionId={selection.id}
        title={sessionTitle ?? "任务"}
        subtitle={employee ? `由 ${employee.name} 处理` : undefined}
        view={task.view}
        actions={actions}
        events={task.events}
        stepDefs={task.snapshot?.steps ?? []}
        emptyHint={taskState === "draft" ? "已按公开样例建好草稿任务（第 44 周销售明细 + 周会纪要），点右侧「提交任务」开始执行。" : undefined}
        elapsedMs={task.view.elapsedMs}
        modelCalls={task.snapshot?.budget.modelCalls ?? { used: 0, limit: 0 }}
      />
    );
    const supplement = task.view.state === "awaiting_initiator" && employee && task.snapshot;
    panel = supplement ? (
      <>
        {initiatorOnly && <div className="panel-card" style={{ color: "var(--warn)" }}>{launchDisabledReason}</div>}
        <LaunchPanel
          key={`${task.snapshot!.id}-${panelKey}-${task.view.lastSeq}`}
          employee={employee}
          mode="supplement"
          initialForm={task.snapshot!.form}
          pause={task.view.pause}
          submitting={submitting}
          disabled={initiatorOnly}
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
        <Button type="primary" block loading={submitting} disabled={initiatorOnly} icon={<SendOutlined />} onClick={submitDraft}>
          提交任务
        </Button>
      </div>
    ) : (
      <TaskPanel snapshot={task.snapshot} view={task.view} downloadUrl={actions.downloadUrl} />
    );
  }

  const showPanel = panelOpen && panel !== null;
  return (
    <div className={`shell ${showPanel ? "" : "no-panel"}`}>
      <Sidebar employees={employees} tasks={tasks} role={role} onRole={setRoleState} selection={selection} onSelect={setSelection} />
      <div className="col surface chat">{center}</div>
      {showPanel ? (
        <div className="col panel" ref={panelRef}>
          <div style={{ display: "flex", alignItems: "center", marginBottom: 10 }}>
            <h3 style={{ margin: 0, flex: 1, fontSize: 18 }}>
              {selection.kind === "task" && taskState === "draft"
                ? "草稿任务"
                : selection.kind === "task" && taskState && taskState !== "awaiting_initiator"
                  ? "任务面板"
                  : selection.kind === "task"
                    ? "补充材料"
                    : "任务面板"}
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

