// 轨迹：当前会话（任务）的只读执行过程记录，固定在对话框顶部。
// 折叠时一行摘要；运行中自动展开并跟随最新事件，暂停/终态默认折叠。数据来自 view/trajectory.ts。
import { useEffect, useRef, useState } from "react";
import { Tag } from "antd";
import {
  BranchesOutlined,
  ClockCircleOutlined,
  CloseCircleFilled,
  DownOutlined,
  ExclamationCircleFilled,
  InfoCircleFilled,
  LoadingOutlined,
  RightOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { TaskState } from "@de/core/contract";
import type { TraceItem, TrajectoryView } from "../view/trajectory";
import { StepIcon } from "./TaskPanel";

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

function ChildRow({ item }: { item: TraceItem }) {
  const dur = fmtDur(item.meta?.durationMs);
  const tokens = item.meta?.usage ? item.meta.usage.input + item.meta.usage.output : undefined;
  const icon =
    item.kind === "model" ? <RobotOutlined /> : item.kind === "qc" ? <CloseCircleFilled style={{ color: "var(--danger)" }} /> : <ToolOutlined />;
  return (
    <div className={`trace-child ${item.status}`}>
      <span className="trace-child-icon">{icon}</span>
      <span className="trace-child-label">{item.label}</span>
      {item.kind === "tool" && item.meta?.attempt !== undefined && item.meta.attempt > 1 && (
        <span className="trace-child-meta">第 {item.meta.attempt} 次尝试</span>
      )}
      {dur && <span className="trace-child-meta">{dur}</span>}
      {tokens !== undefined && <span className="trace-child-meta">{tokens} tokens</span>}
      {item.status === "failed" && item.error && <span className="trace-child-error">{item.error}</span>}
      {item.detail && <span className="trace-child-detail">{item.detail}</span>}
    </div>
  );
}

function TraceRow({ item }: { item: TraceItem }) {
  const [open, setOpen] = useState<boolean | undefined>();
  const children = item.children ?? [];
  const expandable = children.length > 0;
  // 运行中的步骤自动展开；其余默认折叠，点击展开历史细节
  const expanded = open ?? (item.kind === "step" && item.status === "running");
  const dur = fmtDur(item.meta?.durationMs);
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
    <div className="trace-item">
      <div
        className={`trace-row ${item.status} ${expandable ? "expandable" : ""}`}
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
        {item.detail && <span className="trace-detail">{item.detail}</span>}
      </div>
      {expandable && expanded && (
        <div className="trace-children">
          {children.map((child) => (
            <ChildRow key={child.id} item={child} />
          ))}
        </div>
      )}
    </div>
  );
}

export function Trajectory({
  view,
  elapsedMs,
  modelCalls,
}: {
  view: TrajectoryView;
  elapsedMs: number;
  modelCalls: { used: number; limit: number };
}) {
  const [userOpen, setUserOpen] = useState<boolean | undefined>();
  const expanded = userOpen ?? view.active;
  const bodyRef = useRef<HTMLDivElement>(null);

  // 运行中展开时跟随最新事件（像看日志 tail）
  useEffect(() => {
    if (!expanded || !view.active) return;
    const el = bodyRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [expanded, view.active, view.lastSeq]);

  const s = view.summary;
  const summary = [`已完成 ${s.done}/${s.steps} 步`, ...(s.failed > 0 ? [`${s.failed} 步失败`] : []), `模型 ${modelCalls.used}/${modelCalls.limit}`, fmtTime(elapsedMs)].join(" · ");

  return (
    <div className={`trace ${expanded ? "open" : ""}`}>
      <div
        className="trace-head"
        onClick={() => setUserOpen(!expanded)}
        role="button"
        aria-expanded={expanded}
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") setUserOpen(!expanded);
        }}
      >
        <span className="trace-title">
          <BranchesOutlined /> 轨迹
        </span>
        <span className={`dot ${dotOf(view.state)}`} />
        <span className="trace-summary">{summary}</span>
        {view.active && <LoadingOutlined className="trace-live" />}
        <span className="trace-arrow">{expanded ? <DownOutlined /> : <RightOutlined />}</span>
      </div>
      {expanded && (
        <div className="trace-body" ref={bodyRef}>
          {view.rounds.length === 0 && <div className="trace-empty">尚未开始执行</div>}
          {view.rounds.map((round) => (
            <div key={round.round} className="trace-round">
              {view.rounds.length > 1 && <div className="trace-round-label">{round.round === 0 ? "首轮" : `重做第 ${round.round} 轮`}</div>}
              {round.items.map((item) => (
                <TraceRow key={item.id} item={item} />
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
