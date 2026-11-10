// 员工表单卡（T49）的界面派生。纯函数，不依赖 React 与网络。
// 卡片内容（员工、Agent 预填的字段、材料及来源、开工清单定义）来自后端；人改字段、补材料、确认跳过时，
// 这里按同一套规则（对应 internal/session/checklist.go 的 evaluateChecklist）实时判定：
// 标出哪些是 Agent 预填的、材料来自哪里、哪些清单项已备齐，以及“确认开工”为什么点不了。
// 最终仍以后端为准：必需项不齐后端拒绝开工。

import type { ChecklistDef, SessionAttachment, SessionCard, UiRole } from "../api/types";

/** 卡片所处的阶段：等人、已确认开工、发起人取消、被停止 */
export type EmployeeFormState = "pending" | "confirmed" | "rejected" | "cancelled";

/** 字段上的标记：Agent 预填的；人改过 Agent 预填的值 */
export type FieldMark = "prefilled" | "edited";

export interface MaterialRow {
  name: string;
  size?: number;
  /** 来源的说法：来自目录的哪个路径、会话里已有、公开样例、人补传的 */
  origin: string;
  /** Agent 给的（预填）；人补传的为 false */
  prefilled: boolean;
}

/** 清单一行：done 已备齐、skipped 人确认跳过、missing 还缺 */
export interface FormChecklistRow {
  id: string;
  label: string;
  required: boolean;
  status: "done" | "skipped" | "missing";
  /** 可跳过且还缺时，跳过按钮上的说法 */
  skipLabel?: string;
}

export interface EmployeeFormView {
  state: EmployeeFormState;
  stateText: string;
  /** 字段名 → 标记（没有标记的字段是空白或人自己填的） */
  fieldMarks: Record<string, FieldMark>;
  materials: MaterialRow[];
  /** 要带进任务的会话材料文件名：Agent 给的会话材料，加上人补传的 */
  attachments: string[];
  checklist: FormChecklistRow[];
  /** “确认开工”点不了的原因，每条一句话；为空才能确认 */
  blockers: string[];
  /** 当前角色能不能操作这张卡 */
  canAnswer: boolean;
  canConfirm: boolean;
}

export interface EmployeeFormInput {
  card: SessionCard;
  /** 人在卡片上现在的表单字段 */
  formData: Record<string, unknown>;
  /** 会话里现在的材料（含人在卡片上补传的） */
  sessionAttachments: readonly SessionAttachment[];
  /** 人在卡片上确认跳过的可跳过项 */
  skipped: ReadonlySet<string>;
  role: UiRole;
  writable: boolean;
}

const STATE_TEXT: Record<EmployeeFormState, string> = {
  pending: "等你核对并确认",
  confirmed: "已确认开工",
  rejected: "已取消",
  cancelled: "已取消",
};

export function employeeFormState(card: Pick<SessionCard, "pending" | "decision">): EmployeeFormState {
  if (card.pending) return "pending";
  if (card.decision === "confirmed") return "confirmed";
  if (card.decision === "rejected") return "rejected";
  return "cancelled";
}

const filled = (value: unknown): boolean => {
  if (value === undefined || value === null) return false;
  if (typeof value === "string") return value.trim() !== "";
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === "object") return Object.values(value).length > 0 && Object.values(value).every(filled);
  return true;
};

const extensionOf = (name: string) => (/\.[^.]+$/.exec(name)?.[0] ?? "").toLowerCase();

const sameValue = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

function originOf(card: SessionCard, name: string): string {
  const material = card.materials?.find((m) => m.name === name);
  switch (material?.source) {
    case "folder":
      return `来自目录：${material.path ?? name}`;
    case "sample":
      return "来自公开样例";
    case "attachment":
      return "会话里已有的材料";
    default:
      return "你补传的";
  }
}

/** 按材料文件名和表单字段判定开工清单；skipped 里的可跳过项在还缺时算人确认跳过 */
export function evaluateFormChecklist(
  items: readonly ChecklistDef[],
  form: Record<string, unknown>,
  materialNames: readonly string[],
  skipped: ReadonlySet<string>,
): FormChecklistRow[] {
  return items.map((item) => {
    const done =
      item.source === "field"
        ? filled(form[item.field ?? ""])
        : materialNames.some((name) => (item.accept ?? []).some((accepted) => accepted.toLowerCase() === extensionOf(name)));
    const status = done ? "done" : !item.required && skipped.has(item.id) ? "skipped" : "missing";
    return {
      id: item.id,
      label: item.label,
      required: item.required,
      status,
      ...(!item.required && status === "missing" ? { skipLabel: item.skipLabel ?? "跳过" } : {}),
    };
  });
}

export function deriveEmployeeForm(input: EmployeeFormInput): EmployeeFormView {
  const { card, formData, sessionAttachments, skipped, role, writable } = input;
  const state = employeeFormState(card);

  const fieldMarks: Record<string, FieldMark> = {};
  for (const name of card.prefilled ?? []) {
    fieldMarks[name] = sameValue(formData[name], card.draft?.[name]) ? "prefilled" : "edited";
  }

  // 材料：Agent 给的（按来源标注），再加人在卡片上补传的（弹卡之后新出现的会话材料）
  const given = card.materials ?? [];
  const known = new Set([...given.map((m) => m.name), ...(card.baseline ?? [])]);
  const added = sessionAttachments.filter((a) => !known.has(a.name));
  const sizeOf = (name: string) => sessionAttachments.find((a) => a.name === name)?.size;
  const materials: MaterialRow[] = [
    ...given.map((m): MaterialRow => ({ name: m.name, size: m.size ?? sizeOf(m.name), origin: originOf(card, m.name), prefilled: true })),
    ...added.map((a): MaterialRow => ({ name: a.name, size: a.size, origin: "你补传的", prefilled: false })),
  ];
  const attachments = [...given.filter((m) => m.source === "attachment").map((m) => m.name), ...added.map((a) => a.name)];

  const checklist = evaluateFormChecklist(
    card.checklist ?? [],
    formData,
    materials.map((m) => m.name),
    skipped,
  );

  const blockers: string[] = [];
  if (state !== "pending") blockers.push(STATE_TEXT[state]);
  const missing = checklist.filter((row) => row.required && row.status === "missing").map((row) => row.label);
  if (missing.length > 0) blockers.push(`开工清单还缺：${missing.join("、")}`);

  const canAnswer = state === "pending" && writable && role === "initiator";
  return {
    state,
    stateText: STATE_TEXT[state],
    fieldMarks,
    materials,
    attachments,
    checklist,
    blockers,
    canAnswer,
    canConfirm: canAnswer && blockers.length === 0,
  };
}
