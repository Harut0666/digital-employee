// 设置文档 v2（Spec 0004「设置文档 v2 形状」）。
//
// settings.json 是设置唯一的数据来源：有新版本时就地迁移、未知顶层键原样保留、
// 文档内保存 revision（每次写入 +1）做乐观锁。这里放类型、默认值与 zod schema；
// 读写与迁移在 `config/settings-document.ts`。
//
// 注意：本文件只定义“文档长什么样”，模型参数如何生效由 T20（agent / 任务引擎）负责。
import { z } from "zod";
import type { DemoSettings, ModelConnection } from "./engine";

/** 当前设置文档版本；`version` 缺失视为 v1。 */
export const SETTINGS_VERSION = 2;

// ---------------------------------------------------------------- 枚举

/** 推理档位：与 pi-ai / DSH 的口径一致；未知值按 off 处理（T20 消费）。 */
export const THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh"] as const;
export type ThinkingLevel = (typeof THINKING_LEVELS)[number];
export const ThinkingLevelSchema = z.enum(THINKING_LEVELS);

export const THEMES = ["light", "dark", "system"] as const;
export type Theme = (typeof THEMES)[number];

export const TRAJECTORY_DETAILS = ["compact", "standard", "detailed"] as const;
export type TrajectoryDetail = (typeof TRAJECTORY_DETAILS)[number];

export const COLLAPSE_TIMINGS = ["completion", "next-input"] as const;
export type CollapseTiming = (typeof COLLAPSE_TIMINGS)[number];

export const ENTER_BEHAVIORS = ["send", "newline"] as const;
export type EnterBehavior = (typeof ENTER_BEHAVIORS)[number];

export const LINK_OPENINGS = ["in-app", "system"] as const;
export type LinkOpening = (typeof LINK_OPENINGS)[number];

export const LOG_LEVELS = ["error", "warn", "info", "debug"] as const;
export type LogLevel = (typeof LOG_LEVELS)[number];

export const SANDBOX_MODES = ["read-only", "workspace-write", "danger-full-access"] as const;
export type SandboxMode = (typeof SANDBOX_MODES)[number];

export const APPROVAL_POLICIES = ["ask", "never"] as const;
export type ApprovalPolicy = (typeof APPROVAL_POLICIES)[number];

export const DEFAULT_ROLES = ["initiator", "reviewer", "operator"] as const;
export type DefaultRole = (typeof DEFAULT_ROLES)[number];

// ---------------------------------------------------------------- 分区

/** 外观与交互（即时生效）。 */
export interface UiSettings {
  theme: Theme;
  /** 界面字号，12–18，默认 14 */
  fontSize: number;
  trajectoryDetail: TrajectoryDetail;
  /** 轨迹折叠时机：步骤完成即折叠 / 下次输入时折叠 */
  collapseTiming: CollapseTiming;
  enterBehavior: EnterBehavior;
  linkOpening: LinkOpening;
}

/**
 * 执行与预算（对之后开始的步骤 / 模型调用生效）。
 * 每一项都是“默认值”：员工配置或连接配置显式写了就以更靠近步骤的值为准。
 */
export interface ExecutionSettings {
  /** 单个工作步骤超时，毫秒，默认 120000；判断步骤没写 limits.durationSec 时用它 */
  stepTimeoutMs: number;
  /** 模型调用重试次数，默认 2；连接上的 maxRetries 优先 */
  modelMaxRetries: number;
  /** 重试退避，毫秒；按第 N 次重试取第 N 项。工具重试的 backoffMs 为空时用它 */
  retryBackoffMs: number[];
  /** 质检自动修正上限；员工配置没写 limits.qcAutofix 时用它，默认 3 */
  qcAutofix: number;
}

export interface DiagnosticsSettings {
  /** 日志级别，即时生效，默认 info */
  logLevel: LogLevel;
}

/** 权限与审批（本机模式，只控制工具白名单 + 审批 + 审计，不是系统级沙箱）。 */
export interface PermissionSettings {
  sandboxMode: SandboxMode;
  approvalPolicy: ApprovalPolicy;
  /** 只决定打开应用时的初始身份；服务端仍按请求角色校验 */
  defaultRole: DefaultRole;
}

/** 每个模型单独的高级参数（按模型 id 配置，同一连接下的模型互不影响）。 */
export interface ModelOptions {
  /** 上下文窗口，token；缺省沿用 128k */
  contextWindow?: number;
  /** 输出上限，token；缺省沿用 8192 */
  maxTokens?: number;
  /** 该模型可用的推理档位；缺省为全部档位 */
  reasoningEfforts?: ThinkingLevel[];
  /** 该模型默认推理档位；缺省 off */
  defaultReasoningEffort?: ThinkingLevel;
}

// ---------------------------------------------------------------- 文档

export interface AppSettings {
  version: typeof SETTINGS_VERSION;
  /** 乐观锁：每次写入 +1；缺失（v1）时按 0 读 */
  revision: number;
  ui: UiSettings;
  execution: ExecutionSettings;
  diagnostics: DiagnosticsSettings;
  permissions: PermissionSettings;
}

/**
 * 完整的设置文档（v2）。
 * `extra` 是未识别的顶层键（含其它模块写入的键）：读时原样保留，写时原样写回。
 */
export interface SettingsDocument extends AppSettings {
  connections: ModelConnection[];
  defaultConnection: string;
  defaultModel: string;
  /** 演示与调试（docs/design.md §14.1），由设置页与引擎共用 */
  demo: DemoSettings;
  extra: Record<string, unknown>;
}

// ---------------------------------------------------------------- 默认值

export const DEFAULT_UI_SETTINGS: UiSettings = {
  theme: "system",
  fontSize: 14,
  trajectoryDetail: "standard",
  collapseTiming: "completion",
  enterBehavior: "send",
  linkOpening: "in-app",
};

export const DEFAULT_EXECUTION_SETTINGS: ExecutionSettings = {
  stepTimeoutMs: 120_000,
  modelMaxRetries: 2,
  retryBackoffMs: [500, 1500, 4000],
  qcAutofix: 3,
};

export const DEFAULT_DIAGNOSTICS_SETTINGS: DiagnosticsSettings = { logLevel: "info" };

export const DEFAULT_PERMISSION_SETTINGS: PermissionSettings = {
  sandboxMode: "workspace-write",
  approvalPolicy: "ask",
  defaultRole: "initiator",
};

/** 新文档（无 settings.json）用的四个分区默认值。 */
export function defaultAppSettings(revision = 0): AppSettings {
  return {
    version: SETTINGS_VERSION,
    revision,
    ui: { ...DEFAULT_UI_SETTINGS },
    execution: { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [...DEFAULT_EXECUTION_SETTINGS.retryBackoffMs] },
    diagnostics: { ...DEFAULT_DIAGNOSTICS_SETTINGS },
    permissions: { ...DEFAULT_PERMISSION_SETTINGS },
  };
}

// ---------------------------------------------------------------- 范围

/** 字号范围（Spec 0004）。 */
export const FONT_SIZE_MIN = 12;
export const FONT_SIZE_MAX = 18;
/** 步骤超时的允许范围：1 秒–1 小时。 */
export const STEP_TIMEOUT_MIN_MS = 1_000;
export const STEP_TIMEOUT_MAX_MS = 3_600_000;
export const MODEL_RETRIES_MAX = 10;
/** 退避数组最多几项（第 N 次重试取第 N 项）。 */
export const RETRY_BACKOFF_MAX = 10;
/** 单项退避的上限：10 分钟。 */
export const RETRY_BACKOFF_ITEM_MAX_MS = 600_000;
export const QC_AUTOFIX_MAX = 10;
export const CONTEXT_WINDOW_MIN = 1_000;
export const CONTEXT_WINDOW_MAX = 4_000_000;
export const MAX_TOKENS_MIN = 1;
export const MAX_TOKENS_MAX = 2_000_000;
/** 连接级超时与重试的范围。 */
export const CONNECTION_TIMEOUT_MIN_MS = 1_000;
export const CONNECTION_TIMEOUT_MAX_MS = 3_600_000;
export const CONNECTION_RETRIES_MAX = 10;

// ---------------------------------------------------------------- schema

/** 0..max 的整数：用于重试次数、自动修正次数这类“次数”。 */
const boundedCount = (max: number) => z.number().int().min(0).max(max);
/** 0..max 的整数毫秒：用于退避间隔这类“时长”。 */
const milliseconds = (max: number) => z.number().int().min(0).max(max);

export const UiSettingsSchema = z.strictObject({
  theme: z.enum(THEMES).default(DEFAULT_UI_SETTINGS.theme),
  fontSize: z.number().int().min(FONT_SIZE_MIN).max(FONT_SIZE_MAX).default(DEFAULT_UI_SETTINGS.fontSize),
  trajectoryDetail: z.enum(TRAJECTORY_DETAILS).default(DEFAULT_UI_SETTINGS.trajectoryDetail),
  collapseTiming: z.enum(COLLAPSE_TIMINGS).default(DEFAULT_UI_SETTINGS.collapseTiming),
  enterBehavior: z.enum(ENTER_BEHAVIORS).default(DEFAULT_UI_SETTINGS.enterBehavior),
  linkOpening: z.enum(LINK_OPENINGS).default(DEFAULT_UI_SETTINGS.linkOpening),
});

export const ExecutionSettingsSchema = z.strictObject({
  stepTimeoutMs: z
    .number()
    .int()
    .min(STEP_TIMEOUT_MIN_MS)
    .max(STEP_TIMEOUT_MAX_MS)
    .default(DEFAULT_EXECUTION_SETTINGS.stepTimeoutMs),
  modelMaxRetries: boundedCount(MODEL_RETRIES_MAX).default(DEFAULT_EXECUTION_SETTINGS.modelMaxRetries),
  retryBackoffMs: z
    .array(milliseconds(RETRY_BACKOFF_ITEM_MAX_MS))
    .max(RETRY_BACKOFF_MAX)
    .default([...DEFAULT_EXECUTION_SETTINGS.retryBackoffMs]),
  qcAutofix: boundedCount(QC_AUTOFIX_MAX).default(DEFAULT_EXECUTION_SETTINGS.qcAutofix),
});

export const DiagnosticsSettingsSchema = z.strictObject({
  logLevel: z.enum(LOG_LEVELS).default(DEFAULT_DIAGNOSTICS_SETTINGS.logLevel),
});

export const PermissionSettingsSchema = z.strictObject({
  sandboxMode: z.enum(SANDBOX_MODES).default(DEFAULT_PERMISSION_SETTINGS.sandboxMode),
  approvalPolicy: z.enum(APPROVAL_POLICIES).default(DEFAULT_PERMISSION_SETTINGS.approvalPolicy),
  defaultRole: z.enum(DEFAULT_ROLES).default(DEFAULT_PERMISSION_SETTINGS.defaultRole),
});

export const ModelOptionsSchema = z.strictObject({
  contextWindow: z.number().int().min(CONTEXT_WINDOW_MIN).max(CONTEXT_WINDOW_MAX).optional(),
  maxTokens: z.number().int().min(MAX_TOKENS_MIN).max(MAX_TOKENS_MAX).optional(),
  reasoningEfforts: z.array(ThinkingLevelSchema).max(THINKING_LEVELS.length).optional(),
  defaultReasoningEffort: ThinkingLevelSchema.optional(),
});

/** 连接形状：key 允许为空（导出脱敏后就是空串），其它高级字段可选。 */
export const ModelConnectionSchema = z.strictObject({
  name: z.string().min(1).max(64),
  baseUrl: z.string(),
  apiKey: z.string().default(""),
  models: z.array(z.string()).default([]),
  modelOptions: z.record(z.string(), ModelOptionsSchema).optional(),
  timeoutMs: z.number().int().min(CONNECTION_TIMEOUT_MIN_MS).max(CONNECTION_TIMEOUT_MAX_MS).optional(),
  maxRetries: boundedCount(CONNECTION_RETRIES_MAX).optional(),
});

export const DemoToolFaultsSchema = z.record(z.string(), z.enum(["timeout", "error", "invalid_output"]));

export const DemoSettingsSchema = z.strictObject({
  toolFaults: DemoToolFaultsSchema.default({}),
  corruptTraceability: z.boolean().default(false),
  crashOnNextStep: z.boolean().default(false),
});

/** PATCH 的四个分区：都按“部分字段”校验。 */
export const UiSettingsPatchSchema = UiSettingsSchema.partial();
export const ExecutionSettingsPatchSchema = ExecutionSettingsSchema.partial();
export const DiagnosticsSettingsPatchSchema = DiagnosticsSettingsSchema.partial();
export const PermissionSettingsPatchSchema = PermissionSettingsSchema.partial();

/** PATCH /api/settings/app 的请求体：必须带 revision 与服务端当前值比对。 */
export const AppSettingsPatchSchema = z.strictObject({
  revision: z.number().int().min(0),
  ui: UiSettingsPatchSchema.optional(),
  execution: ExecutionSettingsPatchSchema.optional(),
  diagnostics: DiagnosticsSettingsPatchSchema.optional(),
  permissions: PermissionSettingsPatchSchema.optional(),
});
export type AppSettingsPatch = z.infer<typeof AppSettingsPatchSchema>;
export type UiSettingsPatch = z.infer<typeof UiSettingsPatchSchema>;
export type ExecutionSettingsPatch = z.infer<typeof ExecutionSettingsPatchSchema>;
export type DiagnosticsSettingsPatch = z.infer<typeof DiagnosticsSettingsPatchSchema>;
export type PermissionSettingsPatch = z.infer<typeof PermissionSettingsPatchSchema>;

/** PUT /api/settings/role 的请求体。 */
export const SetRoleRequestSchema = z.strictObject({
  role: z.enum(DEFAULT_ROLES),
  revision: z.number().int().min(0).optional(),
});
export type SetRoleRequest = z.infer<typeof SetRoleRequestSchema>;

/** 把 zod 校验错误收成一条中文提示（接口 400 用）。 */
export function describeSettingsProblems(error: z.ZodError): string {
  const first = error.issues[0];
  if (!first) return "设置值不合法";
  const path = first.path.length > 0 ? `${first.path.join(".")}：` : "";
  return `设置值不合法（${path}${first.message}）`;
}
