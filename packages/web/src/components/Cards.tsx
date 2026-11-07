import { useState } from "react";
import { Button, Checkbox, Input, message } from "antd";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  DownloadOutlined,
  ExclamationCircleFilled,
  EyeOutlined,
  FileWordOutlined,
  ReloadOutlined,
  RollbackOutlined,
  SettingOutlined,
  SwapOutlined,
  TeamOutlined,
} from "@ant-design/icons";
import type { TaskCommand } from "@de/core/contract";
import { actionPlan, needsModelSettings, ROLE_LABELS, type RoleHandoff } from "../view/permissions";
import type { ChatItem } from "../view/chat-view";
import type { CommandType, EmployeeInfo, PauseView, SessionResult, TaskAction, UiRole } from "../api";
import { useLinkProps } from "./Preferences";
import { FormCard } from "./FormCard";

export interface CardActions {
  role: UiRole;
  /** 可用操作和在等的角色：任务卡片取卡片上的，草稿面板取快照的 */
  taskActions: TaskAction[];
  waitingFor: UiRole[];
  /** 暂停原因（带 title、resolution） */
  pause: PauseView | undefined;
  /** 发任务命令；resolve 后才恢复按钮 */
  run: (cmd: TaskCommand) => Promise<void>;
  onRole: (role: UiRole) => void;
  openSettings: () => void;
  /** 聚焦右栏的补充表单 */
  openSupplement: () => void;
  /** 打开右栏（窄窗口时是抽屉） */
  openPanel: () => void;
  /** 可勾选的退回章节（取自员工配置 qc.yaml）；退回命令里带的是 id，界面显示 heading */
  sections: { id: string; heading: string }[];
  downloadUrl: (kind: "docx" | "md" | "qc_record", version?: number) => string | undefined;
}

/** 会话层的操作：确认员工、上传材料、提交表单。都只有发起人能做，后端仍会校验。 */
export interface SessionActions {
  employees: EmployeeInfo[];
  confirmEmployee: (employee: string) => Promise<void>;
  upload: (files: File[]) => Promise<void>;
  submit: (form: Record<string, unknown>) => Promise<SessionResult>;
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

/** 当前角色能做的操作，加上其余操作在等谁。按钮全部来自后端，handlers 只替换点击后做什么。 */
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

function EmployeeConfirmCard({ item, actions, session }: { item: Extract<ChatItem, { kind: "employee_confirm" }>; actions: CardActions; session: SessionActions }) {
  const [busy, setBusy] = useState<string>();
  const nameOf = (id: string) => item.candidates.find((c) => c.id === id)?.name ?? id;
  const confirm = async (id: string) => {
    setBusy(id);
    try {
      await session.confirmEmployee(id);
    } finally {
      setBusy(undefined);
    }
  };
  if (!item.active) {
    return (
      <div className="card muted">
        <TeamOutlined /> {item.proposed ? `提议由${nameOf(item.proposed)}接手` : "请选择员工"}（已处理）
      </div>
    );
  }
  const initiator = actions.role === "initiator";
  const others = item.candidates.filter((c) => c.id !== item.proposed);
  const intro =
    item.source === "default" ? "按设置里的默认员工，由下面的员工接手：" : item.source === "model" ? "建议由下面的员工接手：" : "我判断不出该由谁接手，请选择一位员工：";
  return (
    <div className="card">
      <div className="card-title">
        <TeamOutlined style={{ color: "var(--primary)" }} /> 确认由谁接手
      </div>
      <div className="card-body">{intro}</div>
      {item.proposed && (
        <div className="card-body">
          <b>{nameOf(item.proposed)}</b>
          {item.reason && <span className="chat-sub">　{item.reason}</span>}
        </div>
      )}
      {initiator ? (
        <div className="card-actions">
          {item.proposed && (
            <Button type="primary" loading={busy === item.proposed} disabled={busy !== undefined} onClick={() => confirm(item.proposed!)}>
              确认由{nameOf(item.proposed)}接手
            </Button>
          )}
          {others.map((c) => (
            <Button key={c.id} loading={busy === c.id} disabled={busy !== undefined} title={c.description} onClick={() => confirm(c.id)}>
              {item.proposed ? `改由${c.name}` : c.name}
            </Button>
          ))}
        </div>
      ) : (
        <Handoff handoff={{ text: "等待发起人确认员工", switchTo: ["initiator"] }} onRole={actions.onRole} />
      )}
    </div>
  );
}

export function PauseCard({ item, actions }: { item: Extract<ChatItem, { kind: "pause" }>; actions: CardActions }) {
  const pause = item.pause;
  const title = pause?.title ?? "任务已暂停";
  if (!item.active) {
    return (
      <div className="card muted">
        <ExclamationCircleFilled /> {title}：{pause?.message}（已处理）
      </div>
    );
  }
  const scoped: CardActions = { ...actions, taskActions: item.actions, pause };
  // 发起人处理不了的暂停（只有操作员能动）用红色
  const operator = !item.actions.some((a) => a.roles.includes("initiator")) && actions.waitingFor.includes("operator");
  return (
    <div className={`card ${operator ? "danger" : "warn"}`}>
      <div className="card-title" style={{ color: operator ? "var(--danger)" : "var(--warn)" }}>
        <ExclamationCircleFilled /> {title}
      </div>
      <div className="card-body">{pause?.message}</div>
      {pause?.resolution && <div className="card-body">{pause.resolution}</div>}
      <TaskActionBar actions={scoped} handlers={{ supplement: actions.openSupplement }} />
      {pause && needsModelSettings(pause) && (
        <div className="card-actions">
          <Button icon={<SettingOutlined />} onClick={actions.openSettings}>
            打开模型设置
          </Button>
        </div>
      )}
    </div>
  );
}

export function QcCard({ item }: { item: Extract<ChatItem, { kind: "qc" }> }) {
  const bad = item.checks.filter((c) => !c.passed);
  return (
    <div className={`card ${item.passed ? "ok" : "danger"}`}>
      <div className="card-title" style={{ color: item.passed ? "var(--primary)" : "var(--danger)" }}>
        {item.passed ? <CheckCircleFilled /> : <CloseCircleFilled />}
        {item.passed ? "质检通过" : "质检未通过"}
      </div>
      <div className="card-body">{item.passed ? `经 ${item.checks.length} 项检查，未发现问题，符合发布要求。` : `${bad.length} 项未通过。`}</div>
      {bad.map((c) => (
        <div key={c.id} className="card-body">
          <b>{c.label}：</b>
          {c.issues.join("；")}
        </div>
      ))}
      <div className="chips">
        {item.checks.map((c) => (
          <span key={c.id} className={`chip ${c.passed ? "" : "bad"}`}>
            {c.passed ? <CheckCircleFilled /> : <CloseCircleFilled />} {c.label}
          </span>
        ))}
      </div>
    </div>
  );
}

/** 送审提示：审核在右栏进行，对话里只说一句；等别的角色时给切换入口。 */
export function ReviewNoticeCard({ item, actions }: { item: Extract<ChatItem, { kind: "review_notice" }>; actions: CardActions }) {
  if (!item.active) return <div className="notice">交付物已送审（已处理）</div>;
  const pause = item.pause;
  const outOfScope = pause !== undefined && pause.code !== "awaiting_review";
  const reviewer = actions.waitingFor.includes(actions.role);
  return (
    <div className={`card ${outOfScope ? "warn" : ""}`}>
      <div className="card-title" style={outOfScope ? { color: "var(--warn)" } : undefined}>
        {outOfScope ? <ExclamationCircleFilled /> : <FileWordOutlined style={{ color: "var(--info)" }} />}
        {outOfScope ? pause.title : "交付物写好了，请在右侧审核"}
      </div>
      {outOfScope && <div className="card-body">{pause.message}</div>}
      {outOfScope && pause.resolution && <div className="card-body">{pause.resolution}</div>}
      <div className="card-actions">
        <Button size="small" icon={<EyeOutlined />} onClick={actions.openPanel}>
          {reviewer ? "打开审核区" : "查看交付物"}
        </Button>
      </div>
      {!reviewer && actions.waitingFor.length > 0 && (
        <Handoff handoff={{ text: `等待${actions.waitingFor.map((r) => ROLE_LABELS[r]).join("、")}审核`, switchTo: actions.waitingFor }} onRole={actions.onRole} />
      )}
    </div>
  );
}

/** 对话里写的退回意见：Agent 起草，审核人检查后在卡片上确认，才真正退回。 */
export function ReturnConfirmCard({ item, actions }: { item: Extract<ChatItem, { kind: "return_confirm" }>; actions: CardActions }) {
  const [picked, setPicked] = useState<string[]>(item.sections ?? []);
  const [comment, setComment] = useState(item.comment);

  if (!item.active) return <div className="card muted">退回意见草稿：{item.comment}（已处理）</div>;

  const scoped: CardActions = { ...actions, taskActions: item.actions };
  const mine = item.actions.some((a) => a.roles.includes(actions.role));
  return (
    <div className="card">
      <div className="card-title">
        <RollbackOutlined style={{ color: "var(--warn)" }} /> 确认退回意见
        <span className="chat-sub" style={{ fontWeight: 400, marginLeft: "auto" }}>{clock(item.ts)}</span>
      </div>
      {mine ? (
        <ReturnForm sections={actions.sections} picked={picked} onPick={setPicked} comment={comment} onComment={setComment} />
      ) : (
        <div className="card-body">{item.comment}</div>
      )}
      <TaskActionBar
        actions={scoped}
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

/** 退回意见的编辑区：涉及章节（可选）和意见正文。右栏的退回对话框和对话里的确认卡片共用。 */
export function ReturnForm({
  sections,
  picked,
  onPick,
  comment,
  onComment,
}: {
  sections: { id: string; heading: string }[];
  picked: string[];
  onPick: (ids: string[]) => void;
  comment: string;
  onComment: (text: string) => void;
}) {
  return (
    <div className="card-body">
      <div>退回涉及章节（可选）</div>
      <Checkbox.Group className="section-grid" options={sections.map((s) => ({ label: s.heading, value: s.id }))} value={picked} onChange={(v) => onPick(v as string[])} />
      <div>退回意见</div>
      <Input.TextArea
        rows={3}
        maxLength={500}
        showCount
        value={comment}
        onChange={(e) => onComment(e.target.value)}
        placeholder="写明需要修改的地方…"
        style={{ marginTop: 6 }}
      />
    </div>
  );
}
export function DeliveredCard({ item, actions }: { item: Extract<ChatItem, { kind: "delivered" }>; actions: CardActions }) {
  const names: Record<string, string> = { docx: "Word 文档", md: "Markdown 报告", qc_record: "检验记录" };
  const linkProps = useLinkProps();
  return (
    <div className="card ok">
      <div className="card-title" style={{ color: "var(--primary)" }}>
        <CheckCircleFilled /> 已交付
      </div>
      <div className="card-actions">
        {item.deliverables.map((d) => (
          <Button key={d.kind} icon={<DownloadOutlined />} href={actions.downloadUrl(d.kind)} disabled={!actions.downloadUrl(d.kind)} {...linkProps(actions.downloadUrl(d.kind))}>
            {names[d.kind] ?? d.kind}
          </Button>
        ))}
      </div>
    </div>
  );
}

export function renderCard(item: Exclude<ChatItem, { kind: "user" | "agent" }>, actions: CardActions, session: SessionActions) {
  switch (item.kind) {
    case "notice":
      return (
        <div key={item.key} className="notice">
          {item.text}
        </div>
      );
    case "employee_confirm":
      return <EmployeeConfirmCard key={item.key} item={item} actions={actions} session={session} />;
    case "form":
      return <FormCard key={`${item.key}-${JSON.stringify(item.draft)}`} item={item} actions={actions} session={session} />;
    case "pause":
      return <PauseCard key={item.key} item={item} actions={actions} />;
    case "qc":
      return <QcCard key={item.key} item={item} />;
    case "review_notice":
      return <ReviewNoticeCard key={item.key} item={item} actions={actions} />;
    case "return_confirm":
      return <ReturnConfirmCard key={item.key} item={item} actions={actions} />;
    case "delivered":
      return <DeliveredCard key={item.key} item={item} actions={actions} />;
  }
}
