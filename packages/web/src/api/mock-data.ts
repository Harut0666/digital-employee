// 模拟数据：员工、步骤定义、种子任务。把暂停（待发起人补充 / 待人工处理）、待审核、已交付、执行中几种状态都摆出来。

import type { PauseReason, QcCheckResult, TaskEvent } from "@de/core/contract";
import type { StepDef } from "../view/session-view";
import type { EmployeeInfo } from "./types";

export const EMPLOYEES: EmployeeInfo[] = [
  {
    id: "weekly-report",
    name: "经营周报员工",
    description: "基于业务数据，生成经营周报",
    formSchema: {
      type: "object",
      required: ["period"],
      properties: {
        period: {
          type: "object",
          title: "统计周期",
          required: ["year", "week"],
          properties: {
            year: { type: "integer", title: "年份", default: 2026 },
            week: { type: "integer", title: "ISO 周次", default: 44, minimum: 1, maximum: 53 },
          },
        },
        salesFile: { type: "string", title: "销售明细", description: "销售明细文件" },
        minutesFiles: { type: "array", title: "周会纪要", items: { type: "string" } },
      },
    },
    formUi: {
      salesFile: { "ui:widget": "file", "ui:options": { accept: ".xlsx,.csv" }, "ui:help": "支持 xlsx、csv；推荐 xlsx。" },
      minutesFiles: { "ui:widget": "files", "ui:options": { accept: ".docx" }, "ui:help": "可多选；上传本周和前几周的纪要都可以。" },
    },
    reviewSections: [
      { id: "summary", heading: "本周摘要" },
      { id: "metrics", heading: "核心指标" },
      { id: "categories", heading: "品类表现" },
      { id: "anomalies", heading: "异常与原因" },
      { id: "data_quality", heading: "数据质量说明" },
      { id: "next_week", heading: "下周关注事项" },
    ],
  },
  {
    id: "tech-review",
    name: "技术文献研判员工",
    description: "研判技术文献，提炼关键信息",
    formSchema: {
      type: "object",
      required: ["topic"],
      properties: {
        topic: { type: "string", title: "研判主题", default: "" },
        focus: { type: "string", title: "关注点", enum: ["技术路线", "成熟度", "风险"], default: "技术路线" },
      },
    },
  },
];

export const STEP_DEFS: Record<string, StepDef[]> = {
  "weekly-report": [
    { id: "admission", label: "准入校验", kind: "compute" },
    { id: "load", label: "读取数据", kind: "compute" },
    { id: "metrics", label: "计算指标", kind: "compute" },
    { id: "minutes", label: "解析纪要", kind: "compute" },
    { id: "match", label: "匹配原因", kind: "judge" },
    { id: "write", label: "撰写周报", kind: "judge" },
    { id: "render", label: "生成文档", kind: "compute" },
    { id: "qc", label: "自动质检", kind: "qc" },
  ],
  "tech-review": [
    { id: "admission", label: "准入校验", kind: "compute" },
    { id: "parse", label: "解析资料", kind: "compute" },
    { id: "extract", label: "提炼要点", kind: "judge" },
    { id: "route", label: "梳理技术路线", kind: "judge" },
    { id: "render", label: "生成文档", kind: "compute" },
    { id: "qc", label: "自动质检", kind: "qc" },
  ],
};

/** 重做时需要重跑的步骤（只重跑判断步骤） */
export const REWORK_STEPS: Record<string, string[]> = {
  "weekly-report": ["match", "write", "render"],
  "tech-review": ["extract", "route", "render"],
};

export const QC_CHECKS_PASSED: QcCheckResult[] = [
  { id: "structure", label: "结构完整", passed: true, issues: [] },
  { id: "traceability", label: "数据溯源", passed: true, issues: [] },
  { id: "anomaly_coverage", label: "异常覆盖", passed: true, issues: [] },
  { id: "quote_integrity", label: "引用真实", passed: true, issues: [] },
  { id: "forbidden_terms", label: "禁用指标", passed: true, issues: [] },
  { id: "length", label: "篇幅", passed: true, issues: [] },
];

export type EventInput = TaskEvent extends infer T ? (T extends unknown ? Omit<T, "seq" | "ts" | "taskId"> : never) : never;

export interface SeedTask {
  id: string;
  employee: string;
  title: string;
  form: Record<string, unknown>;
  /** [距起点秒数, 事件] */
  script: [number, EventInput][];
  /** 起点相对现在的毫秒偏移（负数 = 过去） */
  startedAgoMs: number;
  /** 种子加载后继续自动推进（演示“执行中”） */
  autoplay?: { fromStepIndex: number };
}

const created = (employee: string, form: Record<string, unknown>): EventInput => ({
  type: "task_created",
  employee,
  configHash: "mock",
  form,
  attachments: [],
});
const submit: EventInput = { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" };
const passed: EventInput = { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" };
const ok = (stepId: string, round = 0): EventInput[] => [
  { type: "step_started", stepId, attempt: 1, reworkRound: round },
  { type: "step_completed", stepId, reworkRound: round, outputPath: `steps/${stepId}.json`, outputSha256: "mock" },
];
const judgeOk = (stepId: string, round = 0): EventInput[] => [
  { type: "step_started", stepId, attempt: 1, reworkRound: round },
  { type: "model_called", stepId, turn: 1, response: {}, durationMs: 2400 },
  { type: "model_called", stepId, turn: 2, response: {}, durationMs: 3100 },
  { type: "step_completed", stepId, reworkRound: round, outputPath: `steps/${stepId}.json`, outputSha256: "mock" },
];
const spaced = (start: number, gap: number, events: EventInput[]): [number, EventInput][] => events.map((e, i) => [start + i * gap, e]);

const weeklyForm = (week: number) => ({ period: { year: 2026, week } });

const toQc = (start: number): [number, EventInput][] => [
  [start, { type: "state_changed", from: "running", to: "qc", trigger: "steps_completed", actor: "system" }],
  [start + 1, { type: "step_started", stepId: "qc", attempt: 1, reworkRound: 0 }],
  [start + 4, { type: "qc_result", attempt: 1, passed: true, checks: QC_CHECKS_PASSED }],
  [start + 4, { type: "step_completed", stepId: "qc", reworkRound: 0, outputPath: "qc.json", outputSha256: "mock" }],
  [start + 5, { type: "deliverable_created", kind: "docx", path: "经营周报.docx", sha256: "mock-docx" }],
  [start + 5, { type: "deliverable_created", kind: "qc_record", path: "检验记录.md", sha256: "mock-qc" }],
];

const reviewPause: PauseReason = {
  code: "awaiting_review",
  message: "质检通过，请审核",
  options: ["approve", "return"],
};

const mainSteps = (round = 0): EventInput[] => [
  ...ok("admission", round),
  ...ok("load", round),
  ...ok("metrics", round),
  ...ok("minutes", round),
  ...judgeOk("match", round),
  ...judgeOk("write", round),
  ...ok("render", round),
];

export const SEEDS: SeedTask[] = [
  {
    // 02-tool-failure：执行中工具故障，待人工处理
    id: "mock-44",
    employee: "weekly-report",
    title: "第 44 周周报",
    form: weeklyForm(44),
    startedAgoMs: 25 * 60_000,
    script: [
      [0, created("weekly-report", weeklyForm(44))],
      [1, submit],
      [3, passed],
      ...spaced(4, 8, [...ok("admission"), ...ok("load"), ...ok("metrics")]),
      [60, { type: "step_started", stepId: "minutes", attempt: 1, reworkRound: 0 }],
      [70, { type: "tool_called", stepId: "minutes", callId: "c1", tool: "parse_minutes", args: {}, attempt: 1 }],
      [100, { type: "tool_result", callId: "c1", ok: false, error: "超时" }],
      [110, { type: "tool_called", stepId: "minutes", callId: "c2", tool: "parse_minutes", args: {}, attempt: 2 }],
      [140, { type: "tool_result", callId: "c2", ok: false, error: "超时" }],
      [150, { type: "tool_called", stepId: "minutes", callId: "c3", tool: "parse_minutes", args: {}, attempt: 3 }],
      [190, { type: "tool_result", callId: "c3", ok: false, error: "超时" }],
      [198, { type: "step_failed", stepId: "minutes", error: "解析纪要工具调用失败" }],
      [
        200,
        {
          type: "state_changed",
          from: "running",
          to: "awaiting_operator",
          trigger: "tool_failed",
          actor: "system",
          pause: {
            code: "tool_failed",
            message: "解析纪要工具超时，已重试 3 次，已完成 3/8 步",
            options: ["retry", "terminate"],
            detail: { tool: "parse_minutes", attempts: 3 },
          },
        },
      ],
    ],
  },
  {
    // 03-review：质检通过，待审核
    id: "mock-43",
    employee: "weekly-report",
    title: "第 43 周周报",
    form: weeklyForm(43),
    startedAgoMs: 3 * 3600_000,
    script: [
      [0, created("weekly-report", weeklyForm(43))],
      [1, submit],
      [3, passed],
      ...spaced(4, 10, mainSteps()),
      ...toQc(90),
      [96, { type: "state_changed", from: "qc", to: "awaiting_review", trigger: "qc_passed", actor: "system", pause: reviewPause }],
    ],
  },
  {
    // 待发起人补充：缺纪要
    id: "mock-45",
    employee: "weekly-report",
    title: "第 45 周周报",
    form: weeklyForm(45),
    startedAgoMs: 50 * 60_000,
    script: [
      [0, created("weekly-report", weeklyForm(45))],
      [1, submit],
      [
        3,
        {
          type: "state_changed",
          from: "admission",
          to: "awaiting_initiator",
          trigger: "admission_failed",
          actor: "system",
          pause: {
            code: "minutes_missing",
            message: "缺少第 45 周的周会纪要。可以补传纪要，或选择“无纪要继续”（异常原因将标注“原因待核实”）。",
            options: ["supplement", "continue_without_minutes", "terminate"],
          },
        },
      ],
    ],
  },
  {
    // 执行中：加载后自动继续往下走
    id: "mock-46",
    employee: "weekly-report",
    title: "第 46 周周报",
    form: weeklyForm(46),
    startedAgoMs: 20_000,
    script: [
      [0, created("weekly-report", weeklyForm(46))],
      [1, submit],
      [3, passed],
      ...spaced(4, 2, [...ok("admission"), ...ok("load")]),
    ],
    autoplay: { fromStepIndex: 2 },
  },
  ...[42, 41].map<SeedTask>((week, i) => ({
    id: `mock-${week}`,
    employee: "weekly-report",
    title: `第 ${week} 周周报`,
    form: weeklyForm(week),
    startedAgoMs: (7 * (i + 1) + 1) * 24 * 3600_000,
    script: [
      [0, created("weekly-report", weeklyForm(week))],
      [1, submit],
      [3, passed],
      ...spaced(4, 10, mainSteps()),
      ...toQc(90),
      [96, { type: "state_changed", from: "qc", to: "awaiting_review", trigger: "qc_passed", actor: "system", pause: reviewPause }],
      [300, { type: "review_decision", decision: "approve" }],
      [301, { type: "state_changed", from: "awaiting_review", to: "delivered", trigger: "approve", actor: "reviewer" }],
    ],
  })),
];


