import { Prompts, Sender, Welcome } from "@ant-design/x";
import { Tag, message } from "antd";
import {
  BookOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  MessageOutlined,
  RobotOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { useEffect, useMemo, useRef } from "react";
import { STATE_LABELS, type TaskEvent, type TaskState } from "@de/core/contract";
import type { EmployeeInfo } from "../api";
import type { ChatCard, SessionView, StepDef } from "../view/session-view";
import { deriveTrajectory } from "../view/trajectory";
import { renderCard, type CardActions } from "./Cards";
import { Trajectory } from "./Trajectory";

const STATE_COLOR: Record<TaskState, string> = {
  draft: "default",
  admission: "processing",
  awaiting_initiator: "orange",
  running: "processing",
  qc: "processing",
  awaiting_operator: "red",
  awaiting_review: "blue",
  delivered: "green",
  terminated: "default",
};

export function StateTag({ state }: { state: TaskState }) {
  return (
    <Tag color={STATE_COLOR[state]} variant="filled" style={{ margin: 0, fontSize: 13 }}>
      {STATE_LABELS[state]}
    </Tag>
  );
}

const clock = (ts: string) => new Date(ts).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });

function Message({ card }: { card: Extract<ChatCard, { kind: "user" | "agent" }> }) {
  const me = card.kind === "user";
  return (
    <div className={`msg-row ${me ? "me" : ""}`}>
      <div className="msg-avatar">{me ? <UserOutlined /> : <RobotOutlined />}</div>
      <div className="msg-col">
        <div className="bubble">{card.text}</div>
        <div className="msg-time">{clock(card.ts)}</div>
      </div>
    </div>
  );
}

function ChatInput({ placeholder }: { placeholder: string }) {
  return (
    <div className="chat-foot">
      <Sender
        placeholder={placeholder}
        onSubmit={() => message.info("前台 Agent 暂未接入：请使用右栏表单和卡片按钮推进任务")}
      />
    </div>
  );
}

export function TaskChat({
  sessionId,
  title,
  view,
  subtitle,
  actions,
  events,
  stepDefs,
  elapsedMs,
  modelCalls,
  emptyHint,
}: {
  sessionId: string;
  title: string;
  view: SessionView;
  subtitle?: string;
  actions: CardActions;
  events: TaskEvent[];
  stepDefs: StepDef[];
  elapsedMs: number;
  modelCalls: { used: number; limit: number };
  /** 没有任何对话卡片时显示的一句提示（例如草稿任务） */
  emptyHint?: string;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const trajectory = useMemo(() => deriveTrajectory(events, stepDefs), [events, stepDefs]);
  useEffect(() => {
    bodyRef.current?.scrollTo({ top: bodyRef.current.scrollHeight, behavior: "smooth" });
  }, [view.lastSeq]);
  return (
    <>
      <div className="chat-head">
        <div>
          <div style={{ display: "flex", gap: 12, alignItems: "center" }}>
            <h2>{title}</h2>
            {view.state && <StateTag state={view.state} />}
          </div>
          {subtitle && <div className="chat-sub">{subtitle}</div>}
        </div>
      </div>
      <Trajectory key={sessionId} view={trajectory} elapsedMs={elapsedMs} modelCalls={modelCalls} />
      <div className="chat-body" ref={bodyRef}>
        {view.cards.length === 0 && emptyHint && (
          <div className="welcome" style={{ paddingTop: 48 }}>
            <p style={{ fontSize: 16, color: "var(--muted)" }}>{emptyHint}</p>
          </div>
        )}
        {view.cards.map((card) =>
          card.kind === "user" || card.kind === "agent" ? <Message key={`${card.kind}-${card.seq}`} card={card} /> : renderCard(card, actions),
        )}
      </div>
      <ChatInput placeholder="问问这个任务…" />
    </>
  );
}

export function NewConversation({ employee, onPrompt }: { employee: EmployeeInfo | undefined; onPrompt: (key: string) => void }) {
  const isWeekly = employee?.id === "weekly-report";
  const prompts = isWeekly
    ? [
        { key: "weekly", icon: <FileTextOutlined />, label: "出第 44 周周报", description: "基于本周销售数据和周会纪要，生成标准经营周报", tint: "#e0f3ed", color: "#1f8a70" },
        { key: "sample", icon: <FolderOpenOutlined />, label: "载入公开样例", description: "用第 44 周的样例数据建一份草稿任务，提交即可看到完整流程", tint: "#e3ebff", color: "#2f6fed" },
        { key: "about", icon: <MessageOutlined />, label: "周报包含哪些内容", description: "了解经营周报的标准章节、核心指标和数据要求", tint: "#efe6fb", color: "#7a4fd1" },
      ]
    : [
        { key: "review", icon: <BookOutlined />, label: "研判一组技术文献", description: "上传文献清单，提炼技术路线与关键信息", tint: "#e3ebff", color: "#2f6fed" },
        { key: "sample", icon: <FolderOpenOutlined />, label: "载入公开样例", description: "查看示例研判报告的结构和内容", tint: "#e0f3ed", color: "#1f8a70" },
        { key: "about", icon: <MessageOutlined />, label: "研判报告包含哪些内容", description: "了解技术路线、来源清单等章节要求", tint: "#efe6fb", color: "#7a4fd1" },
      ];
  return (
    <>
      <div className="chat-body">
        <div className="welcome">
          <Welcome
            variant="borderless"
            icon={<div className="welcome-icon">{isWeekly || !employee ? <RobotOutlined /> : <BookOutlined />}</div>}
            title={`你好，我是${employee?.name ?? "数字员工"}`}
            description={isWeekly ? "上传销售明细和周会纪要，我来生成经营周报" : "上传技术文献，我来研判并提炼关键信息"}
          />
          <Prompts
            wrap
            style={{ marginTop: 24 }}
            items={prompts.map((p) => ({
              key: p.key,
              icon: <span style={{ color: p.color }}>{p.icon}</span>,
              label: p.label,
              description: p.description,
            }))}
            onItemClick={({ data }) => onPrompt(data.key)}
          />
        </div>
      </div>      <ChatInput placeholder={isWeekly ? "描述你要做的周报…" : "描述你要研判的内容…"} />
    </>
  );
}




