import { afterEach, describe, expect, test } from "bun:test";
import { att, connect, type Harness } from "./helpers";

let h: Harness;
afterEach(() => h?.close());

// 引擎对不存在的 $form 引用解析为 null（employee-config 契约），所以可选入参必须接受 null。
describe("可选入参接受 null（引擎把缺失的引用解析为 null）", () => {
  test("validate_submission：allowMissingMinutes / allowPartialPeriod 为 null 等同于未提供", async () => {
    h = await connect();
    const out = await h.call("validate_submission", {
      period: { year: 2026, week: 44 },
      attachments: [att("销售明细.xlsx")],
      allowMissingMinutes: null,
      allowPartialPeriod: null,
    });
    expect(out.ok).toBe(false);
    expect(out.pause.code).toBe("minutes_missing");
  });
});
