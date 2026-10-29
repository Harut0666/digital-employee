// 判定要点核对器的测试装置：用真实渲染器（工具服务器，经 MCP InMemoryTransport）生成一份交付 docx，
// 再拼出一次运行的产物（result.json / events.jsonl / 事实 JSON），凑成一个结果目录。
// 数字都是手写常量，不从被测代码推算，方便断言“期望值与实际值不一致”。
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { createMcpToolClient } from "@de/core";
import type { RenderedReport, WeeklyFacts } from "@de/core/contract";
import { createToolsServer } from "@de/tools";
import JSZip from "jszip";

const metric = (over: Partial<WeeklyFacts["company"]>): WeeklyFacts["company"] => ({
  revenueCents: 10_000_000,
  prevRevenueCents: 12_500_000,
  wow: -0.2,
  grossMarginRate: 0.6,
  targetCents: 12_000_000,
  achievement: 0.833_333,
  deliveryShare: 0.318,
  ...over,
});

/** 一份最小但真实的事实：两家区域、一个异常、退货 11 笔 -260 元。 */
export const FACTS: WeeklyFacts = {
  period: { year: 2026, week: 44, from: "2026-10-26", to: "2026-11-01" },
  dataCoverage: { from: "2026-08-31", to: "2026-11-01" },
  company: metric({}),
  regions: {
    华东: metric({ revenueCents: 6_000_000, prevRevenueCents: 7_500_000, grossMarginRate: 0.61, targetCents: 7_000_000, achievement: 0.857_142 }),
    华南: metric({ revenueCents: 4_000_000, prevRevenueCents: 5_000_000, grossMarginRate: 0.59, targetCents: 5_000_000, achievement: 0.8 }),
  },
  stores: [
    { ...metric({ revenueCents: 6_000_000, prevRevenueCents: 7_500_000 }), code: "HZ01", name: "杭州湖滨店", region: "华东", storeType: "商场店" },
  ],
  categories: [{ category: "饮品", revenueCents: 5_000_000, prevRevenueCents: 6_000_000, wow: -0.166_666 }],
  regionCategories: {
    华南: {
      饮品: { revenueCents: 7_468_274, prevRevenueCents: 5_690_794, grossMarginRate: 0.60547, prevGrossMarginRate: 0.70361 },
    },
  },
  topProducts: [{ code: "D02", name: "拿铁", revenueCents: 2_000_000 }],
  anomalies: [
    {
      id: "region:华东",
      level: "region",
      key: "华东",
      name: "华东",
      region: "华东",
      wow: -0.2,
      revenueCents: 6_000_000,
      prevRevenueCents: 7_500_000,
      baseWeekHasMissingDays: false,
    },
  ],
  trend: [{ week: { year: 2026, week: 44 }, companyCents: 10_000_000, regions: { 华东: 6_000_000, 华南: 4_000_000 } }],
  quality: {
    duplicates: { count: 0, revenueCents: 0, groups: [] },
    idConflicts: { count: 0, recordIds: [] },
    amountMismatch: { count: 0 },
    unknownCodes: { count: 0, revenueCents: 0 },
    returns: {
      count: 11,
      revenueCents: -26_000,
      groups: [
        {
          store: "HZ01",
          storeName: "杭州湖滨店",
          date: "2026-10-15",
          product: "C01",
          productName: "草莓奶油切块",
          count: 5,
          revenueCents: -5_000,
        },
      ],
    },
    storeDays: [],
    prevWeekStoreDays: [],
  },
};

/** 一份“已知正确”的周报正文：核心指标表由渲染器按 truth 生成，叙述里写清判定要点。 */
export const REPORT: RenderedReport = {
  title: "经营周报（2026 年第 44 周）",
  sections: [
    {
      id: "summary",
      heading: "本周摘要",
      paragraphs: ["本周全公司实收 100,000 元，环比 -20.0%。", "纪要称华南本周基本持平，但数据显示华南环比 -20.0%，以数据为准。"],
    },
    { id: "metrics", heading: "核心指标", paragraphs: ["外卖占比为 31.8%。"] },
    {
      id: "anomalies",
      heading: "异常与原因",
      paragraphs: [],
      bullets: ["华东：环比 -20.0%，实收 60,000 元。", "杭州湖滨店（HZ01）：因商场消防改造停业三天。"],
    },
    {
      id: "data_quality",
      heading: "数据质量说明",
      paragraphs: ["本周退货 11 笔，合计 -260 元，已按负数冲减实收。", "杭州湖滨店停业期间没有销售数据，不是数据缺失。"],
    },
  ],
};

export interface RunDir {
  dir: string;
  taskId: string;
  docxPath: string;
}

const sha = (data: string | Buffer): string => createHash("sha256").update(data).digest("hex");

/** 生成一份结果目录；docxPath 是真实渲染出来的交付物。 */
export async function makeRunDir(
  dir: string,
  options: { facts?: WeeklyFacts; report?: RenderedReport; pauseMessage?: string } = {},
): Promise<RunDir> {
  const facts = options.facts ?? FACTS;
  const report = options.report ?? REPORT;
  const taskId = "20261029-120000-test01";
  const taskDir = join(dir, "workspace", "tasks", taskId);

  const tools = createToolsServer();
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await tools.server.connect(serverTransport);
  const toolClient = await createMcpToolClient(clientTransport);
  let rendered: { docxPath: string; sha256: string };
  try {
    const res = await toolClient.call("render_report_docx", {
      report,
      facts,
      outDir: taskDir,
      idempotencyKey: `${taskId}-render_document-r0`,
    });
    if (!res.ok) throw new Error(`render_report_docx 失败：${res.error}`);
    rendered = res.data as { docxPath: string; sha256: string };
  } finally {
    await toolClient.close();
  }

  const artifacts = join(taskDir, "artifacts");
  mkdirSync(artifacts, { recursive: true });
  const factsPath = join(artifacts, `${sha(JSON.stringify(facts))}.json`);
  writeFileSync(factsPath, JSON.stringify(facts));

  const pause = {
    code: "missing_sales",
    message: options.pauseMessage ?? "缺少销售明细：未找到 xlsx 或 csv 格式的销售数据。请上传《销售明细.xlsx》后继续。",
  };
  const events = [
    { type: "task_created", employee: "weekly-report", form: { period: { year: 2026, week: 44 } }, attachments: [], seq: 1, ts: "2026-10-29T00:00:00.000Z", taskId },
    { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator", seq: 2, ts: "2026-10-29T00:00:01.000Z", taskId },
    {
      type: "step_completed",
      stepId: "compute_metrics",
      reworkRound: 0,
      outputPath: factsPath,
      outputSha256: sha(JSON.stringify(facts)),
      seq: 3,
      ts: "2026-10-29T00:00:02.000Z",
      taskId,
    },
    { type: "state_changed", from: "admission", to: "awaiting_initiator", trigger: "admission_failed", actor: "system", pause, seq: 4, ts: "2026-10-29T00:00:03.000Z", taskId },
    {
      type: "step_completed",
      stepId: "render_document",
      reworkRound: 0,
      outputPath: join(taskDir, "render_document.json"),
      outputSha256: sha("render"),
      seq: 5,
      ts: "2026-10-29T00:00:04.000Z",
      taskId,
    },
    {
      type: "model_called",
      stepId: "write_report",
      turn: 1,
      response: "",
      durationMs: 10,
      seq: 6,
      ts: "2026-10-29T00:00:04.500Z",
      taskId,
    },
    {
      type: "model_called",
      stepId: "match_causes",
      turn: 1,
      response: "",
      durationMs: 10,
      seq: 7,
      ts: "2026-10-29T00:00:04.600Z",
      taskId,
    },
    {
      type: "state_changed",
      from: "qc",
      to: "awaiting_review",
      trigger: "qc_passed",
      actor: "system",
      pause: { code: "awaiting_review", message: "已通过质检，等待审核人决定。" },
      seq: 8,
      ts: "2026-10-29T00:00:05.000Z",
      taskId,
    },
  ];
  writeFileSync(join(dir, "events.jsonl"), events.map((e) => JSON.stringify(e)).join("\n") + "\n");

  const qcPath = join(dir, "qc_record-r0.json");
  writeFileSync(qcPath, JSON.stringify({ passed: true, checks: [] }));
  const result = {
    caseId: "X-01",
    title: "核对器测试用例",
    passed: true,
    taskId,
    finalState: "delivered",
    pauseCode: "awaiting_review",
    steps: [],
    deliverables: [
      { kind: "docx", path: rendered.docxPath, sha256: rendered.sha256 },
      { kind: "qc_record", path: qcPath, sha256: sha("qc") },
    ],
    failures: [] as string[],
  };
  writeFileSync(join(dir, "result.json"), JSON.stringify(result, null, 2));
  return { dir, taskId, docxPath: rendered.docxPath };
}

/**
 * 直接改 docx 里的正文（模拟交付物本身错了：渲染器/计算没问题，但交付出去的那份数字不对）。
 * replace 按字面替换，insert 往正文末尾插一段文字。
 */
export async function mutateDocx(path: string, edits: { replace?: [string, string][]; insert?: string }): Promise<void> {
  const zip = await JSZip.loadAsync(readFileSync(path));
  const entry = zip.file("word/document.xml");
  if (!entry) throw new Error("docx 里没有 word/document.xml");
  let xml = await entry.async("string");
  for (const [from, to] of edits.replace ?? []) {
    if (!xml.includes(from)) throw new Error(`docx 正文里没有要替换的文字：${from}`);
    xml = xml.split(from).join(to);
  }
  if (edits.insert) xml = xml.replace("</w:body>", `<w:p><w:r><w:t>${edits.insert}</w:t></w:r></w:p></w:body>`);
  zip.file("word/document.xml", xml);
  writeFileSync(path, await zip.generateAsync({ type: "nodebuffer" }));
}
