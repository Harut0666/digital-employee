// 空状态 / 出错 / 加载的统一版式：图标 + 一句标题 + 一句说明 + 操作。样式见 styles/base.css。
import { Skeleton } from "antd";

export type StateTone = "idle" | "brand" | "waiting" | "error";

export function StateBlock({
  icon,
  title,
  description,
  tone = "idle",
  actions,
  compact,
}: {
  icon: React.ReactNode;
  title: string;
  description?: React.ReactNode;
  tone?: StateTone;
  actions?: React.ReactNode;
  /** 侧栏等窄处用的紧凑版 */
  compact?: boolean;
}) {
  return (
    <div className={`state-block ${compact ? "compact" : ""}`} data-tone={tone} role={tone === "error" ? "alert" : undefined}>
      <div className="state-icon" aria-hidden>
        {icon}
      </div>
      <div className="state-title">{title}</div>
      {description && <div className="state-desc">{description}</div>}
      {actions && <div className="state-actions">{actions}</div>}
    </div>
  );
}

/** 读取中的占位：几行骨架，比一行灰字安心 */
export function LoadingBlock({ rows = 4, label = "正在读取…" }: { rows?: number; label?: string }) {
  return (
    <div className="state-skeleton" role="status" aria-label={label}>
      <Skeleton active title={{ width: "40%" }} paragraph={{ rows }} />
    </div>
  );
}
