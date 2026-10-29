import { describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseCase, runCase } from "../src/case";
import { createFakeEngine, type Rule } from "./fake-engine";

function tmp(): string {
  return mkdtempSync(join(tmpdir(), "de-case-"));
}

/** A-03 形态的假引擎：缺销售明细则暂停，补传后到待审核，审核人通过后交付。 */
const weeklyRule: Rule = ({ state, command, actor, attachments }) => {
  if (command.type === "submit" && state === "draft") {
    return actor === "initiator" ? { state: "awaiting_initiator", pauseCode: "missing_sales" } : { error: "forbidden_role" };
  }
  if (command.type === "supplement" && state === "awaiting_initiator") {
    return attachments.some((a) => a.name.endsWith(".xlsx")) ? { state: "awaiting_review", pauseCode: "awaiting_review" } : { state: "awaiting_initiator", pauseCode: "missing_sales" };
  }
  if (command.type === "approve" && state === "awaiting_review") {
    return actor === "reviewer"
      ? { state: "delivered", deliverables: [{ kind: "docx", path: "D:/x/周报.docx", sha256: "a" }] }
      : { error: "forbidden_role" };
  }
  return { error: "illegal_transition" };
};

function caseDir() {
  const dir = tmp();
  mkdirSync(join(dir, "data"));
  writeFileSync(join(dir, "data", "销售明细.xlsx"), "xlsx");
  writeFileSync(join(dir, "data", "纪要.docx"), "docx");
  return dir;
}

const A03 = {
  id: "A-03",
  title: "缺材料",
  employee: "weekly-report",
  form: { period: { year: 2026, week: 44 } },
  attachments: [{ name: "纪要.docx", path: "data/纪要.docx" }],
  steps: [
    { actor: "initiator", command: { type: "submit" }, expect: { state: "awaiting_initiator", pauseCode: "missing_sales" } },
    {
      actor: "initiator",
      command: { type: "supplement", attachments: [{ name: "销售明细.xlsx", path: "data/销售明细.xlsx" }] },
      expect: { state: "awaiting_review" },
    },
    { actor: "reviewer", command: { type: "approve" }, expect: { state: "delivered", deliverables: ["docx"] } },
  ],
  expect: { state: "delivered" },
};

describe("parseCase", () => {
  test("resolves attachment paths against the case file directory", () => {
    const dir = caseDir();
    const c = parseCase(A03, dir);
    expect(c.attachments).toEqual([{ name: "纪要.docx", sourcePath: resolve(dir, "data/纪要.docx") }]);
    const supplement = c.steps[1]!.command;
    expect(supplement.type === "supplement" && supplement.attachments).toEqual([
      { name: "销售明细.xlsx", sourcePath: resolve(dir, "data/销售明细.xlsx") },
    ]);
  });

  test("defaults: no attachments, empty form", () => {
    const c = parseCase({ id: "X", employee: "e", steps: [{ actor: "initiator", command: { type: "submit" } }] }, tmp());
    expect(c.attachments).toEqual([]);
    expect(c.form).toEqual({});
  });

  test.each([
    ["missing id", { employee: "e", steps: [] }, /id/],
    ["missing employee", { id: "X", steps: [] }, /employee/],
    ["no steps", { id: "X", employee: "e" }, /steps/],
    ["unknown actor", { id: "X", employee: "e", steps: [{ actor: "boss", command: { type: "submit" } }] }, /actor/],
    ["unknown command", { id: "X", employee: "e", steps: [{ actor: "initiator", command: { type: "fly" } }] }, /command/],
    ["return without comment", { id: "X", employee: "e", steps: [{ actor: "reviewer", command: { type: "return" } }] }, /comment/],
    ["bad attachment", { id: "X", employee: "e", attachments: [{ name: "a" }], steps: [{ actor: "initiator", command: { type: "submit" } }] }, /path/],
  ])("rejects an invalid case: %s", (_label, raw, pattern) => {
    expect(() => parseCase(raw, tmp())).toThrow(pattern);
  });

  test("parses the top-level expect.checks into check items", () => {
    const raw = structuredClone(A03) as Record<string, unknown>;
    raw.expect = {
      state: "delivered",
      checks: {
        numbers: [{ path: "table:全公司/实收（元）", expected: 573635 }],
        mustContain: ["以数据为准"],
        mustNotContain: ["客单价"],
        anomalies: ["region:华东"],
        pause: { code: "missing_sales", messageContains: "销售明细" },
      },
    };
    const c = parseCase(raw, caseDir());
    expect(c.expect?.checks?.numbers).toEqual([{ path: "table:全公司/实收（元）", expected: 573635 }]);
    expect(c.expect?.checks?.mustNotContain).toEqual(["客单价"]);
    expect(c.expect?.checks?.pause?.code).toBe("missing_sales");
  });

  test("rejects checks on a step expectation (only the top-level expect may carry them)", () => {
    const raw = structuredClone(A03) as Record<string, unknown>;
    const steps = raw.steps as Record<string, unknown>[];
    steps[0]!.expect = { state: "awaiting_initiator", checks: { anomalies: ["region:华东"] } };
    expect(() => parseCase(raw, caseDir())).toThrow(/steps\[0\]/);
  });

  test("rejects a malformed checks block and names the case file", () => {
    const raw = structuredClone(A03) as Record<string, unknown>;
    raw.expect = { checks: { numbers: [{ path: "doc:xxx", expected: 1 }] } };
    expect(() => parseCase(raw, caseDir(), "A-99.json")).toThrow(/A-99\.json.*checks/);
  });
});

describe("runCase", () => {
  test("drives the commands in order, waits for the task to settle after each, and writes snapshot, events and result", async () => {
    const dir = caseDir();
    const out = join(tmp(), "out");
    const engine = createFakeEngine(weeklyRule);
    const result = await runCase(engine, parseCase(A03, dir), { outDir: out });

    expect(result.passed).toBe(true);
    expect(result.finalState).toBe("delivered");
    expect(result.steps.map((s) => s.state)).toEqual(["awaiting_initiator", "awaiting_review", "delivered"]);
    expect(result.deliverables).toEqual([{ kind: "docx", path: "D:/x/周报.docx", sha256: "a" }]);
    // 创建者、命令序列与等待交替出现
    expect(engine.calls.map((c) => c.type)).toEqual(["create", "command", "wait", "command", "wait", "command", "wait"]);
    expect((engine.calls[0]!.detail as { actor: string }).actor).toBe("initiator");

    const snapshot = JSON.parse(readFileSync(join(out, "snapshot.json"), "utf8"));
    expect(snapshot.state).toBe("delivered");
    const events = readFileSync(join(out, "events.jsonl"), "utf8").trim().split("\n").map((l) => JSON.parse(l));
    expect(events.map((e) => e.seq)).toEqual([1, 2, 3, 4]);
    const saved = JSON.parse(readFileSync(join(out, "result.json"), "utf8"));
    expect(saved).toMatchObject({ caseId: "A-03", passed: true, taskId: "T1", finalState: "delivered" });
    expect(saved.deliverables[0].path).toBe("D:/x/周报.docx");
  });

  test("a failed expectation marks the case failed, stops the sequence, and still writes the results", async () => {
    const dir = caseDir();
    const out = join(tmp(), "out");
    const bad = structuredClone(A03);
    bad.steps[0]!.expect = { state: "delivered", pauseCode: "missing_sales" };
    const result = await runCase(createFakeEngine(weeklyRule), parseCase(bad, dir), { outDir: out });
    expect(result.passed).toBe(false);
    expect(result.steps).toHaveLength(1);
    expect(result.steps[0]!.failures[0]).toContain("delivered");
    expect(existsSync(join(out, "result.json"))).toBe(true);
    expect(existsSync(join(out, "events.jsonl"))).toBe(true);
  });

  test("an expected rejection (wrong role) passes when the step says ok:false with the error code", async () => {
    const dir = caseDir();
    const c = structuredClone(A03);
    c.steps.splice(1, 2, { actor: "initiator", command: { type: "approve" }, expect: { ok: false, error: "illegal_transition", state: "awaiting_initiator" } } as never);
    c.expect = { state: "awaiting_initiator" } as never;
    const result = await runCase(createFakeEngine(weeklyRule), parseCase(c, dir), { outDir: join(tmp(), "o") });
    expect(result.passed).toBe(true);
    expect(result.steps[1]!.ok).toBe(false);
    expect(result.steps[1]!.error).toBe("illegal_transition");
  });

  test("an unexpected rejection fails the case", async () => {
    const dir = caseDir();
    const c = structuredClone(A03);
    c.steps[0]!.actor = "reviewer"; // 越权提交
    const result = await runCase(createFakeEngine(weeklyRule), parseCase(c, dir), { outDir: join(tmp(), "o") });
    expect(result.passed).toBe(false);
    expect(result.steps[0]!.failures[0]).toContain("forbidden_role");
  });

  test("final expectations are checked too (state, pauseCode, deliverables)", async () => {
    const dir = caseDir();
    const c = structuredClone(A03);
    c.steps = c.steps.slice(0, 1);
    c.expect = { state: "delivered" };
    const result = await runCase(createFakeEngine(weeklyRule), parseCase(c, dir), { outDir: join(tmp(), "o") });
    expect(result.passed).toBe(false);
    expect(result.failures[0]).toContain("delivered");
  });
});
