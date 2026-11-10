// 对话里的任务步骤（Spec 0006“员工步骤在对话中的形式”、T43）。
//
// 每个步骤一行：业务说法、状态、耗时、所用模型。点开沿用现有的轨迹详情：由调用方打开轨迹面板并定位到这一步。
// 只展示，不产生任何推进任务的动作。整张列表可折叠：折叠头上一句进度（已完成 3/7 步），
// 步骤都走完时默认收起，有步骤开始跑或失败时自动展开。

import { useEffect, useRef, useState } from "react";
import { DownOutlined, RightOutlined, RobotOutlined } from "@ant-design/icons";
import { stepProgress, type StepRow } from "../view/step-rows";
import { StepIcon } from "./Trajectory";

const STATUS_LABEL: Record<StepRow["status"], string> = {
  pending: "等待",
  running: "进行中",
  done: "完成",
  failed: "失败",
  skipped: "跳过",
};

const fmtDur = (ms: number) => (ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`);

export function StepList({
  rows,
  onOpen,
  title = "任务步骤",
  defaultOpen,
  selected,
}: {
  rows: readonly StepRow[];
  onOpen: (stepId: string) => void;
  title?: string;
  /** 不给时：还有步骤没走完就展开，都走完就收起 */
  defaultOpen?: boolean;
  /** 右栏里正看着详情的那一步：这一行高亮 */
  selected?: string;
}) {
  const progress = stepProgress(rows);
  const [open, setOpen] = useState(defaultOpen ?? !progress.finished);
  // 状态变成进行中或出错时自动展开（人手动收起之后，下一次变化再展开）
  const tone = useRef(progress.tone);
  useEffect(() => {
    if (tone.current !== progress.tone && (progress.tone === "running" || progress.tone === "error")) setOpen(true);
    tone.current = progress.tone;
  }, [progress.tone]);

  return (
    <div className="steps" data-tone={progress.tone}>
      <button type="button" className="steps-head" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="steps-caret" aria-hidden>
          {open ? <DownOutlined /> : <RightOutlined />}
        </span>
        <span className="steps-title">{title}</span>
        <span className={`dot ${progress.tone}`} aria-hidden />
        <span className="steps-summary">{progress.label}</span>
      </button>
      {open && (
        <div className="step-rows" aria-label="任务步骤">
          {rows.map((row) => (
            <button key={row.id} type="button" className={`step-row ${row.status} ${selected === row.id ? "selected" : ""}`} aria-pressed={selected === row.id} onClick={() => onOpen(row.id)} title="查看这一步做了什么">
              <span className="step-row-icon">
                <StepIcon status={row.status} />
              </span>
              <span className="step-row-label">{row.label}</span>
              {row.status === "failed" && row.error && <span className="step-row-error">{row.error}</span>}
              <span className="step-row-meta">
                {row.model && (
                  <span className="step-row-model">
                    <RobotOutlined /> {row.model}
                  </span>
                )}
                {row.durationMs !== undefined && <span className="step-row-dur">{fmtDur(row.durationMs)}</span>}
              </span>
              <span className="step-row-status">{STATUS_LABEL[row.status]}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
