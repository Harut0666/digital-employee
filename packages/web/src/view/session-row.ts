// 侧栏会话行与状态语义色。纯函数。
//
// 状态点和任务状态胶囊用同一套语义：运行中 / 等你处理 / 已完成 / 出错 / 空闲，
// 颜色在样式里按这五个语义取（浅色深色各自保证对比度），组件不再各挑颜色。

import type { TaskState } from "@de/core/contract";
import type { SessionSummary, StatusDot } from "../api/types";
import { unreadOf } from "./sidebar-tree";

export type Tone = "running" | "waiting" | "done" | "error" | "idle";

const DOT_TONE: Record<string, Tone> = { waiting: "waiting", running: "running", done: "done", idle: "idle" };

/** 后端状态点 → 语义色 */
export function sessionTone(dot: StatusDot | string | undefined): Tone {
  return DOT_TONE[dot ?? "idle"] ?? "idle";
}

/** 任务状态 → 语义色：和侧栏的状态点同一种说法。awaiting_operator（工具故障等）是需要操作员介入的出错。 */
export function stateTone(state: TaskState | undefined): Tone {
  switch (state) {
    case "admission":
    case "running":
    case "qc":
      return "running";
    case "awaiting_initiator":
    case "awaiting_review":
      return "waiting";
    case "awaiting_operator":
      return "error";
    case "delivered":
      return "done";
    default:
      return "idle";
  }
}

/** 侧栏行右侧的相对时间：刚刚 / N 分钟 / N 小时 / 昨天 / N 天 / 今年 M月D日 / 往年 YYYY/M/D（短，不挤标题）。 */
export function sessionTime(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  if (!iso || Number.isNaN(at.getTime())) return "";
  const elapsed = now.getTime() - at.getTime();
  // 一分钟内（含时钟略有偏差、时间在未来的情况）
  if (elapsed < 60_000) return "刚刚";
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((day(now) - day(at)) / 86_400_000);
  if (days === 0) {
    const minutes = Math.floor(elapsed / 60_000);
    return minutes < 60 ? `${minutes} 分钟` : `${Math.floor(minutes / 60)} 小时`;
  }
  if (days === 1) return "昨天";
  if (days < 7) return `${days} 天`;
  if (at.getFullYear() === now.getFullYear()) return `${at.getMonth() + 1}月${at.getDate()}日`;
  return `${at.getFullYear()}/${at.getMonth() + 1}/${at.getDate()}`;
}

export interface SessionMenuItem {
  key: "rename" | "archive";
  label: string;
}

export interface SessionRowView {
  tone: Tone;
  /** “已完成未读”的小圆点 */
  unread: boolean;
  /** 由旧任务推导的只读会话：行上标“只读” */
  readOnly: boolean;
  canRename: boolean;
  /** 行右侧“更多”菜单：只读会话不能重命名，但能归档 / 取消归档 */
  menu: SessionMenuItem[];
}

export function sessionRowView(session: Pick<SessionSummary, "statusDot" | "unread" | "archived" | "readOnly">): SessionRowView {
  const unread = unreadOf(session);
  const readOnly = session.readOnly === true;
  const archive: SessionMenuItem = { key: "archive", label: session.archived ? "取消归档" : "归档" };
  return {
    // 归档的灰掉；完成但已读的回到空闲
    tone: session.archived ? "idle" : session.statusDot === "done" && !unread ? "idle" : sessionTone(session.statusDot),
    unread,
    readOnly,
    canRename: !readOnly,
    menu: readOnly ? [archive] : [{ key: "rename", label: "重命名" }, archive],
  };
}
