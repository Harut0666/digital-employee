// 对话里的任务步骤（Spec 0006“员工步骤在对话中的形式”、T43）。
//
// 每个步骤一行：业务说法、状态、耗时、所用模型。点开沿用现有的轨迹详情：由调用方打开轨迹面板并定位到这一步。
// 只展示，不产生任何推进任务的动作。

import { RobotOutlined } from "@ant-design/icons";
import type { StepRow } from "../view/step-rows";
import { StepIcon } from "./Trajectory";

const STATUS_LABEL: Record<StepRow["status"], string> = {
  pending: "等待",
  running: "进行中",
  done: "完成",
  failed: "失败",
  skipped: "跳过",
};

const fmtDur = (ms: number) => (ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`);

export function StepList({ rows, onOpen }: { rows: readonly StepRow[]; onOpen: (stepId: string) => void }) {
  return (
    <div className="step-rows" aria-label="任务步骤">
      {rows.map((row) => (
        <button
          key={row.id}
          type="button"
          className={`step-row ${row.status}`}
          onClick={() => onOpen(row.id)}
          title="在轨迹详情里查看这一步"
        >
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
  );
}
