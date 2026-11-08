import { Prompts, Sender, Welcome } from "@ant-design/x";
import { Alert, Button, Collapse, Tag, Typography } from "antd";
import { CaretDownOutlined, CaretRightOutlined, CheckCircleFilled, CloseCircleFilled, FolderOpenOutlined, LoadingOutlined, RobotOutlined, SettingOutlined, StopOutlined, ToolOutlined, UserOutlined } from "@ant-design/icons";
import { useEffect, useMemo, useRef, useState } from "react";
import { STATE_LABELS, type TaskEvent, type TaskState } from "@de/core/contract";
import type { PreflightResult, SessionDetail, StepStatusView, UiRole } from "../api";
import type { AgentToolView } from "../api/types";
import type { ChatItem, LiveTurn } from "../view/chat-view";
import { deriveStages } from "../view/stages";
import { deriveTrajectory } from "../view/trajectory";
import { ROLE_LABELS } from "../view/permissions";
import { renderCard, type CardActions, type SessionActions } from "./Cards";
import { TrajectoryDetails, useTrajectoryOpen, type TraceFile } from "./Trajectory";
import { StageBar } from "./StageBar";
import { notifyUserInput, useUiPreferences } from "./Preferences";
import { enterAction } from "../pages/settings/preferences";

const STATE_COLOR: Record<TaskState, string> = {
  draft: "default",
  admission: "processing",
  awaiting_initiator: "orange",
  running: "processing",
  qc: "processing",
  awaiting_operator: "red",
  awaiting_review: "blue",
  delivered: "green",
  terminated: "default",
};

export function StateTag({ state }: { state: TaskState }) {
  return (
    <Tag color={STATE_COLOR[state]} variant="filled" style={{ margin: 0, fontSize: 13 }}>
      {STATE_LABELS[state]}
    </Tag>
  );
}

const SESSION_TAG: Record<string, string> = { reception: "接待中", drafting: "填写表单" };

const clock = (ts: string) => new Date(ts).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });

/** 一轮里的一次工具调用：默认折成一行，点开看参数和结果 */
function ToolCalls({ items }: { items: AgentToolView[] }) {
  return (
    <Collapse
      className="tool-calls"
      size="small"
      bordered={false}
      defaultActiveKey={items.some((t) => !t.ended) ? ["tools"] : []}
      expandIconPosition="start"
      expandIcon={({ isActive }) => (isActive ? <CaretDownOutlined /> : <CaretRightOutlined />)}
      items={[
        {
          key: "tools",
          label: (
            <span className="tool-calls-label">
              <ToolOutlined /> 调用了 {items.length} 次工具{items.some((t) => !t.ended) ? "，还没结束" : ""}
            </span>
          ),
          children: (
            <ul className="tool-list">
              {items.map((tool) => (
                <li key={tool.id} className={`tool ${tool.ended ? (tool.isErr ? "bad" : "ok") : "busy"}`}>
                  <div className="tool-head">
                    {tool.ended ? tool.isErr ? <CloseCircleFilled /> : <CheckCircleFilled /> : <LoadingOutlined />}
                    <code>{tool.tool}</code>
                    {tool.args && Object.keys(tool.args).length > 0 && <span className="tool-args">{brief(tool.args)}</span>}
                  </div>
                  {tool.ended && (tool.reason || tool.result !== undefined) && (
                    <Typography.Paragraph className="tool-result" ellipsis={{ rows: 3, expandable: true, symbol: "展开" }}>
                      {tool.reason || brief(tool.result)}
                    </Typography.Paragraph>
                  )}
                </li>
              ))}
            </ul>
          ),
        },
      ]}
    />
  );
}

/** 工具参数和结果压成一行 */
function brief(value: unknown): string {
  const text = typeof value === "string" ? value : JSON.stringify(value);
  return text.length > 120 ? `${text.slice(0, 120)}…` : text;
}

/** 正在说的一段话：后到的字一段段接上，末尾闪一个光标 */
function StreamingMessage({ text, running }: { text: string; running: boolean }) {
  return (
    <div className="msg-row">
      <div className="msg-avatar">
        <RobotOutlined />
      </div>
      <div className="msg-col">
        <div className="bubble streaming">
          {text}
          <span className={running ? "caret" : "caret steady"} />
        </div>
      </div>
    </div>
  );
}

function Message({ item }: { item: Extract<ChatItem, { kind: "user" | "agent" }> }) {
  const me = item.kind === "user";
  // 不是发起人说的话，标出是谁说的（审核人、操作员也会在对话里说话）
  const who = me && item.role && item.role !== "initiator" ? `${ROLE_LABELS[item.role]} · ` : "";
  return (
    <div className={`msg-row ${me ? "me" : ""}`}>
      <div className="msg-avatar">{me ? <UserOutlined /> : <RobotOutlined />}</div>
      <div className="msg-col">
        <div className="bubble">{item.text}</div>
        <div className="msg-time">
          {who}
          {clock(item.ts)}
        </div>
      </div>
    </div>
  );
}

/**
 * 输入框：发送立即受理，回复在事件流里逐字出现；正在说话时主按钮变成“停止”，但照样能发——
 * 这时发的话排进这一轮，在下一步之前送达。
 */
function ChatInput({
  placeholder,
  disabled,
  onSend,
  streaming,
  onStop,
}: {
  placeholder: string;
  disabled?: boolean;
  onSend: (text: string) => Promise<boolean>;
  /** 正在说话：显示“停止” */
  streaming?: boolean;
  /** 点“停止” */
  onStop?: () => Promise<void> | void;
}) {
  const { ui } = useUiPreferences();
  const [value, setValue] = useState("");
  const [sending, setSending] = useState(false);
  const [stopping, setStopping] = useState(false);

  const send = async (text: string) => {
    const trimmed = text.trim();
    if (!trimmed || disabled) return;
    // 通知轨迹：用户开始下一次输入（「下次输入时折叠」用它收起上一轮）
    notifyUserInput();
    setSending(true);
    try {
      if (await onSend(trimmed)) setValue("");
    } finally {
      setSending(false);
    }
  };

  const stop = async () => {
    if (!onStop || stopping) return;
    setStopping(true);
    try {
      await onStop();
    } finally {
      setStopping(false);
    }
  };

  return (
    <div className="chat-foot">
      <Sender
        value={value}
        onChange={setValue}
        placeholder={streaming ? "接着说…（会在下一步之前送达）" : placeholder}
        // 正在说话时发送键是“停止”，但输入框里的字照样能发出去（Enter / Ctrl+Enter）
        loading={!streaming && sending}
        suffix={
          streaming ? (
            <Button type="primary" className="stop-button" icon={<StopOutlined />} loading={stopping} onClick={() => void stop()}>
              停止
            </Button>
          ) : undefined
        }
        disabled={disabled}
        // Enter 由下面的 onKeyDown 按偏好决定；Sender 默认的 Enter 提交保留给「发送」模式
        submitType="enter"
        onSubmit={(text) => void send(text)}
        onKeyDown={(event) => {
          if (event.key !== "Enter") return;
          const ctrl = event.ctrlKey || event.metaKey;
          // Ctrl/Cmd+Enter 始终发送（两种模式一致）
          if (ctrl) {
            event.preventDefault();
            void send(value);
            return false;
          }
          // 换行模式：拦住 Sender 的 Enter 提交，让 textarea 自己换行
          if (enterAction(ui.enterBehavior, { shift: event.shiftKey }) === "newline") return false;
          return undefined;
        }}
      />
    </div>
  );
}

export function SessionChat({
  detail,
  items,
  subtitle,
  actions,
  session,
  events,
  steps,
  elapsedMs,
  modelCalls,
  attachments,
  artifacts,
  checklist,
  headerExtra,
  onSend,
  live,
  onStop,
}: {
  detail: SessionDetail;
  items: ChatItem[];
  subtitle?: string;
  actions: CardActions;
  session: SessionActions;
  /** 会话绑定任务时的事件和步骤，用来画状态机和轨迹 */
  events: TaskEvent[];
  steps: StepStatusView[];
  elapsedMs: number;
  modelCalls: { used: number; limit: number };
  /** 轨迹详情里的任务附件和产物 */
  attachments: string[];
  artifacts: TraceFile[];
  /** 开工清单（T30）：放在状态机下方 */
  checklist?: React.ReactNode;
  /** 标题栏右侧的附加入口，比如窄窗口下打开右栏抽屉 */
  headerExtra?: React.ReactNode;
  onSend: (text: string) => Promise<boolean>;
  /** 正在说的一轮（排在最后一条消息之后） */
  live?: LiveTurn;
  onStop?: () => Promise<void> | void;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const trajectory = useMemo(() => deriveTrajectory(events, steps), [events, steps]);
  // 事件比会话摘要新：有状态变化事件时以它为准（草稿任务没有这种事件）
  const state = trajectory.state ?? detail.taskState;
  const stages = useMemo(() => deriveStages(events, steps, state, actions.pause?.title), [events, steps, state, actions.pause?.title]);
  const [traceOpen, setTraceOpen] = useTrajectoryOpen(trajectory.active);
  const lastKey = items.at(-1)?.key;
  useEffect(() => {
    bodyRef.current?.scrollTo({ top: bodyRef.current.scrollHeight, behavior: "smooth" });
  }, [lastKey, items.length]);
  const tag = detail.taskState ? <StateTag state={detail.taskState} /> : SESSION_TAG[detail.status] && <Tag style={{ margin: 0 }}>{SESSION_TAG[detail.status]}</Tag>;
  const showStages = Boolean(detail.taskId) || detail.status === "drafting";
  return (
    <>
      <div className="chat-head">
        <div className="chat-head-main">
          <div className="chat-title">
            <h2 title={detail.title}>{detail.title}</h2>
            {tag}
          </div>
          {subtitle && <div className="chat-sub">{subtitle}</div>}
        </div>
        {detail.taskId && (
          <Button type="link" className="trace-toggle" aria-expanded={traceOpen} onClick={() => setTraceOpen(!traceOpen)}>
            {traceOpen ? "收起轨迹详情" : "轨迹详情"}
          </Button>
        )}
        {headerExtra}
      </div>
      {showStages && <StageBar line={stages} live={trajectory.active} meta={detail.taskId ? { modelCalls, elapsedMs } : undefined} />}
      {checklist}
      {detail.taskId && traceOpen && (
        <TrajectoryDetails view={trajectory} elapsedMs={elapsedMs} modelCalls={modelCalls} attachments={attachments} artifacts={artifacts} />
      )}
      <div className="chat-body" ref={bodyRef}>
        {items.map((item) =>
        item.kind === "user" || item.kind === "agent" ? (
          <Message key={item.key} item={item} />
        ) : item.kind === "tools" ? (
          <ToolCalls key={item.key} items={item.items} />
        ) : item.kind === "streaming" ? (
          <StreamingMessage key={item.key} text={item.text} running={live?.running ?? false} />
        ) : (
          renderCard(item, actions, session)
        ),
      )}
      </div>
      <ChatInput
        placeholder={detail.readOnly ? "这是旧任务的只读记录，要继续工作请开新对话" : detail.taskId ? "问问这个任务…" : "补充要求，或问问员工…"}
        disabled={detail.readOnly}
        onSend={onSend}
        streaming={!detail.readOnly && Boolean(live?.running)}
        onStop={onStop}
      />
    </>
  );
}
/** 没配 Key 时的首屏提示：说明原因，并给设置入口。 */
export function PreflightNotice({ preflight, openSettings }: { preflight: PreflightResult | undefined; openSettings: () => void }) {
  if (!preflight || preflight.ok) return null;
  return (
    <Alert
      className="preflight-notice"
      type="warning"
      showIcon
      message="还不能开始工作"
      description={preflight.message}
      action={
        <Button type="primary" icon={<SettingOutlined />} onClick={openSettings}>
          {preflight.settings?.label ?? "去设置"}
        </Button>
      }
    />
  );
}

export function NewConversation({
  preflight,
  openSettings,
  onStart,
  onSample,
  role,
}: {
  preflight: PreflightResult | undefined;
  openSettings: () => void;
  onStart: (text: string) => Promise<boolean>;
  onSample: () => void;
  role: UiRole;
}) {
  return (
    <>
      <div className="chat-body">
        <div className="welcome">
          <PreflightNotice preflight={preflight} openSettings={openSettings} />
          <Welcome
            variant="borderless"
            icon={
              <div className="welcome-icon">
                <RobotOutlined />
              </div>
            }
            title="你好，说说要做什么"
            description="用一句话说明工作，我来安排合适的员工接手，再帮你备齐材料。"
          />
          <Prompts
            wrap
            style={{ marginTop: 24 }}
            items={[
              {
                key: "sample",
                icon: <FolderOpenOutlined style={{ color: "#2f6fed" }} />,
                label: "载入公开样例",
                description: "用第 44 周的样例数据建一份草稿任务，提交即可看到完整流程",
              },
            ]}
            onItemClick={() => onSample()}
          />
          {role !== "initiator" && <p className="chat-sub" style={{ marginTop: 16 }}>当前角色是{ROLE_LABELS[role]}；只有发起人能确认员工和提交任务。</p>}
        </div>
      </div>
      <ChatInput placeholder="例如：帮我出第 44 周的经营周报" onSend={onStart} />
    </>
  );
}
