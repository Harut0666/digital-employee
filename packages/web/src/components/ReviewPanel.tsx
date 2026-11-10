// 右栏：上方交付物预览（单独滚动），下方固定检验记录、修改说明和审核操作。不分标签页。
import { useEffect, useState } from "react";
import { Button, Collapse, Dropdown, message, Modal, Segmented, Tooltip } from "antd";
import { CheckCircleFilled, CloseCircleFilled, DownloadOutlined, DownOutlined, ExportOutlined, InboxOutlined } from "@ant-design/icons";
import type { TaskSnapshot } from "../api";
import { deriveReviewPanel, openMenu, previewSource } from "../view/review-panel";
import type { EarlierDelivery, FilingView } from "../view/delivery";
import { actionPlan } from "../view/permissions";
import { DocxPreview } from "./DocxPreview";
import { MarkdownPreview } from "./MarkdownPreview";
import { Handoff, ReturnForm, TaskActionBar, type CardActions, type SessionActions } from "./Cards";
import { FilingCard } from "./FilingCard";
import { useLinkProps } from "./Preferences";
import { StateBlock } from "./StateBlock";

const FILE_LABELS: Record<string, string> = { docx: "Word 文档", md: "Markdown 简报", qc_record: "检验记录" };

interface ReviewPanelProps {
  snapshot: TaskSnapshot | undefined;
  actions: CardActions;
  /** 入库确认要用的会话操作（起草描述、放入知识库） */
  session: SessionActions;
  /** 当前任务的入库确认；交付之后才有 */
  filing?: FilingView;
  /** 会话里早先交付的任务 */
  earlier?: EarlierDelivery[];
}

export function ReviewPanel({ snapshot, actions, session, filing, earlier = [] }: ReviewPanelProps) {
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
        </div>
        <StateBlock icon={<InboxOutlined />} title="还没有交付物" description="员工完成后，交付物、检验记录和审核操作会显示在这里。" />
        <EarlierDeliveries earlier={earlier} actions={actions} session={session} />
      </div>
    );
  }

  const version = view.latest ? undefined : view.selected;
  const docUrl = view.docKind ? actions.downloadUrl(view.docKind, version) : undefined;
  // 预览按地址刷新：最新版的下载地址不带版本，重做后要靠版本号区分，否则一直显示旧版
  const previewUrl = previewSource(docUrl, view.selected);
  const pause = view.reviewing && snapshot.pause?.code !== "awaiting_review" ? snapshot.pause : undefined;
  const plan = actionPlan(view.reviewing ? actions.taskActions : [], view.reviewing ? actions.waitingFor : [], actions.role);
  const menu = openMenu(view.files, view.docKind);
  // 交付之后：交付物下载、放入知识库、早先任务的交付物，都在文档下面这一块
  const delivered = snapshot.state === "delivered" && view.latest;

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
        {menu.length > 0 && (
          <Dropdown
            trigger={["click"]}
            menu={{
              items: menu.flatMap((item, index) => [
                ...(item.group === "other" && menu[index - 1]?.group === "main" ? [{ type: "divider" as const, key: `divider-${index}` }] : []),
                { key: item.key, label: item.label },
              ]),
              onClick: ({ key }) => {
                const picked = menu.find((item) => item.key === key);
                if (picked) void actions.openFile(picked.kind, picked.mode, version);
              },
            }}
          >
            <Button size="small" icon={<ExportOutlined />}>
              打开 <DownOutlined />
            </Button>
          </Dropdown>
        )}
        <Tooltip title={docUrl ? "下载" : "模拟模式没有文件"}>
          <Button type="text" icon={<DownloadOutlined />} href={docUrl} disabled={!docUrl} {...linkProps(docUrl)} />
        </Tooltip>
      </div>

      <div className="review-doc">
        {view.docKind === "md" ? (
          <MarkdownPreview key={previewUrl ?? "placeholder"} url={previewUrl} />
        ) : (
          <DocxPreview key={previewUrl ?? "placeholder"} url={previewUrl} reflow />
        )}
      </div>

      {(delivered || earlier.length > 0) && (
        <div className="review-delivery">
          {delivered && (
            <>
              <div className="review-row">
                <b>
                  <CheckCircleFilled className="review-ok" /> 已交付
                </b>
                <span className="review-files">
                  {view.files.map((kind) => {
                    const url = actions.downloadUrl(kind, undefined);
                    return (
                      <Button key={kind} size="small" icon={<DownloadOutlined />} href={url} disabled={!url} {...linkProps(url)}>
                        {FILE_LABELS[kind] ?? kind}
                      </Button>
                    );
                  })}
                </span>
              </div>
              {filing && <FilingCard key={`${filing.taskId}-${filing.description}-${filing.filed ?? ""}`} item={filing} actions={actions} session={session} />}
            </>
          )}
          <EarlierDeliveries earlier={earlier} actions={actions} session={session} />
        </div>
      )}

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

/** 会话里早先交付的任务：交付物下载和它的入库确认，默认收起 */
function EarlierDeliveries({ earlier, actions, session }: { earlier: EarlierDelivery[]; actions: CardActions; session: SessionActions }) {
  const linkProps = useLinkProps();
  if (earlier.length === 0) return null;
  return (
    <Collapse
      size="small"
      className="review-earlier"
      items={earlier.map((run) => ({
        key: run.taskId,
        label: `早先的交付 · 第 ${run.run} 次运行`,
        children: (
          <div className="review-earlier-body">
            <span className="review-files">
              {run.deliverables.map((d) => {
                const url = actions.downloadUrl(d.kind, undefined, run.taskId);
                return (
                  <Button key={d.kind} size="small" icon={<DownloadOutlined />} href={url} disabled={!url} {...linkProps(url)}>
                    {FILE_LABELS[d.kind] ?? d.kind}
                  </Button>
                );
              })}
            </span>
            {run.filing && <FilingCard key={`${run.filing.taskId}-${run.filing.description}-${run.filing.filed ?? ""}`} item={run.filing} actions={actions} session={session} />}
          </div>
        ),
      }))}
    />
  );
}