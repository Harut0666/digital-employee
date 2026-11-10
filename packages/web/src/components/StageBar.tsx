// 轨迹状态机：发起、准入、执行、质检、审核、交付，下方一行业务话。数据来自 view/stages.ts。
import { CheckOutlined, CloseOutlined, LoadingOutlined, PauseOutlined } from "@ant-design/icons";
import type { StageLine } from "../view/stages";

const fmtTime = (ms: number) => {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
};

export function StageBar({
  line,
  live,
  meta,
  model,
}: {
  line: StageLine;
  live: boolean;
  meta?: { modelCalls: { used: number; limit: number }; elapsedMs: number };
  /** 任务当前的会话模型（“连接 / 模型”），轨迹里核对用的是哪个模型（T39） */
  model?: string;
}) {
  return (
    <div className="stages">
      <ol className="stage-track" aria-label="任务进度">
        {line.stages.map((s) => (
          <li key={s.key} className={`stage ${s.status}`} aria-current={s.status === "current" || s.status === "paused" ? "step" : undefined}>
            <span className="stage-dot">
              {s.status === "done" && <CheckOutlined />}
              {s.status === "paused" && <PauseOutlined />}
              {s.status === "stopped" && <CloseOutlined />}
            </span>
            <span className="stage-label">{s.label}</span>
          </li>
        ))}
      </ol>
      <div className="stage-activity">
        {live && <LoadingOutlined className="trace-live" />}
        <span>{line.activity}</span>
        {meta && meta.modelCalls.limit > 0 && (
          <span className="stage-meta">
            {model ? `${model} · ` : ""}模型 {meta.modelCalls.used}/{meta.modelCalls.limit} · 用时 {fmtTime(meta.elapsedMs)}
          </span>
        )}
      </div>
    </div>
  );
}
