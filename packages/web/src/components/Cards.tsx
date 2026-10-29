import { useState } from "react";
import { Button, Checkbox, Input, message, Space, Tag, Tooltip } from "antd";
import { ThoughtChain } from "@ant-design/x";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  DownloadOutlined,
  ExclamationCircleFilled,
  FileWordOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import type { TaskCommand, TaskState } from "@de/core/contract";
import { availableCommands, ROLE_LABELS, type CommandType } from "../view/permissions";
import type { ChatCard } from "../view/session-view";
import type { UiRole } from "../api";

export interface CardActions {
  role: UiRole;
  state: TaskState | undefined;
  /** 发命令；resolve 后才恢复按钮 */
  run: (cmd: TaskCommand) => Promise<void>;
  /** 聚焦右栏的补充表单 */
  openSupplement: () => void;
  /** 可勾选的退回章节（取自员工配置 qc.yaml）；退回命令里带的是 id，界面显示 heading */
  sections: { id: string; heading: string }[];
  downloadUrl: (kind: "docx" | "md" | "qc_record") => string | undefined;
}

const STEP_KIND_TAG = {
  compute: { text: "计算", color: "default" },
  judge: { text: "判断", color: "purple" },
  qc: { text: "质检", color: "cyan" },
} as const;

const clock = (ts: string) => new Date(ts).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });

export function ProgressCard({ card }: { card: Extract<ChatCard, { kind: "progress" }> }) {
  const items = card.steps.map((s) => {
    const tag = STEP_KIND_TAG[s.kind];
    return {
      key: s.id,
      status: s.status === "done" ? ("success" as const) : s.status === "running" ? ("loading" as const) : s.status === "failed" ? ("error" as const) : s.status === "skipped" ? ("abort" as const) : undefined,
      icon: s.status === "pending" ? <span className="step-pending" /> : undefined,
      title: (
        <span className="step-row">
          <span className="name">{s.label}</span>
          <Tag color={tag.color} variant="filled" style={{ margin: 0 }}>
            {tag.text}
          </Tag>
          {s.status === "failed" && <span className="err">{s.error ?? "执行失败"}</span>}
        </span>
      ),
    };
  });
  return (
    <div className="card">
      {card.round > 0 && <div className="msg-time" style={{ textAlign: "left", marginBottom: 6 }}>第 {card.round} 轮重做</div>}
      <ThoughtChain items={items} line="solid" />
    </div>
  );
}

const CMD_LABEL: Record<CommandType, string> = {
  submit: "提交",
  supplement: "补充材料",
  continue_without_minutes: "无纪要继续",
  continue_with_partial: "按现有资料继续",
  retry: "重试",
  terminate: "终止",
  approve: "通过",
  return: "退回修改",
};

/** 命令按钮：角色不对或状态不对时禁用，并说明原因。 */
export function CommandButton({
  cmd,
  actions,
  options,
  primary,
  danger,
  onClick,
}: {
  cmd: CommandType;
  actions: CardActions;
  options?: TaskCommand["type"][];
  primary?: boolean;
  danger?: boolean;
  onClick?: () => Promise<void> | void;
}) {
  const [busy, setBusy] = useState(false);
  const allowed = actions.state ? availableCommands(actions.state, actions.role, options ? { options } : undefined).includes(cmd) : false;
  const btn = (
    <Button
      type={primary ? "primary" : "default"}
      danger={danger}
      disabled={!allowed}
      loading={busy}
      icon={cmd === "retry" ? <ReloadOutlined /> : undefined}
      onClick={async () => {
        setBusy(true);
        try {
          if (onClick) await onClick();
          else await actions.run({ type: cmd } as TaskCommand);
        } finally {
          setBusy(false);
        }
      }}
    >
      {CMD_LABEL[cmd]}
    </Button>
  );
  return allowed ? btn : <Tooltip title={`当前角色（${ROLE_LABELS[actions.role]}）不能执行此操作`}>{btn}</Tooltip>;
}

export function PauseCard({ card, actions }: { card: Extract<ChatCard, { kind: "pause" }>; actions: CardActions }) {
  const { pause } = card;
  const operator = card.state === "awaiting_operator";
  const title = operator ? (pause.code === "tool_failed" ? "工具调用失败" : "任务已暂停，需要人工处理") : "需要发起人补充";
  if (!card.active) {
    return (
      <div className="card muted">
        <ExclamationCircleFilled /> {title}：{pause.message}（已处理）
      </div>
    );
  }
  return (
    <div className={`card ${operator ? "danger" : "warn"}`}>
      <div className="pause-row">
        <div>
          <div className="card-title" style={{ color: operator ? "var(--danger)" : "var(--warn)" }}>
            <ExclamationCircleFilled /> {title}
          </div>
          <div className="card-body">{pause.message}</div>
        </div>
        {operator && (
          <Space>
            <CommandButton cmd="retry" actions={actions} options={pause.options} primary />
            <CommandButton cmd="terminate" actions={actions} options={pause.options} />
          </Space>
        )}
      </div>
      {!operator && (
        <div className="card-actions">
          {pause.options.includes("supplement") && (
            <CommandButton cmd="supplement" actions={actions} options={pause.options} primary onClick={actions.openSupplement} />
          )}
          {pause.options.includes("continue_without_minutes") && <CommandButton cmd="continue_without_minutes" actions={actions} options={pause.options} />}
          {pause.options.includes("continue_with_partial") && <CommandButton cmd="continue_with_partial" actions={actions} options={pause.options} />}
          {pause.options.includes("terminate") && <CommandButton cmd="terminate" actions={actions} options={pause.options} />}
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
  return (
    <div className="card">
      <div className="card-title">
        <FileWordOutlined style={{ color: "#2f6fed", fontSize: 28 }} />
        <span>
          请审核：经营周报.docx
          <div className="chat-sub" style={{ fontWeight: 400 }}>{clock(card.ts)}</div>
        </span>
        {docx && (
          <Button style={{ marginLeft: "auto" }} icon={<DownloadOutlined />} href={docx}>
            下载
          </Button>
        )}
      </div>
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
      <div className="card-actions" style={{ justifyContent: "flex-end" }}>
        <CommandButton
          cmd="return"
          actions={actions}
          onClick={async () => {
            if (!comment.trim()) {
              message.warning("请填写退回意见");
              return;
            }
            await actions.run({ type: "return", comment: comment.trim(), sections: picked.length ? picked : undefined });
          }}
        />
        <CommandButton cmd="approve" actions={actions} primary />
      </div>
    </div>
  );
}

export function DeliveredCard({ card, actions }: { card: Extract<ChatCard, { kind: "delivered" }>; actions: CardActions }) {
  const names: Record<string, string> = { docx: "周报 Word", md: "Markdown 报告", qc_record: "检验记录" };
  return (
    <div className="card ok">
      <div className="card-title" style={{ color: "var(--primary)" }}>
        <CheckCircleFilled /> 已交付
      </div>
      <div className="card-actions">
        {card.deliverables.map((d) => (
          <Button key={d.kind} icon={<DownloadOutlined />} href={actions.downloadUrl(d.kind)} disabled={!actions.downloadUrl(d.kind)}>
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
    case "progress":
      return <ProgressCard key={`${card.kind}-${card.seq}`} card={card} />;
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

