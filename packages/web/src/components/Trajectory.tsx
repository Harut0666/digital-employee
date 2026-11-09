// 轨迹：当前会话（任务）的只读执行过程记录，固定在对话框顶部。
// 默认只显示状态机和业务话，人点“轨迹详情”才展开这里。数据来自 view/trajectory.ts。
import { useEffect, useRef, useState } from "react";
import { Tag } from "antd";
import {
  BranchesOutlined,
  CheckCircleFilled,
  ClockCircleOutlined,
  CloseCircleFilled,
  DownOutlined,
  ExclamationCircleFilled,
  FileTextOutlined,
  InfoCircleFilled,
  LoadingOutlined,
  MinusCircleOutlined,
  PaperClipOutlined,
  RightOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { TrajectoryDetail, TaskState } from "@de/core/contract";
import type { TraceItem, TrajectoryView } from "../view/trajectory";
import { trajectoryDisplay, shouldAutoExpand, type TrajectoryDisplay } from "../pages/settings/preferences";
import { onUserInput, useLinkProps, useUiPreferences } from "./Preferences";


export function StepIcon({ status }: { status: string }) {
  if (status === "done") return <CheckCircleFilled style={{ color: "var(--primary)" }} />;
  if (status === "failed") return <CloseCircleFilled style={{ color: "var(--danger)" }} />;
  if (status === "running") return <LoadingOutlined style={{ color: "var(--info)" }} />;
  return <MinusCircleOutlined style={{ color: "var(--dot-default)" }} />;
}

const STEP_KIND = { compute: "计算", judge: "判断", qc: "质检" } as const;
const STEP_KIND_COLOR = { compute: "default", judge: "purple", qc: "cyan" } as const;

const fmtTime = (ms: number) => {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
};

const fmtDur = (ms: number | undefined) => {
  if (ms === undefined || ms < 0) return undefined;
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
};

/** 状态点复用左栏会话分组的颜色约定。 */
function dotOf(state: TaskState | undefined): string {
  if (!state || state === "draft" || state === "terminated") return "default";
  if (state === "admission" || state === "running" || state === "qc") return "processing";
  if (state === "delivered") return "success";
  return "warning";
}

function ChildRow({ item, display }: { item: TraceItem; display: TrajectoryDisplay }) {
  const dur = display.showMeta ? fmtDur(item.meta?.durationMs) : undefined;
  const tokens = display.showMeta && item.meta?.usage ? item.meta.usage.input + item.meta.usage.output : undefined;
  // 正在输出的模型增量（T43）不等详细度：边输出边看
  const showDetail = display.showDetail || item.status === "running";
  const icon =
    item.kind === "model" ? <RobotOutlined /> : item.kind === "qc" ? <CloseCircleFilled style={{ color: "var(--danger)" }} /> : <ToolOutlined />;
  return (
    <div className={`trace-child ${item.status}`}>
      <span className="trace-child-icon">{icon}</span>
      <span className="trace-child-label">{item.label}</span>
      {display.showMeta && item.kind === "tool" && item.meta?.attempt !== undefined && item.meta.attempt > 1 && (
        <span className="trace-child-meta">第 {item.meta.attempt} 次尝试</span>
      )}
      {dur && <span className="trace-child-meta">{dur}</span>}
      {tokens !== undefined && <span className="trace-child-meta">{tokens} tokens</span>}
      {item.status === "failed" && item.error && <span className="trace-child-error">{item.error}</span>}
      {showDetail && item.detail && <span className="trace-child-detail">{item.detail}</span>}
      {item.status === "running" && <span className="trace-child-caret" />}
    </div>
  );
}

function TraceRow({ item, detail, focused }: { item: TraceItem; detail: TrajectoryDetail | undefined; focused?: boolean }) {
  const display = trajectoryDisplay(detail);
  const [open, setOpen] = useState<boolean | undefined>();
  const children = item.children ?? [];
  const expandable = children.length > 0;
  // 运行中的步骤自动展开跟随最新事件；其余按详细度决定默认展开
  const expanded = open ?? (expandable && (shouldAutoExpand(detail, item.status) || focused === true));
  const dur = display.showMeta ? fmtDur(item.meta?.durationMs) : undefined;
  const icon =
    item.kind === "step" ? (
      <StepIcon status={item.status} />
    ) : item.kind === "qc" ? (
      <SafetyCertificateOutlined style={{ color: item.meta?.passed ? "var(--primary)" : "var(--danger)" }} />
    ) : item.kind === "state" ? (
      <ClockCircleOutlined style={{ color: "var(--muted)" }} />
    ) : item.status === "failed" ? (
      <ExclamationCircleFilled style={{ color: "var(--danger)" }} />
    ) : (
      <InfoCircleFilled style={{ color: "var(--muted)" }} />
    );
  return (
    <div className="trace-item" data-step-id={item.stepId}>
      <div
        className={`trace-row ${item.status} ${expandable ? "expandable" : ""} ${focused ? "focused" : ""}`}
        onClick={expandable ? () => setOpen(!expanded) : undefined}
        role={expandable ? "button" : undefined}
        aria-expanded={expandable ? expanded : undefined}
        tabIndex={expandable ? 0 : undefined}
        onKeyDown={
          expandable
            ? (e) => {
                if (e.key === "Enter" || e.key === " ") setOpen(!expanded);
              }
            : undefined
        }
      >
        <span className="trace-caret">{expandable ? expanded ? <DownOutlined /> : <RightOutlined /> : null}</span>
        {icon}
        <span className="trace-label">{item.label}</span>
        {item.kind === "step" && item.meta?.stepKind && (
          <Tag className="trace-tag" color={STEP_KIND_COLOR[item.meta.stepKind]} variant="filled">
            {STEP_KIND[item.meta.stepKind]}
          </Tag>
        )}
        {dur && <span className="trace-dur">{dur}</span>}
        {item.status === "failed" && item.error && <span className="trace-error">{item.error}</span>}
        {display.showDetail && item.detail && <span className="trace-detail">{item.detail}</span>}
      </div>
      {expandable && expanded && (
        <div className="trace-children">
          {children.map((child) => (
            <ChildRow key={child.id} item={child} display={display} />
          ))}
        </div>
      )}
    </div>
  );
}

/** 轨迹详情的展开状态：默认收起，人点“轨迹详情”展开；一轮执行结束后按「折叠时机」偏好收起。 */
export function useTrajectoryOpen(active: boolean): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(false);
  const { ui } = useUiPreferences();
  const wasActive = useRef(active);

  useEffect(() => {
    // 完成即折叠：执行刚结束就收起；下次输入时折叠：留到下一次输入
    if (ui.collapseTiming === "completion" && wasActive.current && !active) setOpen(false);
    wasActive.current = active;
  }, [ui.collapseTiming, active]);

  useEffect(() => onUserInput(() => ui.collapseTiming === "next-input" && setOpen(false)), [ui.collapseTiming]);
  return [open, setOpen];
}

export interface TraceFile {
  key: string;
  label: string;
  url?: string;
}

/** 轨迹详情：按轮次与步骤的工具调用、模型轮次、质检，加上任务附件和产物。只读。 */
export function TrajectoryDetails({
  view,
  modelCalls,
  elapsedMs,
  attachments,
  artifacts,
  focusStepId,
}: {
  view: TrajectoryView;
  modelCalls: { used: number; limit: number };
  elapsedMs: number;
  attachments: string[];
  artifacts: TraceFile[];
  /** 从对话里的步骤行点进来时要定位的步骤（T43） */
  focusStepId?: string;
}) {
  const { ui } = useUiPreferences();
  const linkProps = useLinkProps();
  const bodyRef = useRef<HTMLDivElement>(null);

  // 运行中跟随最新事件（像看日志 tail）
  useEffect(() => {
    if (!view.active) return;
    const el = bodyRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [view.active, view.lastSeq]);

  // 点开某一步：滚到这一步（渲染出来之后再滚，等子项展开）
  useEffect(() => {
    if (!focusStepId) return;
    const timer = setTimeout(() => {
      const row = bodyRef.current?.querySelector(`[data-step-id="${CSS.escape(focusStepId)}"]`);
      row?.scrollIntoView({ block: "nearest" });
    }, 0);
    return () => clearTimeout(timer);
  }, [focusStepId, view.lastSeq]);

  const s = view.summary;
  const summary = [`已完成 ${s.done}/${s.steps} 步`, ...(s.failed > 0 ? [`${s.failed} 步失败`] : []), `模型 ${modelCalls.used}/${modelCalls.limit}`, fmtTime(elapsedMs)].join(" · ");

  return (
    <div className="trace open">
      <div className="trace-head static">
        <span className="trace-title">
          <BranchesOutlined /> 轨迹详情
        </span>
        <span className={`dot ${dotOf(view.state)}`} />
        <span className="trace-summary">{summary}</span>
        {view.active && <LoadingOutlined className="trace-live" />}
      </div>
      <div className="trace-body" ref={bodyRef}>
        {view.rounds.length === 0 && <div className="trace-empty">尚未开始执行</div>}
        {view.rounds.map((round) => (
          <div key={round.round} className="trace-round">
            {view.rounds.length > 1 && <div className="trace-round-label">{round.round === 0 ? "首轮" : `重做第 ${round.round} 轮`}</div>}
            {round.items.map((item) => (
              <TraceRow key={item.id} item={item} detail={ui.trajectoryDetail} focused={focusStepId !== undefined && item.stepId === focusStepId} />
            ))}
          </div>
        ))}
        <div className="trace-round">
          <div className="trace-round-label">任务附件</div>
          {attachments.length === 0 && <div className="trace-empty">没有附件</div>}
          {attachments.map((name) => (
            <div key={name} className="trace-child">
              <span className="trace-child-icon"><PaperClipOutlined /></span>
              <span className="trace-child-label">{name}</span>
            </div>
          ))}
        </div>
        <div className="trace-round">
          <div className="trace-round-label">步骤产物</div>
          {artifacts.length === 0 && <div className="trace-empty">还没有产物</div>}
          {artifacts.map((file) => (
            <div key={file.key} className="trace-child">
              <span className="trace-child-icon"><FileTextOutlined /></span>
              {file.url ? (
                <a className="trace-child-label" href={file.url} {...linkProps(file.url)}>
                  {file.label}
                </a>
              ) : (
                <span className="trace-child-label">{file.label}</span>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}