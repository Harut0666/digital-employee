// 提问卡（ask_user_question）的界面派生与作答状态。纯函数，不依赖 React 与网络。
// 问题和答案都来自后端的会话接口；这里只管：卡片显示成什么、人选到哪一步、能不能提交、提交的答案长什么样。

import type { QuestionAnswer, QuestionItem, SessionCard, UiRole } from "../api/types";

/** 人在卡片上的作答草稿：每题选了哪些选项、“其他”里写了什么 */
export type QuestionDraft = Record<string, { selected: string[]; custom: string }>;

export type QuestionState = "pending" | "answered" | "cancelled";

export interface QuestionCardView {
  requestId: string;
  state: QuestionState;
  stateText: string;
  /** 现在能不能作答：还在等、是发起人、会话可写 */
  canAnswer: boolean;
  questions: QuestionItem[];
  /** 已答复时每题的答案（题目文字 + 选了什么） */
  answered: { id: string; header: string; question: string; text: string }[];
}

export function questionState(card: SessionCard): QuestionState {
  if (card.pending) return "pending";
  return card.decision === "answered" ? "answered" : "cancelled";
}

/** 一题的答案写成一句话：选中的选项，加上“其他”里写的 */
export function answerText(answer: QuestionAnswer | undefined): string {
  if (!answer) return "（未回答）";
  const parts = [...answer.selected];
  if (answer.custom?.trim()) parts.push(`其他：${answer.custom.trim()}`);
  return parts.length > 0 ? parts.join("、") : "（未回答）";
}

export function deriveQuestionCard(card: SessionCard, opts: { writable: boolean; role: UiRole }): QuestionCardView {
  const state = questionState(card);
  const questions = card.questions ?? [];
  const canAnswer = state === "pending" && opts.writable && opts.role === "initiator";
  const byId = new Map((card.answers ?? []).map((a) => [a.id, a]));
  return {
    requestId: card.requestId ?? "",
    state,
    stateText:
      state === "pending" ? (canAnswer ? "等你回答" : "等待发起人回答") : state === "answered" ? "已回答" : "已取消（这一轮被停止）",
    canAnswer,
    questions,
    answered: questions.map((q) => ({ id: q.id, header: q.header || q.question, question: q.question, text: answerText(byId.get(q.id)) })),
  };
}

export function emptyDraft(questions: readonly QuestionItem[]): QuestionDraft {
  return Object.fromEntries(questions.map((q) => [q.id, { selected: [], custom: "" }]));
}

/** 点一个选项：单选是换成它（再点一次取消），多选是切换 */
export function toggleOption(draft: QuestionDraft, question: QuestionItem, label: string): QuestionDraft {
  const current = draft[question.id] ?? { selected: [], custom: "" };
  let selected: string[];
  if (question.multiSelect) {
    selected = current.selected.includes(label) ? current.selected.filter((l) => l !== label) : [...current.selected, label];
  } else {
    selected = current.selected.includes(label) ? [] : [label];
  }
  return { ...draft, [question.id]: { ...current, selected } };
}

export function setCustom(draft: QuestionDraft, id: string, custom: string): QuestionDraft {
  const current = draft[id] ?? { selected: [], custom: "" };
  return { ...draft, [id]: { ...current, custom } };
}

/** 还没回答的问题（每题至少选一个选项，或在“其他”里写点什么） */
export function unanswered(questions: readonly QuestionItem[], draft: QuestionDraft): QuestionItem[] {
  return questions.filter((q) => {
    const d = draft[q.id];
    return !d || (d.selected.length === 0 && d.custom.trim() === "");
  });
}

export function canSubmit(questions: readonly QuestionItem[], draft: QuestionDraft): boolean {
  return questions.length > 0 && unanswered(questions, draft).length === 0;
}

/** 提交的答案：按问题顺序，选项按卡片上的顺序，“其他”去掉首尾空白，空的不带 */
export function buildAnswers(questions: readonly QuestionItem[], draft: QuestionDraft): QuestionAnswer[] {
  return questions.map((q) => {
    const d = draft[q.id] ?? { selected: [], custom: "" };
    const selected = q.options.map((o) => o.label).filter((label) => d.selected.includes(label));
    const custom = d.custom.trim();
    return custom ? { id: q.id, selected, custom } : { id: q.id, selected };
  });
}
