import { Sender } from "@ant-design/x";
import type { SenderRef } from "@ant-design/x/es/sender/interface";
import { Alert, Button, Dropdown, Tooltip } from "antd";
import {
  BranchesOutlined,
  DownOutlined,
  FolderAddOutlined,
  FolderOutlined,
  LockOutlined,
  MessageOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  StopOutlined,
  UnlockOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { useEffect, useMemo, useRef, useState } from "react";
import { STATE_LABELS, type TaskEvent, type TaskState } from "@de/core/contract";
import type { ModelConnectionOption, ModelRef, PermissionMode, PreflightResult, SessionDetail, StepStatusView, UiRole } from "../api";
import { modelLabel, modelMenuItems, newSessionModelMenu, parseModelValue } from "../view/session-model";
import { normalizePermissionMode, permissionHint, permissionMenuItems, permissionModeLabel, permissionTone } from "../view/permission-mode";
import type { ChatItem, LiveTurn } from "../view/chat-view";
import { deriveStages } from "../view/stages";
import { deriveStepRows } from "../view/step-rows";
import { deriveTrajectory } from "../view/trajectory";
import { ROLE_LABELS } from "../view/permissions";
import { composerView, enterHint, STARTERS, type Starter } from "../view/composer";
import type { EmployeeInfo } from "../api/types";
import { stateTone } from "../view/session-row";
import type { NewChatTarget } from "../view/new-chat";
import { renderCard, type CardActions, type SessionActions } from "./Cards";
import { Markdown } from "./Markdown";
import { ToolTurn } from "./ToolRows";
import { useStickToBottom } from "../hooks/stick-to-bottom";
import { atQuery, filterFolderPaths, insertAtPath } from "../view/folder-files";
import { TrajectoryDetails, useTrajectoryOpen, type TraceFile } from "./Trajectory";
import { StepList } from "./StepList";
import { StageBar } from "./StageBar";
import { StateBlock } from "./StateBlock";
import { notifyUserInput, useUiPreferences } from "./Preferences";
import { enterAction } from "../pages/settings/preferences";

/** 对话顶部的状态胶囊：任务状态，语义色与侧栏状态点一致 */
export function StatusPill({ state }: { state: TaskState }) {
  const tone = stateTone(state);
  return (
    <span className="pill" data-tone={tone}>
      <span className={`dot ${tone}`} aria-hidden />
      {STATE_LABELS[state]}
    </span>
  );
}

const clock = (ts: string) => new Date(ts).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });

/**
 * 员工的一条回复：没有头像和气泡，文字铺满内容列，内容走 Markdown。
 * 说话中（streaming）和说完后的正式消息是同一个组件、同一个 key，流式内容被正式消息整体覆盖，不会出现两份。
 */
function AgentMessage({ text, ts, streaming, running }: { text: string; ts?: string; streaming: boolean; running: boolean }) {
  return (
    <div className="msg-row">
      <div className="msg-col">
        <div className={`bubble md-bubble ${streaming ? "streaming" : ""} ${streaming && !running ? "settled" : ""}`}>
          <Markdown text={text} streaming={streaming && running} />
        </div>
        {ts && !streaming && <div className="msg-time">{clock(ts)}</div>}
      </div>
    </div>
  );
}

function Message({ item }: { item: Extract<ChatItem, { kind: "user" }> }) {
  // 不是发起人说的话，标出是谁说的（审核人、操作员也会在对话里说话）
  const other = item.role && item.role !== "initiator" ? item.role : undefined;
  return (
    <div className={`msg-row me ${item.pending ? "pending" : ""}`}>
      <div className="msg-col">
        {other && (
          <div className="msg-who">
            <UserOutlined /> {ROLE_LABELS[other]}
          </div>
        )}
        <div className="bubble">{item.text}</div>
        <div className="msg-time">{item.pending ? "发送中…" : clock(item.ts)}</div>
      </div>
    </div>
  );
}
/**
 * 输入框右下角的模型下拉（T39）：显示“连接 / 模型”，点开按连接分组列出可选模型。
 * 没有可用模型时按钮变成提醒色，菜单里给“去设置”入口。
 */
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
          ? [
              { key: "empty", disabled: true, label: "还没有可用的模型：先到设置里配置 Key 并选好模型" },
              { key: "settings", label: "去设置里配置模型连接" },
            ]
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
      <Button size="small" type="text" className={`model-picker ${empty ? "empty" : ""}`} icon={<RobotOutlined />} aria-label="选择模型" title={empty ? "还没有可用的模型" : modelLabel(current)}>
        <span className="picker-label">{empty ? "未配置模型" : modelLabel(current)}</span>
        <DownOutlined />
      </Button>
    </Dropdown>
  );
}

const MODE_ICONS: Record<PermissionMode, React.ReactNode> = {
  read_only: <LockOutlined />,
  workspace: <SafetyCertificateOutlined />,
  full: <UnlockOutlined />,
};

/** 输入框左下角的权限模式下拉（T48，三档）：显示当前模式，点开列出三个模式和各自的一句说明；完全权限用提醒色。 */
function PermissionModePicker({
  current,
  disabled,
  onPick,
}: {
  current: PermissionMode | undefined;
  disabled?: boolean;
  onPick: (mode: PermissionMode) => Promise<boolean>;
}) {
  const items = permissionMenuItems(current);
  const tone = permissionTone(current);
  return (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      disabled={disabled}
      menu={{
        selectedKeys: items.filter((i) => i.selected).map((i) => i.value),
        items: items.map((item) => ({
          key: item.value,
          icon: MODE_ICONS[item.value],
          label: (
            <div className="mode-item" style={{ maxWidth: 340, whiteSpace: "normal" }}>
              <b style={item.tone === "warn" ? { color: "var(--warn)" } : undefined}>{item.label}</b>
              <span className="chat-sub">{item.hint}</span>
            </div>
          ),
        })),
        onClick: ({ key }) => {
          if (key !== normalizePermissionMode(current)) void onPick(key as PermissionMode);
        },
      }}
    >
      <Button size="small" type="text" className="mode-picker" data-tone={tone} style={tone === "warn" ? { color: "var(--warn)" } : undefined} icon={MODE_ICONS[normalizePermissionMode(current)]} aria-label="权限模式" title={permissionHint(current)} disabled={disabled}>
        <span className="picker-label">{permissionModeLabel(current)}</span>
        <DownOutlined />
      </Button>
    </Dropdown>
  );
}

/**
 * 输入区：一个圆角容器，上方是文本框，底栏左侧权限模式、右侧模型和发送 / 停止。会话页与新对话页共用。
 * 发送立即受理，回复在事件流里逐字出现；失败时保留原文。@ 选目录文件（T41）：选中即视为人已确认。
 * 正在说话时右下角多一个“停止”，但输入框里的字照样能发出去——这时发的话排进这一轮，在下一步之前送达。
 */
function ChatInput({
  placeholder,
  disabled,
  notice,
  modePicker,
  modelPicker,
  folderFiles = [],
  onUseFolderFile,
  onSend,
  streaming,
  onStop,
  draft,
}: {
  placeholder: string;
  disabled?: boolean;
  /** 输入框上方的明确提示（只读会话） */
  notice?: React.ReactNode;
  /** 左下角的权限模式下拉（T48） */
  modePicker?: React.ReactNode;
  modelPicker?: React.ReactNode;
  /** 工作区目录里可选的文件（相对路径） */
  folderFiles?: string[];
  /** 人用 @ 选中的文件：确认作为材料 */
  onUseFolderFile?: (path: string) => Promise<void>;
  onSend: (text: string) => Promise<boolean>;
  /** 正在说话：右下角多一个“停止” */
  streaming?: boolean;
  /** 点“停止” */
  onStop?: () => Promise<void> | void;
  /** 示例起点把话填进输入框：nonce 变了就替换一次 */
  draft?: { text: string; nonce: number };
}) {
  const { ui } = useUiPreferences();
  const [value, setValue] = useState("");
  const [caret, setCaret] = useState(0);
  const [sending, setSending] = useState(false);
  const [stopping, setStopping] = useState(false);
  const areaRef = useRef<SenderRef>(null);
  const word = folderFiles.length > 0 && onUseFolderFile ? atQuery(value, caret) : undefined;
  const candidates = word === undefined ? [] : filterFolderPaths(folderFiles, word);

  useEffect(() => {
    if (!draft) return;
    setValue(draft.text);
    setCaret(draft.text.length);
    requestAnimationFrame(() => areaRef.current?.focus?.());
  }, [draft?.nonce]); // eslint-disable-line react-hooks/exhaustive-deps

  const send = async (text: string) => {
    const trimmed = text.trim();
    if (!trimmed || disabled) return;
    // 通知轨迹：用户开始下一次输入（「下次输入时折叠」用它收起上一轮）
    notifyUserInput();
    setSending(true);
    // 话已经显示在对话里（待确认），输入框立刻清空；没发成功再还回来（输入框里已有新写的字就接在它前面）
    setValue("");
    try {
      if (!(await onSend(trimmed))) setValue((now) => (now.trim() === "" ? trimmed : `${trimmed}\n${now}`));
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
    <div className="composer">
      {notice && (
        <div className="composer-notice" role="status">
          <LockOutlined /> <span>{notice}</span>
        </div>
      )}
      <div className="composer-box">
        {candidates.length > 0 && (
          <div className="at-picker" role="listbox" aria-label="工作区目录里的文件">
            <div className="at-picker-title">工作区目录里的文件</div>
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
          placeholder={placeholder}
          autoSize={{ minRows: 1, maxRows: 8 }}
          // 发送键挪到底栏右侧（footer），文本框这一行只剩输入
          suffix={false}
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
          footer={(_, { components }) => {
            const { SendButton, LoadingButton } = components;
            return (
              <div className="composer-bar">
                <div className="composer-bar-left">{modePicker}</div>
                <div className="composer-bar-right">
                  {modelPicker}
                  {streaming && (
                    <Button className="stop-button" size="small" danger icon={<StopOutlined />} loading={stopping} onClick={() => void stop()} aria-label="停止这一轮">
                      停止
                    </Button>
                  )}
                  <span className="send-slot" title="发送">
                    {sending ? <LoadingButton /> : <SendButton />}
                  </span>
                </div>
              </div>
            );
          }}
        />
      </div>
      {!disabled && <div className="composer-hint">{enterHint(ui.enterBehavior)}</div>}
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
  headerExtra,
  folderFiles,
  modelOptions,
  onSelectModel,
  onSelectMode,
  openSettings,
  onSend,
  draft,
  live,
  onStop,
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
  /** 输入框左下角的权限模式下拉（T48）：选模式 */
  onSelectMode: (mode: PermissionMode) => Promise<boolean>;
  openSettings: () => void;
  /** 会话绑定任务时的事件和步骤，用来画状态机和轨迹 */
  events: TaskEvent[];
  steps: StepStatusView[];
  elapsedMs: number;
  modelCalls: { used: number; limit: number };
  /** 轨迹详情里的任务附件和产物 */
  attachments: string[];
  artifacts: TraceFile[];
  /** 标题栏右侧的附加入口，比如窄窗口下打开右栏抽屉 */
  headerExtra?: React.ReactNode;
  onSend: (text: string) => Promise<boolean>;
  /** 新对话第一句没发出去：话还给输入框（nonce 变了就替换一次） */
  draft?: { text: string; nonce: number };
  /** 正在说的一轮（排在最后一条消息之后） */
  live?: LiveTurn;
  onStop?: () => Promise<void> | void;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const trajectory = useMemo(() => deriveTrajectory(events, steps), [events, steps]);
  // 对话里的步骤行（T43）：每个步骤一行，业务说法、状态、耗时、所用模型；点开定位到轨迹详情
  const stepRows = useMemo(
    () => deriveStepRows(events, steps, { nowMs: Date.now(), fallbackModel: detail.model ? modelLabel(detail.model) : undefined }),
    [events, steps, detail.model],
  );
  const started = stepRows.some((row) => row.status !== "pending");
  // 事件比会话摘要新：有状态变化事件时以它为准（草稿任务没有这种事件）
  const state = trajectory.state ?? detail.taskState;
  const stages = useMemo(() => deriveStages(events, steps, state, actions.pause?.title), [events, steps, state, actions.pause?.title]);
  const [traceOpen, setTraceOpen] = useTrajectoryOpen(trajectory.active);
  const [focusedStep, setFocusedStep] = useState<string>();
  // 点开步骤行：打开轨迹详情并定位到这一步
  const openStep = (stepId: string) => {
    setFocusedStep(stepId);
    setTraceOpen(true);
  };
  // 自动滚动：新消息、逐字增长的回复、卡片展开、窗口缩放都跟着到底；人往上翻了就不打扰，翻回底部附近恢复；
  // 切会话重新贴底，用户自己发了话也回到底部
  const pinToBottom = useStickToBottom(bodyRef, detail.id);
  const lastUserKey = items.filter((i) => i.kind === "user").at(-1)?.key ?? "";
  const seenUserKey = useRef(lastUserKey);
  useEffect(() => {
    if (lastUserKey !== seenUserKey.current) pinToBottom();
    seenUserKey.current = lastUserKey;
  }, [lastUserKey, pinToBottom]);
  const showStages = Boolean(detail.taskId);
  const composer = composerView({
    readOnly: detail.readOnly,
    streaming: Boolean(live?.running),
    hasTask: Boolean(detail.taskId),
    canMention: detail.status !== "task" && (folderFiles?.length ?? 0) > 0,
  });
  return (
    <>
      <div className="chat-head">
        <div className="chat-head-main">
          <div className="chat-title">
            <h2 title={detail.title}>{detail.title}</h2>
            {detail.taskState && <StatusPill state={detail.taskState} />}
            {detail.readOnly && (
              <span className="pill" data-tone="idle" title="由旧任务生成的记录">
                <LockOutlined /> 只读
              </span>
            )}
          </div>
          {subtitle && <div className="chat-sub chat-subtitle">{subtitle}</div>}
        </div>
        {detail.taskId && (
          <Tooltip title={traceOpen ? "收起轨迹详情" : "查看每一步做了什么"}>
            <Button type="text" size="small" className="trace-toggle" icon={<BranchesOutlined />} aria-expanded={traceOpen} onClick={() => setTraceOpen(!traceOpen)}>
              {traceOpen ? "收起轨迹详情" : "轨迹详情"}
            </Button>
          </Tooltip>
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
      {detail.taskId && started && <StepList rows={stepRows} onOpen={openStep} />}
      {detail.taskId && traceOpen && (
        <TrajectoryDetails
          view={trajectory}
          elapsedMs={elapsedMs}
          modelCalls={modelCalls}
          attachments={attachments}
          artifacts={artifacts}
          focusStepId={focusedStep}
        />
      )}
      <div className="chat-body thread" ref={bodyRef}>
        {items.length === 0 && (
          <StateBlock
            icon={<MessageOutlined />}
            tone="brand"
            title={detail.readOnly ? "这条记录里没有对话" : "还没有消息"}
            description={detail.readOnly ? "它由旧任务生成，只保留任务本身的步骤和交付物。" : "说说要做什么，或者问问员工能帮上什么。"}
          />
        )}
        {items.map((item) =>
          item.kind === "user" ? (
            <Message key={item.key} item={item} />
          ) : item.kind === "agent" ? (
            <AgentMessage key={item.key} text={item.text} ts={item.ts} streaming={false} running={false} />
          ) : item.kind === "streaming" ? (
            <AgentMessage key={item.key} text={item.text} streaming running={item.running} />
          ) : item.kind === "tools" ? (
            <ToolTurn key={item.key} item={item} />
          ) : (
            renderCard(item, actions, session)
          ),
        )}
      </div>
      <ChatInput
        placeholder={composer.placeholder}
        disabled={composer.disabled}
        notice={composer.notice}
        folderFiles={detail.status === "task" ? [] : folderFiles}
        onUseFolderFile={session.useFolderFiles ? (path) => session.useFolderFiles([path]) : undefined}
        onSend={onSend}
        draft={draft}
        modePicker={<PermissionModePicker current={detail.permissionMode} disabled={detail.readOnly} onPick={onSelectMode} />}
        modelPicker={
          <ModelPicker options={modelOptions} current={detail.model} disabled={detail.readOnly} onPick={onSelectModel} openSettings={openSettings} />
        }
        streaming={composer.showStop}
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

/**
 * 新对话页右下角的模型下拉（T45）：和会话里的下拉同一份数据，选中的模型决定这个会话从第一句话起用哪个；
 * 没有可用模型时写“还没有可用的模型”并给设置入口。
 */
function NewSessionModelPicker({
  options,
  current,
  onPick,
  openSettings,
}: {
  options: readonly ModelConnectionOption[];
  current: SessionDetail["model"];
  onPick: (model: ModelRef) => void;
  openSettings: () => void;
}) {
  const menu = newSessionModelMenu(options, current);
  return (
    <Dropdown
      trigger={["click"]}
      placement="topRight"
      menu={{
        selectedKeys: menu.selectedValue ? [menu.selectedValue] : [],
        items: menu.items.map((item) =>
          item.type === "group"
            ? { type: "group" as const, key: item.key, label: item.label, children: item.children }
            : { key: item.key, label: item.label, disabled: item.type === "item" && item.disabled },
        ),
        onClick: ({ key }) => {
          if (key === "settings") {
            openSettings();
            return;
          }
          if (key === "empty") return;
          const picked = parseModelValue(key);
          if (picked.connection && picked.model) onPick(picked);
        },
      }}
    >
      <Button size="small" type="text" className={`model-picker ${menu.empty ? "empty" : ""}`} icon={<RobotOutlined />} aria-label="选择模型" title={menu.label}>
        <span className="picker-label">{menu.label}</span>
        <DownOutlined />
      </Button>
    </Dropdown>
  );
}

/** 新对话开在哪个工作区：只有一个就写出来，有多个可以更换。 */
function WorkspaceTarget({ target, onChange }: { target: NewChatTarget; onChange?: (workspaceId: string) => void }) {
  if (!target.current) return null;
  const label = (
    <span className="target-chip">
      <FolderOutlined /> 工作区：<b>{target.current.name}</b>
      {target.choices.length > 0 && <DownOutlined />}
    </span>
  );
  if (target.choices.length === 0) return <div className="welcome-target">{label}</div>;
  return (
    <div className="welcome-target">
      <Dropdown
        trigger={["click"]}
        menu={{
          selectedKeys: [target.current.id],
          items: target.choices.map((choice) => ({ key: choice.id, label: choice.disabled ? `${choice.name}（目录缺失）` : choice.name, disabled: choice.disabled })),
          onClick: ({ key }) => onChange?.(key),
        }}
      >
        <button type="button" className="target-button" aria-label="更换工作区">
          {label}
        </button>
      </Dropdown>
    </div>
  );
}

export function NewConversation({
  preflight,
  openSettings,
  onStart,
  onSample,
  employees,
  onPickEmployee,
  role,
  modelOptions,
  defaultModel,
  target,
  onChangeWorkspace,
  onAddWorkspace,
}: {
  preflight: PreflightResult | undefined;
  openSettings: () => void;
  /** 发出第一句话：带上右下角选中的模型（没选时 undefined，服务端沿用 lastModel） */
  onStart: (text: string, model?: ModelRef) => Promise<boolean>;
  onSample: () => void;
  /** “直接找员工”：列出的员工，点一下不经过前台 Agent，直接打开这位员工的空白表单 */
  employees?: readonly EmployeeInfo[];
  onPickEmployee?: (employee: EmployeeInfo) => void;
  role: UiRole;
  /** 新会话沿用的模型（设置的 lastModel）：右下角先显示它，可以有可用模型时换（T39、T45） */
  modelOptions?: readonly ModelConnectionOption[];
  defaultModel?: SessionDetail["model"];
  /** 新对话开在哪个工作区，或被拦住（先添加工作区） */
  target?: NewChatTarget;
  onChangeWorkspace?: (workspaceId: string) => void;
  onAddWorkspace?: () => void;
}) {
  // 新对话页选的模型：没换过时显示设置里的 lastModel；换过就是人选的
  const [picked, setPicked] = useState<ModelRef | undefined>();
  const [draft, setDraft] = useState<{ text: string; nonce: number }>();
  const selected = picked ?? defaultModel;
  const blocked = target?.blocked;
  const startWith = (starter: Starter) => {
    if (starter.action === "sample") onSample();
    else setDraft({ text: starter.text ?? "", nonce: (draft?.nonce ?? 0) + 1 });
  };
  return (
    <>
      <div className="chat-body thread welcome-body">
        <div className="welcome">
          <div className="welcome-mark" aria-hidden>
            <RobotOutlined />
          </div>
          <h1 className="welcome-title">你好，说说要做什么</h1>
          <p className="welcome-desc">用一句话说明工作，我来安排合适的员工接手，再帮你备齐材料。</p>
          <PreflightNotice preflight={preflight} openSettings={openSettings} />
          {blocked ? (
            <StateBlock
              tone="brand"
              icon={<FolderAddOutlined />}
              title="先添加一个工作区"
              description={
                blocked.canAdd
                  ? blocked.reason === "missing"
                    ? "已登记的工作区目录都找不到了。添加一个可用的目录，才能开始新对话。"
                    : "工作区就是一个本机目录，对话和文件都放在里面。添加之后才能开始新对话。"
                  : (blocked.hint ?? "工作区由环境变量指定，不能添加")
              }
              actions={
                blocked.canAdd && (
                  <Button type="primary" icon={<FolderAddOutlined />} onClick={onAddWorkspace}>
                    添加工作区
                  </Button>
                )
              }
            />
          ) : (
            <>
              {target && <WorkspaceTarget target={target} onChange={onChangeWorkspace} />}
              {employees && employees.length > 0 && onPickEmployee && (
                <>
                  <div className="welcome-section">直接找员工</div>
                  <div className="starters employee-entries" role="list" aria-label="直接找员工">
                    {employees.map((employee) => (
                      <button
                        key={employee.id}
                        type="button"
                        role="listitem"
                        className="starter employee-entry"
                        disabled={role !== "initiator"}
                        title={role !== "initiator" ? "只有发起人能找员工开工" : undefined}
                        onClick={() => onPickEmployee(employee)}
                      >
                        <span className="starter-title">{employee.name}</span>
                        <span className="starter-desc">{employee.description}</span>
                      </button>
                    ))}
                  </div>
                  <div className="welcome-section">或者说一句话，让前台安排</div>
                </>
              )}
              <div className="starters" role="list" aria-label="示例起点">
                {STARTERS.map((starter) => (
                  <button key={starter.key} type="button" role="listitem" className="starter" onClick={() => startWith(starter)}>
                    <span className="starter-title">{starter.title}</span>
                    <span className="starter-desc">{starter.description}</span>
                  </button>
                ))}
              </div>
            </>
          )}
          {role !== "initiator" && <p className="chat-sub welcome-role">当前角色是{ROLE_LABELS[role]}；只有发起人能确认员工表单、让员工开工。</p>}
        </div>
      </div>
      <ChatInput
        placeholder={blocked ? "先添加工作区，再开始对话" : "例如：帮我出第 44 周的经营周报"}
        disabled={Boolean(blocked)}
        draft={draft}
        onSend={(text) => onStart(text, picked)}
        modelPicker={
          modelOptions ? (
            <NewSessionModelPicker options={modelOptions} current={selected} onPick={setPicked} openSettings={openSettings} />
          ) : undefined
        }
      />
    </>
  );
}
