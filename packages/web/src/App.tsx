import { useCallback, useMemo, useRef, useState } from "react";
import { App as AntApp, Button, ConfigProvider, Tooltip } from "antd";
import { XProvider } from "@ant-design/x";
import zhCN from "antd/locale/zh_CN";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import type { TaskCommand } from "@de/core/contract";
import { createApi, IS_MOCK, type UiRole, type UploadItem } from "./api";
import { ApiContext, useApi, useEmployees, useTask, useTaskList } from "./hooks/data";
import { Sidebar, type Selection } from "./components/Sidebar";
import { NewConversation, TaskChat } from "./components/Chat";
import { LaunchPanel } from "./components/LaunchPanel";
import { TaskPanel } from "./components/TaskPanel";
import type { CardActions } from "./components/Cards";
import { ROLE_LABELS } from "./view/permissions";

const SECTIONS: Record<string, string[]> = {
  "weekly-report": ["本周摘要", "核心指标", "品类表现", "异常与原因", "数据质量", "下周关注"],
};
const DEFAULT_SECTIONS = ["摘要", "正文", "来源清单"];

const PAGE_TITLES = { replay: "事件回放", config: "员工配置", settings: "设置" } as const;

function Shell({ roleRef }: { roleRef: { current: UiRole } }) {
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
  const employee = employees.find((e) => e.id === (selection.kind === "new" ? selection.employee : task.snapshot?.employee));

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

  const actions: CardActions = {
    role,
    state: task.view.state,
    run,
    openSupplement: () => {
      setPanelOpen(true);
      panelRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    },
    sections: SECTIONS[task.snapshot?.employee ?? ""] ?? DEFAULT_SECTIONS,
    downloadUrl: (kind) => (taskId ? api.deliverableUrl(taskId, kind) : undefined),
  };

  const initiatorOnly = role !== "initiator";
  const launchDisabledReason = initiatorOnly ? `当前角色（${ROLE_LABELS[role]}）不能发起任务，请切换为发起人` : undefined;

  let center: React.ReactNode;
  let panel: React.ReactNode;
  if (selection.kind === "page") {
    center = (
      <div className="chat-body">
        <div className="placeholder-page">
          <h2>{PAGE_TITLES[selection.page]}</h2>
          <p>本期先放占位页，后续 ticket 实现。</p>
        </div>
      </div>
    );
    panel = null;
  } else if (selection.kind === "new") {
    center = <NewConversation employee={employee} onPrompt={() => message.info("请在右侧填写表单并上传材料后提交")} />;
    panel = employee && (
      <>
        {initiatorOnly && <div className="panel-card" style={{ color: "var(--warn)" }}>{launchDisabledReason}</div>}
        <LaunchPanel key={employee.id} employee={employee} mode="launch" submitting={submitting} disabled={initiatorOnly} onSubmit={launch} />
      </>
    );
  } else {
    center = (
      <TaskChat
        title={tasks.find((t) => t.id === selection.id)?.title ?? task.snapshot?.id ?? "任务"}
        subtitle={employee ? `由 ${employee.name} 处理` : undefined}
        view={task.view}
        actions={actions}
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
              {selection.kind === "task" && task.view.state && task.view.state !== "draft" && task.view.state !== "awaiting_initiator" ? "任务面板" : selection.kind === "task" ? "补充材料" : "任务面板"}
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

function initialRole(): UiRole {
  const q = new URLSearchParams(location.search).get("role");
  return q === "reviewer" || q === "operator" ? q : "initiator";
}

export function App() {
  // 角色放在 ref 里，数据层每次请求时读取，用于 x-role 请求头
  const roleRef = useRef<UiRole>(initialRole());
  const api = useMemo(() => createApi(() => roleRef.current), []);
  return (
    <ConfigProvider locale={zhCN} theme={{ token: { colorPrimary: "#1F8A70", borderRadius: 10 } }} button={{ autoInsertSpace: false }}>
      <XProvider>
        <AntApp style={{ height: "100%" }}>
          <ApiContext.Provider value={api}>
            <Shell roleRef={roleRef} />
          </ApiContext.Provider>
        </AntApp>
      </XProvider>
    </ConfigProvider>
  );
}

