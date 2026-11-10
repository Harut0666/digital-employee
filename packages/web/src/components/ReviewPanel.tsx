// 右栏：上方交付物预览（单独滚动），下方固定检验记录、修改说明和审核操作。不分标签页。
import { useEffect, useState } from "react";
import { Button, message, Modal, Segmented, Tooltip } from "antd";
import { CheckCircleFilled, CloseCircleFilled, DownloadOutlined, InboxOutlined } from "@ant-design/icons";
import type { TaskSnapshot } from "../api";
import { deriveReviewPanel } from "../view/review-panel";
import { actionPlan } from "../view/permissions";
import { DocxPreview } from "./DocxPreview";
import { MarkdownPreview } from "./MarkdownPreview";
import { PanelClose } from "./PanelClose";
import { Handoff, ReturnForm, TaskActionBar, type CardActions } from "./Cards";
import { useLinkProps } from "./Preferences";
import { StateBlock } from "./StateBlock";

export function ReviewPanel({ snapshot, actions, onClose }: { snapshot: TaskSnapshot | undefined; actions: CardActions; onClose?: () => void }) {
  const [selected, setSelected] = useState<number>();
  const [returning, setReturning] = useState(false);
  const linkProps = useLinkProps();
  const versionCount = snapshot?.versions?.length ?? 0;
  // 出了新版本就回到最新一版
  useEffect(() => setSelected(undefined), [snapshot?.id, versionCount]);

  const view = deriveReviewPanel(
    { state: snapshot?.state, versions: snapshot?.versions ?? [], qc: snapshot?.qc, downloadNames: snapshot?.downloadNames },
    selected,
  );

  if (!snapshot || view.selected === undefined) {
    return (
      <div className="review">
        <div className="review-head">
          <span className="review-label">交付物</span>
          <span className="review-name" />
          {onClose && <PanelClose onClose={onClose} />}
        </div>
        <StateBlock icon={<InboxOutlined />} title="还没有交付物" description="员工完成后，交付物、检验记录和审核操作会显示在这里。" />
      </div>
    );
  }

  const version = view.latest ? undefined : view.selected;
  const docUrl = view.docKind ? actions.downloadUrl(view.docKind, version) : undefined;
  const pause = view.reviewing && snapshot.pause?.code !== "awaiting_review" ? snapshot.pause : undefined;
  const plan = actionPlan(view.reviewing ? actions.taskActions : [], view.reviewing ? actions.waitingFor : [], actions.role);

  return (
    <div className="review">
      <div className="review-head">
        <span className="review-label">交付物</span>
        <span className="review-name" title={view.fileName}>
          {view.fileName ?? (view.docKind === "md" ? "Markdown 报告" : "Word 周报")}
        </span>
        {view.versions.length > 1 && (
          <Segmented
            size="small"
            value={view.selected}
            onChange={(v) => setSelected(v as number)}
            options={view.versions.map((v) => ({ label: v.label, value: v.version }))}
          />
        )}
        <Tooltip title={docUrl ? "下载" : "模拟模式没有文件"}>
          <Button type="text" icon={<DownloadOutlined />} href={docUrl} disabled={!docUrl} {...linkProps(docUrl)} />
        </Tooltip>
        {onClose && <PanelClose onClose={onClose} />}
      </div>

      <div className="review-doc">
        {view.docKind === "md" ? (
          <MarkdownPreview key={docUrl ?? "placeholder"} url={docUrl} />
        ) : (
          <DocxPreview key={docUrl ?? "placeholder"} url={docUrl} reflow />
        )}
      </div>

      <div className="review-foot">
        <div className="review-info">
          {view.qc && (
            <div className="review-row">
              <b>检验记录</b>
              <span className={view.qc.passed ? "review-ok" : "review-bad"}>
                {view.qc.passed ? <CheckCircleFilled /> : <CloseCircleFilled />}{" "}
                {view.qc.passed ? `${view.qc.total} 项质检全部通过` : `${view.qc.failed} 项未通过`}
              </span>
            </div>
          )}
          {view.qc && (
            <div className="chips review-chips">
              {view.qc.checks.map((c) => (
                <span key={c.id} className={`chip ${c.passed ? "" : "bad"}`}>
                  {c.label}
                </span>
              ))}
            </div>
          )}
          {view.feedback && (
            <div className="review-row">
              <b>退回意见</b>
              <span>{view.feedback}</span>
            </div>
          )}
          {view.changes.length > 0 && (
            <div className="review-row">
              <b>这版改了什么</b>
              <span>{view.changes.join("；")}</span>
            </div>
          )}
          {view.returnedWith && (
            <div className="review-row">
              <b>这版被退回</b>
              <span>{view.returnedWith}</span>
            </div>
          )}
          {!view.latest && <div className="chat-sub">正在查看历史版本，审核请切回最新一版。</div>}
          {pause && (
            <div className="review-pause">
              <b>{pause.title}</b>
              <div>{pause.message}</div>
              {pause.resolution && <div>{pause.resolution}</div>}
            </div>
          )}
          {snapshot.state === "delivered" && view.latest && <div className="review-ok">已交付</div>}
        </div>
        {view.reviewing && (
          <div className="review-actions">
            {plan.mine.length > 0 && (
              <TaskActionBar actions={actions} handlers={{ return: () => setReturning(true) }} />
            )}
            {plan.mine.length === 0 && plan.handoff && <Handoff handoff={plan.handoff} onRole={actions.onRole} />}
          </div>
        )}
      </div>
      <ReturnDialog open={returning} actions={actions} onClose={() => setReturning(false)} />
    </div>
  );
}

function ReturnDialog({ open, actions, onClose }: { open: boolean; actions: CardActions; onClose: () => void }) {
  const [picked, setPicked] = useState<string[]>([]);
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      open={open}
      title="退回修改"
      okText="确认退回"
      cancelText="取消"
      confirmLoading={busy}
      onCancel={onClose}
      onOk={async () => {
        if (!comment.trim()) {
          message.warning("请填写退回意见");
          return;
        }
        setBusy(true);
        try {
          await actions.run({ type: "return", comment: comment.trim(), sections: picked.length ? picked : undefined });
          setComment("");
          setPicked([]);
          onClose();
        } finally {
          setBusy(false);
        }
      }}
    >
      <ReturnForm sections={actions.sections} picked={picked} onPick={setPicked} comment={comment} onComment={setComment} />
    </Modal>
  );
}
