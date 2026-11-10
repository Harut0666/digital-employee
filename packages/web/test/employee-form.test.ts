import { describe, expect, test } from "bun:test";
import type { ChecklistDef, SessionCard } from "../src/api/types";
import { deriveEmployeeForm, employeeFormState, evaluateFormChecklist } from "../src/view/employee-form";

const CHECKLIST: ChecklistDef[] = [
  { id: "period", label: "统计周期", required: true, source: "field", field: "period" },
  { id: "sales", label: "销售明细", required: true, source: "upload", accept: [".xlsx", ".csv"] },
  { id: "minutes", label: "本周纪要", required: false, source: "material", accept: [".docx"], skipField: "allowMissingMinutes", skipLabel: "无纪要继续" },
];

const card = (patch: Partial<SessionCard> = {}): SessionCard => ({
  type: "employee_form",
  requestId: "r1",
  employee: "weekly-report",
  employeeName: "经营周报员工",
  pending: true,
  draft: { period: { year: 2026, week: 44 } },
  prefilled: ["period"],
  materials: [
    { name: "销售明细.xlsx", size: 100, source: "folder", path: "资料/销售明细.xlsx" },
    { name: "旧材料.csv", source: "attachment" },
  ],
  baseline: ["旧材料.csv", "上一次的.docx"],
  checklist: CHECKLIST,
  ...patch,
});

const view = (c: SessionCard, extra: Partial<Parameters<typeof deriveEmployeeForm>[0]> = {}) =>
  deriveEmployeeForm({
    card: c,
    formData: c.draft ?? {},
    sessionAttachments: [{ name: "旧材料.csv", size: 5 }, { name: "上一次的.docx", size: 9 }],
    skipped: new Set(),
    role: "initiator",
    writable: true,
    ...extra,
  });

describe("员工表单卡的状态", () => {
  test("等人、已确认、取消、被停止", () => {
    expect(employeeFormState({ pending: true })).toBe("pending");
    expect(employeeFormState({ decision: "confirmed" })).toBe("confirmed");
    expect(employeeFormState({ decision: "rejected" })).toBe("rejected");
    expect(employeeFormState({ decision: "cancelled" })).toBe("cancelled");
    expect(employeeFormState({})).toBe("cancelled");
  });
});

describe("预填标记", () => {
  test("Agent 预填的字段标 prefilled，人改了值就标 edited", () => {
    const c = card();
    expect(view(c).fieldMarks).toEqual({ period: "prefilled" });
    expect(view(c, { formData: { period: { year: 2026, week: 43 } } }).fieldMarks).toEqual({ period: "edited" });
  });

  test("人自己填的字段没有标记", () => {
    expect(view(card(), { formData: { period: { year: 2026, week: 44 }, remark: "先写华东" } }).fieldMarks).toEqual({ period: "prefilled" });
  });
});

describe("材料来源", () => {
  test("Agent 给的材料标出来源；弹卡之后新出现的会话材料是人补传的", () => {
    const v = view(card(), {
      sessionAttachments: [{ name: "旧材料.csv", size: 5 }, { name: "上一次的.docx", size: 9 }, { name: "周会纪要_2026W44.docx", size: 20 }],
    });
    expect(v.materials.map((m) => [m.name, m.origin, m.prefilled])).toEqual([
      ["销售明细.xlsx", "来自目录：资料/销售明细.xlsx", true],
      ["旧材料.csv", "会话里已有的材料", true],
      ["周会纪要_2026W44.docx", "你补传的", false],
    ]);
  });

  test("弹卡时已有、Agent 没点名的会话材料不混进来（上一次任务的材料）", () => {
    const v = view(card());
    expect(v.materials.map((m) => m.name)).toEqual(["销售明细.xlsx", "旧材料.csv"]);
    // 提交给后端的会话材料：Agent 点名的会话材料加人补传的；目录文件由后端在确认时复制
    expect(v.attachments).toEqual(["旧材料.csv"]);
  });

  test("公开样例的材料来自公开样例", () => {
    const v = view(card({ standalone: true, baseline: [], materials: [{ name: "销售明细.xlsx", source: "sample" }] }), { sessionAttachments: [] });
    expect(v.materials[0]).toMatchObject({ origin: "来自公开样例", prefilled: true });
  });
});

describe("不能确认的原因", () => {
  test("必需项没备齐：写明缺什么，不能确认", () => {
    const c = card({ draft: {}, prefilled: [], materials: [] });
    const v = view(c);
    expect(v.canConfirm).toBe(false);
    expect(v.blockers).toEqual(["开工清单还缺：统计周期、销售明细"]);
  });

  test("补传了材料、填了字段就能确认；可跳过项还缺不拦着", () => {
    const v = view(card());
    expect(v.blockers).toEqual([]);
    expect(v.canConfirm).toBe(true);
    expect(v.checklist.map((r) => `${r.id}=${r.status}`)).toEqual(["period=done", "sales=done", "minutes=missing"]);
    expect(v.checklist[2]?.skipLabel).toBe("无纪要继续");
  });

  test("可跳过项由人确认跳过，必需项不能跳过", () => {
    expect(view(card(), { skipped: new Set(["minutes"]) }).checklist[2]?.status).toBe("skipped");
    const required = evaluateFormChecklist(CHECKLIST, {}, [], new Set(["sales"]));
    expect(required[1]?.status).toBe("missing");
  });

  test("已经答复的卡片、非发起人都不能确认", () => {
    const done = view(card({ pending: false, decision: "confirmed" }));
    expect(done.canAnswer).toBe(false);
    expect(done.blockers).toContain("已确认开工");
    const reviewer = view(card(), { role: "reviewer" });
    expect(reviewer.canAnswer).toBe(false);
    expect(reviewer.canConfirm).toBe(false);
    expect(view(card(), { writable: false }).canAnswer).toBe(false);
  });

  test("字段按空串、空对象判未填", () => {
    expect(evaluateFormChecklist(CHECKLIST.slice(0, 1), { period: {} }, [], new Set())[0]?.status).toBe("missing");
    expect(evaluateFormChecklist(CHECKLIST.slice(0, 1), { period: "  " }, [], new Set())[0]?.status).toBe("missing");
  });
});
