import { describe, expect, test } from "bun:test";
import type { SessionChecklist } from "../src/api";
import { checklistBar, qcRows } from "../src/view/checklist";

const checklist = (statuses: Record<string, "done" | "missing" | "skipped">): SessionChecklist => {
  const items = [
    { id: "period", label: "统计周期", required: true },
    { id: "sales", label: "销售明细", required: true },
    { id: "minutes", label: "本周纪要", required: false, skipLabel: "无纪要继续" },
  ].map((item) => ({ ...item, status: statuses[item.id] ?? "missing" }));
  const missing = items.filter((i) => i.required && i.status === "missing").map((i) => i.label);
  return { items, ready: missing.length === 0, missing };
};

describe("checklistBar：开工前展开，开工后收成一行", () => {
  test("开工前展开，逐项说明，还缺的必需项写在标题里", () => {
    const bar = checklistBar(checklist({ period: "done" }), { started: false });
    expect(bar.mode).toBe("expanded");
    expect(bar.headline).toBe("开工清单 1/3 已备齐 · 还缺：销售明细");
    expect(bar.ready).toBe(false);
    expect(bar.rows.map((r) => [r.label, r.tone, r.stateText])).toEqual([
      ["统计周期", "ok", "已备齐"],
      ["销售明细", "todo", "还缺"],
      ["本周纪要", "todo", "可跳过"],
    ]);
  });

  test("只有还缺的可跳过项能点跳过，按钮用配置里的说法", () => {
    const bar = checklistBar(checklist({}), { started: false });
    expect(bar.rows.map((r) => r.skipLabel)).toEqual([undefined, undefined, "无纪要继续"]);
    const uploaded = checklistBar(checklist({ minutes: "done" }), { started: false });
    expect(uploaded.rows[2]!.skipLabel).toBeUndefined();
  });

  test("跳过的项单独标出，算作不阻挡开工", () => {
    const bar = checklistBar(checklist({ period: "done", sales: "done", minutes: "skipped" }), { started: false });
    expect(bar.ready).toBe(true);
    expect(bar.headline).toBe("开工清单 2/3 已备齐 · 跳过 1 项");
    expect(bar.rows[2]).toMatchObject({ tone: "skip", stateText: "已跳过（无纪要继续）" });
  });

  test("开工后收成一行，不再给跳过按钮", () => {
    const bar = checklistBar(checklist({ period: "done", sales: "done", minutes: "done" }), { started: true });
    expect(bar.mode).toBe("collapsed");
    expect(bar.headline).toBe("开工清单 3/3 已备齐");
    expect(bar.rows.every((r) => r.skipLabel === undefined)).toBe(true);
  });
});

describe("qcRows：质检结果用同样的清单样式", () => {
  test("通过的打勾，未通过的标红并带问题", () => {
    const rows = qcRows([
      { id: "structure", label: "结构完整", passed: true, issues: [] },
      { id: "traceability", label: "数据溯源", passed: false, issues: ["第 2 段的数字没有来源"] },
    ]);
    expect(rows.map((r) => [r.label, r.tone, r.stateText])).toEqual([
      ["结构完整", "ok", "通过"],
      ["数据溯源", "bad", "未通过"],
    ]);
    expect(rows[1]!.detail).toBe("第 2 段的数字没有来源");
  });
});
