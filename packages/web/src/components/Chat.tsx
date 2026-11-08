import { Prompts, Sender, Welcome } from "@ant-design/x";
import type { SenderRef } from "@ant-design/x/es/sender/interface";
import { Alert, Button, Dropdown, Tag } from "antd";
import { DownOutlined, FolderOpenOutlined, RobotOutlined, SettingOutlined, UserOutlined } from "@ant-design/icons";
import { useEffect, useMemo, useRef, useState } from "react";
import { STATE_LABELS, type TaskEvent, type TaskState } from "@de/core/contract";
import type { ModelConnectionOption, PreflightResult, SessionDetail, StepStatusView, UiRole } from "../api";
import { modelLabel, modelMenuItems, parseModelValue } from "../view/session-model";
import type { ChatItem } from "../view/chat-view";
import { deriveStages } from "../view/stages";
import { deriveTrajectory } from "../view/trajectory";
import { ROLE_LABELS } from "../view/permissions";
import { renderCard, type CardActions, type SessionActions } from "./Cards";
import { atQuery, filterFolderPaths, insertAtPath } from "../view/folder-files";
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

/** 输入框：发送等完整回复；失败时保留原文。右下角是这一轮用的模型（T39）。@ 选目录文件（T41）：选中即视为人已确认。 */
/** 输入框右下角的模型下拉（T39）：显示“连接 / 模型”，点开按连接分组列出可选模型。 */
function ModelPicker({
  options,
  current,
  disabled,
  onPick,
  openSettings,
}: {
  options: readonly ModelConnectionOption[];
  current: SessionDetail["model"];
  disabled?: boolean;
  onPick: (connection: string, model: string) => Promise<boolean>;
  openSettings?: () => void;
}) {
  const groups = modelMenuItems(options, current);
  const picked = current ? `${current.connection}/${current.model}` : undefined;
  const empty = groups.length === 0;
  return (
    <Dropdown
      trigger={["click"]}
      placement="topRight"
      disabled={disabled && !empty}
      menu={{
        selectedKeys: picked ? [picked] : [],
        items: empty
          ? [{ key: "empty", disabled: true, label: "还没有可用的模型：先到设置里配置 Key 并选好模型" }]
          : [
              ...groups.map((group) => ({ type: "group" as const, key: `group:${group.label}`, label: group.label, children: group.options.map((o) => ({ key: o.value, label: o.label })) })),
              { type: "divider" as const, key: "divider" },
              { key: "settings", label: "去设置里配置模型连接" },
            ],
        onClick: ({ key }) => {
          if (key === "settings") {
            openSettings?.();
            return;
          }
          if (disabled) return;
          const { connection, model } = parseModelValue(key);
          if (connection && model && !(current && current.connection === connection && current.model === model)) void onPick(connection, model);
        },
      }}
    >
      <Button size="small" type="text" className="model-picker" icon={<RobotOutlined />} title={empty ? "还没有可用的模型" : modelLabel(current)}>
        {empty ? "选择模型" : modelLabel(current)}
        <DownOutlined />
      </Button>
    </Dropdown>
  );
}

function ChatInput({
  placeholder,
  disabled,
  modelPicker,
  folderFiles = [],
  onUseFolderFile,
  onSend,
}: {
  placeholder: string;
  disabled?: boolean;
  modelPicker?: React.ReactNode;
  /** 工作区目录里可选的文件（相对路径） */
  folderFiles?: string[];
  /** 人用 @ 选中的文件：确认作为材料 */
  onUseFolderFile?: (path: string) => Promise<void>;
  onSend: (text: string) => Promise<boolean>;
}) {
  const { ui } = useUiPreferences();
  const [value, setValue] = useState("");
  const [caret, setCaret] = useState(0);
  const [sending, setSending] = useState(false);
  const areaRef = useRef<SenderRef>(null);
  const word = folderFiles.length > 0 && onUseFolderFile ? atQuery(value, caret) : undefined;
  const candidates = word === undefined ? [] : filterFolderPaths(folderFiles, word);

  const send = async (text: string) => {
    const trimmed = text.trim();
    if (!trimmed || sending || disabled) return;
    // 通知轨迹：用户开始下一次输入（「下次输入时折叠」用它收起上一轮）
    notifyUserInput();
    setSending(true);
    try {
      if (await onSend(trimmed)) setValue("");
    } finally {
      setSending(false);
    }
  };

  /** 选中一个目录文件：把 @词 换成 @路径，并把文件作为材料（与在卡片上确认一样） */
  const pick = async (path: string) => {
    if (!onUseFolderFile) return;
    const next = insertAtPath(value, caret, path);
    setValue(next.text);
    setCaret(next.caret);
    await onUseFolderFile(path);
    // 光标放回输入框，人可以接着写
    requestAnimationFrame(() => {
      const area = areaRef.current?.inputElement ?? areaRef.current;
      if (!area || typeof (area as HTMLTextAreaElement).setSelectionRange !== "function") return;
      (area as HTMLTextAreaElement).focus();
      (area as HTMLTextAreaElement).setSelectionRange(next.caret, next.caret);
    });
  };

  return (
    <div className="chat-foot">
      {candidates.length > 0 && (
        <div className="at-picker" role="listbox" aria-label="工作区目录里的文件">
          {candidates.slice(0, 20).map((path) => (
            <button key={path} type="button" role="option" aria-selected={false} className="at-picker-item" onClick={() => void pick(path)}>
              {path}
            </button>
          ))}
        </div>
      )}
      <Sender
        ref={areaRef}
        value={value}
        onChange={(text, event) => {
          setValue(text);
          const element = event?.target as HTMLTextAreaElement | undefined;
          setCaret(element?.selectionStart ?? text.length);
        }}
        placeholder={folderFiles.length > 0 ? `${placeholder}（输入 @ 选目录里的文件）` : placeholder}
        loading={sending}
        disabled={disabled}
        // Enter 由下面的 onKeyDown 按偏好决定；Sender 默认的 Enter 提交保留给「发送」模式
        submitType="enter"
        onSubmit={(text) => void send(text)}
        onKeyDown={(event) => {
          const textarea = event.target as HTMLTextAreaElement | null;
          if (textarea && typeof textarea.selectionStart === "number") setCaret(textarea.selectionStart);
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
      {modelPicker && <div className="chat-foot-bar">{modelPicker}</div>}
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
  folderFiles,
  modelOptions,
  onSelectModel,
  openSettings,
  onSend,
}: {
  detail: SessionDetail;
  items: ChatItem[];
  subtitle?: string;
  actions: CardActions;
  session: SessionActions;
  /** 工作区目录里可选的文件（@ 选择）；只读会话和已开工的会话不给 */
  folderFiles?: string[];
  /** 输入框右下角的模型下拉（T39）：可选模型、选模型、去设置 */
  modelOptions: readonly ModelConnectionOption[];
  onSelectModel: (connection: string, model: string) => Promise<boolean>;
  openSettings: () => void;
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
      {showStages && (
        <StageBar
          line={stages}
          live={trajectory.active}
          meta={detail.taskId ? { modelCalls, elapsedMs } : undefined}
          model={detail.taskId && detail.model ? modelLabel(detail.model) : undefined}
        />
      )}
      {checklist}
      {detail.taskId && traceOpen && (
        <TrajectoryDetails view={trajectory} elapsedMs={elapsedMs} modelCalls={modelCalls} attachments={attachments} artifacts={artifacts} />
      )}
      <div className="chat-body" ref={bodyRef}>
        {items.map((item) => (item.kind === "user" || item.kind === "agent" ? <Message key={item.key} item={item} /> : renderCard(item, actions, session)))}
      </div>
      <ChatInput
        placeholder={detail.readOnly ? "这是旧任务的只读记录，要继续工作请开新对话" : detail.taskId ? "问问这个任务…" : "补充要求，或问问员工…"}
        disabled={detail.readOnly}
        folderFiles={detail.taskId ? [] : folderFiles}
        onUseFolderFile={session.useFolderFiles ? (path) => session.useFolderFiles([path]) : undefined}
        onSend={onSend}
        modelPicker={
          <ModelPicker options={modelOptions} current={detail.model} disabled={detail.readOnly} onPick={onSelectModel} openSettings={openSettings} />
        }
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
  modelOptions,
  defaultModel,
}: {
  preflight: PreflightResult | undefined;
  openSettings: () => void;
  onStart: (text: string) => Promise<boolean>;
  onSample: () => void;
  role: UiRole;
  /** 新会话沿用的模型（设置的 lastModel）：右下角显示，开了会话就定了（T39） */
  modelOptions?: readonly ModelConnectionOption[];
  defaultModel?: SessionDetail["model"];
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
      <ChatInput
        placeholder="例如：帮我出第 44 周的经营周报"
        onSend={onStart}
        modelPicker={
          modelOptions ? (
            <Dropdown
              trigger={["click"]}
              placement="topRight"
              menu={{
                items: [
                  { key: "hint", disabled: true, label: "发出第一句话后就能在会话里换模型" },
                  { key: "settings", label: "去设置里配置模型连接" },
                ],
                onClick: ({ key }) => key === "settings" && openSettings(),
              }}
            >
              <Button size="small" type="text" className="model-picker" icon={<RobotOutlined />} title={modelLabel(defaultModel)}>
                {modelLabel(defaultModel)}
                <DownOutlined />
              </Button>
            </Dropdown>
          ) : undefined
        }
      />
    </>
  );
}
