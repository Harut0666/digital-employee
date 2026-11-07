// 开工清单：对话顶部、状态机下方。开工前展开，逐项说明并可确认跳过；开工后收成一行，点开可看明细。
// 勾选只由后端判定，这里没有勾选按钮。质检结果用同一套行样式（CheckRows）。

import { useState } from "react";
import { Button } from "antd";
import { CheckCircleFilled, CloseCircleFilled, DownOutlined, MinusCircleOutlined, RightOutlined, StopOutlined } from "@ant-design/icons";
import type { SessionChecklist } from "../api";
import { checklistBar, type CheckRow, type CheckTone } from "../view/checklist";

const ICONS: Record<CheckTone, React.ReactNode> = {
  ok: <CheckCircleFilled />,
  bad: <CloseCircleFilled />,
  todo: <MinusCircleOutlined />,
  skip: <StopOutlined />,
};

export function CheckRows({ rows, onSkip, busy }: { rows: CheckRow[]; onSkip?: (key: string) => void; busy?: boolean }) {
  return (
    <div className="check-rows">
      {rows.map((r) => (
        <div key={r.key} className={`check-row tone-${r.tone}`}>
          <span className="check-icon">{ICONS[r.tone]}</span>
          <span className="check-label">
            {r.label}
            {r.detail && <span className="check-detail">{r.detail}</span>}
          </span>
          {r.skipLabel && onSkip && (
            <Button size="small" disabled={busy} onClick={() => onSkip(r.key)}>
              {r.skipLabel}
            </Button>
          )}
          <span className="state">{r.stateText}</span>
        </div>
      ))}
    </div>
  );
}

export function ChecklistPanel({
  checklist,
  started,
  canSkip,
  onSkip,
}: {
  checklist: SessionChecklist;
  started: boolean;
  /** 当前角色能否确认跳过（只有发起人） */
  canSkip: boolean;
  onSkip: (item: string) => Promise<void>;
}) {
  const bar = checklistBar(checklist, { started });
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const skip = async (item: string) => {
    setBusy(true);
    try {
      await onSkip(item);
    } finally {
      setBusy(false);
    }
  };

  if (bar.mode === "collapsed") {
    return (
      <div className={`checklist collapsed ${bar.ready ? "ready" : ""}`}>
        <button type="button" className="checklist-line" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="check-icon">{bar.ready ? <CheckCircleFilled /> : <MinusCircleOutlined />}</span>
          <span>{bar.headline}</span>
          <span className="checklist-toggle">{open ? <DownOutlined /> : <RightOutlined />}</span>
        </button>
        {open && <CheckRows rows={bar.rows} />}
      </div>
    );
  }
  return (
    <div className={`checklist ${bar.ready ? "ready" : ""}`}>
      <div className="checklist-line">
        <span className="check-icon">{bar.ready ? <CheckCircleFilled /> : <MinusCircleOutlined />}</span>
        <span>{bar.headline}</span>
      </div>
      <CheckRows rows={bar.rows} onSkip={canSkip ? (key) => void skip(key) : undefined} busy={busy} />
      <div className="checklist-note">由程序按上传的文件和表单勾选；文件内容是否符合要求，开工后由准入校验检查。</div>
    </div>
  );
}
