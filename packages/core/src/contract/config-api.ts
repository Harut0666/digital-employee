// 员工配置页的接口契约（docs/design.md §4.1）：服务端与前端共用。
// 配置文件是唯一的数据来源：GET 返回每个文件的原文与解析结果，PUT 先校验再写回；
// 每次保存前留一个版本快照，版本可以对比、回滚。

import type { EmployeeWorkflow, QcConfig } from "./employee-config";
import type { FormParamName } from "./form";
import type { QcTemplate } from "./qc";

/** SKILL.md 的三个可编辑区域（配置页“岗位说明”三个文本框）。 */
export interface SkillSections {
  /** “## 岗位职责”标题下的内容 */
  duties: string;
  /** 中间的全部章节（含各自的 ## 标题） */
  writing: string;
  /** “## 注意事项”标题下的全部内容 */
  cautions: string;
}

/** frontdesk.md 拆成的人设正文与只读工具。 */
export interface FrontdeskDoc {
  body: string;
  tools: string[];
}

export type ConfigFileKind = "yaml" | "json" | "markdown";

/** 一个配置文件在配置页里的样子：原文 + 解析结果。 */
export interface ConfigFileInfo {
  /** 相对员工目录的路径，如 workflow.yaml、prompts/write_report.md */
  path: string;
  kind: ConfigFileKind;
  content: string;
  /** YAML / JSON 的解析结果；markdown 没有 */
  parsed?: unknown;
  /** 解析失败时的说明：此时配置加载失败，只能走“查看源文件”修 */
  error?: string;
}

/** 配置页要读的加载结果（程序加载成功时才有）。 */
export interface LoadedEmployeeConfig {
  skill: string;
  frontdesk: string;
  workflow: EmployeeWorkflow;
  qc: QcConfig;
  formSchema: Record<string, unknown>;
  formUi?: Record<string, unknown>;
  prompts: Record<string, string>;
  outputSchemas: Record<string, Record<string, unknown>>;
}

/** 配置页需要的派生视图，由服务端按当前文件算好。 */
export interface EmployeeConfigViews {
  /** SKILL.md 的三个文本框 */
  skill: SkillSections;
  /** frontdesk.md 的人设与只读工具 */
  frontdesk: FrontdeskDoc;
  /** 表单字段可绑定的程序参数 */
  formParams: { name: FormParamName; label: string }[];
  /** 质检规则模板（6 种） */
  qcTemplates: readonly QcTemplate[];
}

export interface ConfigVersion {
  /** <时间戳>-<哈希短码>，同时是目录名 */
  id: string;
  createdAt: string;
  /** 整份配置的 sha256 */
  hash: string;
}

export interface ConfigDiffFile {
  path: string;
  /** 相对“版本 → 当前”：added 当前新增、removed 当前删掉、modified 内容改了 */
  status: "added" | "removed" | "modified";
  /** 行级差异（unified 风格，行首是空格 / - / +） */
  hunks: string[];
}

export interface ConfigDiff {
  version: ConfigVersion;
  hasChanges: boolean;
  files: ConfigDiffFile[];
}

export interface RestoreResult {
  /** 回滚前给当前配置留的快照 */
  snapshot: ConfigVersion;
  restoredFrom: string;
}

/** GET /api/employees/:id/config */
export interface EmployeeConfigResponse {
  id: string;
  name: string;
  dir: string;
  /** 配置能加载时的整份哈希，与任务事件里的 configHash 同源 */
  hash?: string;
  files: ConfigFileInfo[];
  /** 程序加载后的配置；加载失败时没有，错误见 error */
  config?: LoadedEmployeeConfig;
  views: EmployeeConfigViews;
  /** form.schema.json 里 x-purpose 的问题（正常为空） */
  formSchemaErrors: string[];
  versions: ConfigVersion[];
  /** 配置加载失败时的说明 */
  error?: string;
}

/** PUT /api/employees/:id/config/:file 的请求体：原文或结构化数据二选一。 */
export interface SaveConfigRequest {
  /** 源文件视图：用户编辑的原文 */
  content?: string;
  /** 表单视图：界面上的结构化对象 */
  data?: unknown;
}

/** PUT 成功后的响应 */
export interface SaveConfigResponse {
  ok: true;
  path: string;
  /** 保存前留的版本快照 */
  version: ConfigVersion;
  /** 保存后的整份配置哈希；配置文件本身没问题但整份配置加载失败时没有 */
  hash?: string;
}

/** POST .../versions/:version/restore 的响应 */
export interface RestoreConfigResponse {
  ok: true;
  /** 回滚前给当前配置留的快照（回滚本身也是一个新版本） */
  version: ConfigVersion;
  restoredFrom: string;
  versions: ConfigVersion[];
  /** 回滚后的整份配置哈希 */
  hash?: string;
}
