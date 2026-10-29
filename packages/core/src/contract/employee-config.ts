// 员工配置契约（docs/design.md §4）。一个员工一个目录，配置文件是唯一的数据来源。
//   SKILL.md          岗位职责、注意事项、写作要求（判断步骤的系统提示词）
//   workflow.yaml     EmployeeWorkflowSchema
//   form.schema.json  发起表单（JSON Schema，前端用 @rjsf/antd 渲染）
//   qc.yaml           QcConfigSchema
//   frontdesk.md      前台 Agent 人设
//   prompts/*.md      各判断步骤的提示词片段
//   schemas/*.json    各判断步骤的输出 JSON Schema

import { z } from "zod";

/**
 * 员工 id 即员工目录名：只允许小写字母、数字和连字符。
 * 引擎的 create、HTTP 层与服务端员工列表共用这一条规则，避免各自为政。
 */
export const EMPLOYEE_ID_PATTERN = /^[a-z0-9-]+$/;
export const EmployeeIdSchema = z.string().regex(EMPLOYEE_ID_PATTERN, "员工 id 只允许小写字母、数字和连字符");
export function isEmployeeId(value: unknown): value is string {
  return typeof value === "string" && EMPLOYEE_ID_PATTERN.test(value);
}

const ModelRefSchema = z.object({
  connection: z.string().default("default"),
  model: z.string().default("default"),
});

/**
 * 步骤的入参映射。值以 "$" 开头时按引用解析：
 *   $form.<path>             表单字段。continue_without_minutes / continue_with_partial 命令
 *                            会由引擎把 form.allowMissingMinutes / form.allowPartialPeriod 置为 true
 *   $steps.<stepId>.<path>   某步骤最近一次完成的输出（重做时即上一版）
 *   $rendered.<stepId>       该步骤输出的 DraftReport，已用 factsStep 的事实做完槽位渲染
 *   $task.dir                任务目录
 *   $task.attachments        AttachmentRef[]
 *   $task.feedback           最近一次退回的 { comment, sections }，没有则为 null
 *   $task.idempotencyKey     任务 + 步骤 + 重做轮次 + 入参哈希，供写文件类工具使用
 *   $config.<path>           qc.yaml 中的值
 * 路径段 "[*]" 表示对数组逐项取值，如 $steps.admission.minutes[*].path。
 * 引用不存在（含步骤尚未执行）时解析为 null，不抛错。
 * 其余值原样传入。
 *
 * 判断步骤的输出若在顶层带 needsHuman: true（及 needsHumanReason），
 * 引擎转入 awaiting_operator，pause.code = "rework_needs_human"。
 */
const InputMappingSchema = z.record(z.string(), z.unknown());

const ComputeStepSchema = z.object({
  id: z.string(),
  label: z.string(),
  kind: z.literal("compute"),
  tool: z.string(),
  input: InputMappingSchema.default({}),
  optional: z.boolean().default(false),
});

const JudgeStepSchema = z.object({
  id: z.string(),
  label: z.string(),
  kind: z.literal("judge"),
  /** 相对员工目录的提示词文件 */
  prompt: z.string(),
  /** 交给模型的上下文，同样支持 $ 引用 */
  input: InputMappingSchema.default({}),
  /** 开放给本步骤的工具；不在列表内的调用被拒绝并留痕 */
  tools: z.array(z.string()).default([]),
  /** 输出 JSON Schema 文件；模型必须调用 submit_result 工具提交符合该 Schema 的结果 */
  outputSchema: z.string(),
  limits: z.object({ rounds: z.number().int().positive(), durationSec: z.number().positive() }),
  model: ModelRefSchema.default({ connection: "default", model: "default" }),
  /** 退回重做时是否重跑本步骤 */
  rework: z.boolean().default(false),
  /** always：每轮都执行；rework：只在重做轮次（reworkRound > 0）执行，首轮记为 skipped */
  when: z.enum(["always", "rework"]).default("always"),
  optional: z.boolean().default(false),
});

const QcStepSchema = z.object({
  id: z.string(),
  label: z.string(),
  kind: z.literal("qc"),
});

export const StepConfigSchema = z.discriminatedUnion("kind", [ComputeStepSchema, JudgeStepSchema, QcStepSchema]);
export type StepConfig = z.infer<typeof StepConfigSchema>;
export type ComputeStepConfig = z.infer<typeof ComputeStepSchema>;
export type JudgeStepConfig = z.infer<typeof JudgeStepSchema>;

export const EmployeeWorkflowSchema = z.object({
  id: EmployeeIdSchema,
  name: z.string(),
  description: z.string().default(""),
  limits: z.object({
    task: z.object({ modelCalls: z.number().int().positive(), durationSec: z.number().positive() }),
    toolRetry: z.object({ attempts: z.number().int().positive(), backoffMs: z.array(z.number().nonnegative()) }),
    qcAutofix: z.number().int().nonnegative(),
  }),
  /** 准入校验步骤的 id，必须是第一个 compute 步骤 */
  admissionStep: z.string(),
  /** 产出 WeeklyFacts 的步骤 id，$rendered.* 与质检用它的输出作为事实 */
  factsStep: z.string().optional(),
  /**
   * 质检的输入取自哪些步骤的输出（缺省值即场景 A 的约定）：
   *   draft     报告草稿（判断步骤的输出，DraftReport），重做时也是“上一版草稿”的来源
   *   minutes   纪要解析结果
   *   rendered  渲染步骤的输出（带 estimatedPages、chartCount）
   * 事实取自 factsStep。
   */
  qcSources: z
    .object({
      draft: z.string().default("write_report"),
      minutes: z.string().default("parse_minutes"),
      rendered: z.string().default("render_document"),
    })
    .prefault({}),
  /**
   * 质检不通过时“应重跑哪个步骤”（QcCheckResult.fixStep 的取值），按问题类别配置：
   *   write    写作类问题（结构、溯源、禁用指标、篇幅、意见响应、自定义规则）；缺省为 qcSources.draft
   *   causes   原因类问题（异常覆盖、引用真实）；缺省 "match_causes"
   * 显式配置的 id 必须是 workflow 中的步骤。
   */
  qcFixSteps: z
    .object({
      write: z.string().optional(),
      causes: z.string().default("match_causes"),
    })
    .prefault({}),
  steps: z.array(StepConfigSchema).min(1),
});
export type EmployeeWorkflow = z.infer<typeof EmployeeWorkflowSchema>;

const CustomRuleSchema = z.object({
  id: z.string(),
  label: z.string(),
  template: z.enum(["forbidden_terms", "required_terms", "section_must_contain", "max_items", "max_pages", "number_range"]),
  enabled: z.boolean().default(true),
  params: z.record(z.string(), z.unknown()),
});

export const QcConfigSchema = z.object({
  builtin: z.object({
    structure: z.object({
      enabled: z.boolean().default(true),
      /** 顺序即周报章节顺序；约定第一个为摘要（受 summaryMaxSentences 约束），最后一个为下周关注（受 nextWeekMaxItems 约束） */
      sections: z.array(z.object({ id: z.string(), heading: z.string() })),
      summaryMaxSentences: z.number().int().positive(),
      nextWeekMaxItems: z.number().int().positive(),
      minCharts: z.number().int().nonnegative(),
    }),
    traceability: z.object({ enabled: z.boolean().default(true) }),
    anomaly_coverage: z.object({ enabled: z.boolean().default(true), threshold: z.number().positive() }),
    quote_integrity: z.object({ enabled: z.boolean().default(true) }),
    forbidden_terms: z.object({ enabled: z.boolean().default(true), terms: z.array(z.string()) }),
    length: z.object({ enabled: z.boolean().default(true), maxPages: z.number().positive() }),
    feedback_response: z.object({ enabled: z.boolean().default(true) }),
  }),
  contradiction: z.object({
    flatTolerance: z.number().nonnegative(),
    directionMin: z.number().nonnegative(),
    magnitudeTolerance: z.number().nonnegative(),
  }),
  custom: z.array(CustomRuleSchema).default([]),
});
export type QcConfig = z.infer<typeof QcConfigSchema>;

/** 加载后的完整员工配置 */
export interface EmployeeConfig {
  id: string;
  dir: string;
  /** 全部配置文件内容的 sha256，写入 task_created 事件 */
  hash: string;
  skill: string;
  frontdesk: string;
  workflow: EmployeeWorkflow;
  qc: QcConfig;
  formSchema: Record<string, unknown>;
  /** form.ui.json（@rjsf 的 uiSchema），没有则为 undefined */
  formUi?: Record<string, unknown>;
  prompts: Record<string, string>;
  outputSchemas: Record<string, Record<string, unknown>>;
}
