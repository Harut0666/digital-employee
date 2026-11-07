import { useState } from "react";
import { Button, Checkbox, Input, message, Tag } from "antd";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  DownloadOutlined,
  ExclamationCircleFilled,
  FileWordOutlined,
  ReloadOutlined,
  SettingOutlined,
  SwapOutlined,
} from "@ant-design/icons";
import { STATE_LABELS, type TaskCommand } from "@de/core/contract";
import { actionPlan, needsModelSettings, ROLE_LABELS, type RoleHandoff } from "../view/permissions";
import type { ChatCard } from "../view/session-view";
import type { CommandType, PauseView, TaskAction, UiRole } from "../api";
import { useLinkProps } from "./Preferences";

export interface CardActions {
  role: UiRole;
  /** 快照给出的可用操作和在等的角色；快照还没跟上事件时为空 */
  taskActions: TaskAction[];
  waitingFor: UiRole[];
  /** 快照里的暂停原因，比事件里的多 title、resolution */
  pause: PauseView | undefined;
  /** 发命令；resolve 后才恢复按钮 */
  run: (cmd: TaskCommand) => Promise<void>;
  onRole: (role: UiRole) => void;
  openSettings: () => void;
  /** 聚焦右栏的补充表单 */
  openSupplement: () => void;
  /** 可勾选的退回章节（取自员工配置 qc.yaml）；退回命令里带的是 id，界面显示 heading */
  sections: { id: string; heading: string }[];
  downloadUrl: (kind: "docx" | "md" | "qc_record") => string | undefined;
}

const clock = (ts: string) => new Date(ts).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });

function ActionButton({ action, run, onClick }: { action: TaskAction; run: CardActions["run"]; onClick?: () => Promise<void> | void }) {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      type={action.style === "primary" ? "primary" : "default"}
      danger={action.style === "danger"}
      loading={busy}
      icon={action.command === "retry" ? <ReloadOutlined /> : undefined}
      onClick={async () => {
        setBusy(true);
        try {
          if (onClick) await onClick();
          else await run({ type: action.command } as TaskCommand);
        } finally {
          setBusy(false);
        }
      }}
    >
      {action.label}
    </Button>
  );
}

/** 等谁处理，以及切到对应角色的入口。 */
export function Handoff({ handoff, onRole }: { handoff: RoleHandoff; onRole: (role: UiRole) => void }) {
  return (
    <div className="handoff">
      <span>{handoff.text}</span>
      {handoff.switchTo.map((r) => (
        <Button key={r} size="small" icon={<SwapOutlined />} onClick={() => onRole(r)}>
          切换为{ROLE_LABELS[r]}
        </Button>
      ))}
    </div>
  );
}

/** 当前角色能做的操作，加上其余操作在等谁。按钮全部来自快照，handlers 只替换点击后做什么。 */
export function TaskActionBar({
  actions,
  handlers,
  align,
}: {
  actions: CardActions;
  handlers?: Partial<Record<CommandType, () => Promise<void> | void>>;
  align?: "end";
}) {
  const plan = actionPlan(actions.taskActions, actions.waitingFor, actions.role);
  return (
    <>
      {plan.mine.length > 0 && (
        <div className="card-actions" style={align === "end" ? { justifyContent: "flex-end" } : undefined}>
          {plan.mine.map((a) => (
            <ActionButton key={a.command} action={a} run={actions.run} onClick={handlers?.[a.command]} />
          ))}
        </div>
      )}
      {plan.handoff && <Handoff handoff={plan.handoff} onRole={actions.onRole} />}
    </>
  );
}

export function PauseCard({ card, actions }: { card: Extract<ChatCard, { kind: "pause" }>; actions: CardActions }) {
  if (!card.active) {
    return (
      <div className="card muted">
        <ExclamationCircleFilled /> {STATE_LABELS[card.state]}：{card.pause.message}（已处理）
      </div>
    );
  }
  const pause: PauseView = actions.pause ?? card.pause;
  const operator = card.state === "awaiting_operator";
  return (
    <div className={`card ${operator ? "danger" : "warn"}`}>
      <div className="card-title" style={{ color: operator ? "var(--danger)" : "var(--warn)" }}>
        <ExclamationCircleFilled /> {pause.title ?? STATE_LABELS[card.state]}
      </div>
      <div className="card-body">{pause.message}</div>
      {pause.resolution && <div className="card-body">{pause.resolution}</div>}
      <TaskActionBar actions={actions} handlers={{ supplement: actions.openSupplement }} />
      {needsModelSettings(pause) && (
        <div className="card-actions">
          <Button icon={<SettingOutlined />} onClick={actions.openSettings}>
            打开模型设置
          </Button>
        </div>
      )}
    </div>
  );
}

export function QcCard({ card }: { card: Extract<ChatCard, { kind: "qc" }> }) {
  const bad = card.checks.filter((c) => !c.passed);
  return (
    <div className={`card ${card.passed ? "ok" : "danger"}`}>
      <div className="card-title" style={{ color: card.passed ? "var(--primary)" : "var(--danger)" }}>
        {card.passed ? <CheckCircleFilled /> : <CloseCircleFilled />}
        {card.passed ? "质检通过" : "质检未通过"}
      </div>
      <div className="card-body">
        {card.passed ? `经 ${card.checks.length} 项检查，未发现问题，符合发布要求。` : `第 ${card.attempt} 次质检，${bad.length} 项未通过。`}
      </div>
      {bad.map((c) => (
        <div key={c.id} className="card-body">
          <b>{c.label}：</b>
          {c.issues.join("；")}
        </div>
      ))}
      <div className="chips">
        {card.checks.map((c) => (
          <span key={c.id} className={`chip ${c.passed ? "" : "bad"}`}>
            {c.passed ? <CheckCircleFilled /> : <CloseCircleFilled />} {c.label}
          </span>
        ))}
      </div>
    </div>
  );
}

export function ReviewCard({ card, actions }: { card: Extract<ChatCard, { kind: "review" }>; actions: CardActions }) {
  const [picked, setPicked] = useState<string[]>([]);
  const [comment, setComment] = useState("");
  const docx = actions.downloadUrl("docx");
  const linkProps = useLinkProps();

  if (!card.active) {
    const d = card.decision;
    return (
      <div className="card muted">
        {d?.decision === "approve" && "审核人已通过"}
        {d?.decision === "return" && (
          <>
            审核人已退回：{d.comment}
            {d.sections?.map((s) => (
              <Tag key={s} style={{ marginLeft: 6 }}>
                {actions.sections.find((x) => x.id === s)?.heading ?? s}
              </Tag>
            ))}
          </>
        )}
        {!d && "审核已结束"}
      </div>
    );
  }
  // 普通待审核的说明就是这张卡本身；超范围意见要说清为什么回到这里
  const pause = actions.pause?.code === "awaiting_review" ? undefined : actions.pause;
  const canReturn = actions.taskActions.some((a) => a.command === "return" && a.roles.includes(actions.role));
  return (
    <div className="card">
      <div className="card-title">
        <FileWordOutlined style={{ color: "#2f6fed", fontSize: 28 }} />
        <span>
          请审核：经营周报.docx
          <div className="chat-sub" style={{ fontWeight: 400 }}>{clock(card.ts)}</div>
        </span>
        {docx && (
          <Button style={{ marginLeft: "auto" }} icon={<DownloadOutlined />} href={docx} {...linkProps(docx)}>
            下载
          </Button>
        )}
      </div>
      {pause && (
        <div className="card-body review-pause">
          <b>{pause.title}</b>
          <div>{pause.message}</div>
          {pause.resolution && <div>{pause.resolution}</div>}
        </div>
      )}
      {canReturn && (
        <div className="card-body">
          <div>退回涉及章节（可选）</div>
          <Checkbox.Group className="section-grid" options={actions.sections.map((s) => ({ label: s.heading, value: s.id }))} value={picked} onChange={(v) => setPicked(v as string[])} />
          <div>退回意见</div>
          <Input.TextArea
            rows={3}
            maxLength={500}
            showCount
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="写明需要修改的地方…"
            style={{ marginTop: 6 }}
          />
        </div>
      )}
      <TaskActionBar
        actions={actions}
        align="end"
        handlers={{
          return: async () => {
            if (!comment.trim()) {
              message.warning("请填写退回意见");
              return;
            }
            await actions.run({ type: "return", comment: comment.trim(), sections: picked.length ? picked : undefined });
          },
        }}
      />
    </div>
  );
}

export function DeliveredCard({ card, actions }: { card: Extract<ChatCard, { kind: "delivered" }>; actions: CardActions }) {
  const names: Record<string, string> = { docx: "周报 Word", md: "Markdown 报告", qc_record: "检验记录" };
  const linkProps = useLinkProps();
  return (
    <div className="card ok">
      <div className="card-title" style={{ color: "var(--primary)" }}>
        <CheckCircleFilled /> 已交付
      </div>
      <div className="card-actions">
        {card.deliverables.map((d) => (
          <Button key={d.kind} icon={<DownloadOutlined />} href={actions.downloadUrl(d.kind)} disabled={!actions.downloadUrl(d.kind)} {...linkProps(actions.downloadUrl(d.kind))}>
            {names[d.kind] ?? d.kind}
          </Button>
        ))}
      </div>
    </div>
  );
}

export function NoticeCard({ card }: { card: Extract<ChatCard, { kind: "notice" }> }) {
  return <div className={`card ${card.level === "warning" ? "warn" : "muted"}`}>{card.text}</div>;
}

export function renderCard(card: ChatCard, actions: CardActions) {
  switch (card.kind) {
    case "pause":
      return <PauseCard key={`${card.kind}-${card.seq}`} card={card} actions={actions} />;
    case "qc":
      return <QcCard key={`${card.kind}-${card.seq}`} card={card} />;
    case "review":
      return <ReviewCard key={`${card.kind}-${card.seq}`} card={card} actions={actions} />;
    case "delivered":
      return <DeliveredCard key={`${card.kind}-${card.seq}`} card={card} actions={actions} />;
    case "notice":
      return <NoticeCard key={`${card.kind}-${card.seq}`} card={card} />;
    default:
      return null;
  }
}

