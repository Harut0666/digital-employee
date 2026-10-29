// 员工配置契约（docs/design.md §4）。一个员工一个目录，配置文件是唯一的数据来源。
//   SKILL.md          岗位职责、注意事项、写作要求（判断步骤的系统提示词）
//   workflow.yaml     EmployeeWorkflowSchema
//   form.schema.json  发起表单（JSON Schema，前端用 @rjsf/antd 渲染）
//   qc.yaml           QcConfigSchema
//   frontdesk.md      前台 Agent 人设
//   prompts/*.md      各判断步骤的提示词片段
//   schemas/*.json    各判断步骤的输出 JSON Schema

import { z } from "zod";

const ModelRefSchema = z.object({
  connection: z.string().default("default"),
  model: z.string().default("default"),
});

/**
 * 计算步骤的入参映射。值以 "$" 开头时按引用解析：
 *   $form.<path>            表单字段
 *   $steps.<stepId>.<path>  某步骤输出
 *   $task.dir               任务目录
 *   $config.<path>          qc.yaml 中的值
 * 其余值原样传入。
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
  id: z.string(),
  name: z.string(),
  description: z.string().default(""),
  limits: z.object({
    task: z.object({ modelCalls: z.number().int().positive(), durationSec: z.number().positive() }),
    toolRetry: z.object({ attempts: z.number().int().positive(), backoffMs: z.array(z.number().nonnegative()) }),
    qcAutofix: z.number().int().nonnegative(),
  }),
  /** 准入校验步骤的 id，必须是第一个 compute 步骤 */
  admissionStep: z.string(),
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
  prompts: Record<string, string>;
  outputSchemas: Record<string, Record<string, unknown>>;
}
