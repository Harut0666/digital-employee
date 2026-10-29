// 表单字段用途契约（docs/design.md §4.1）：form.schema.json 的字段可以带 x-purpose。
//
//   x-purpose: writing_requirement   字段的标题和取值作为“发起人的写作要求”注入每个判断步骤
//   x-purpose: param:<参数名>        字段绑定到程序已经支持的参数，取值必须是下面列出的名字
//
// 新增 param 取值等于新增程序逻辑：只有真正接在引擎或工具上的参数才能出现在 FORM_PARAMS 里。

export const FORM_PURPOSE_WRITING_REQUIREMENT = "writing_requirement";
export const FORM_PURPOSE_PARAM_PREFIX = "param:";

/**
 * 程序已支持的表单参数（x-purpose: "param:<名字>"）。
 * 每一项都对应程序里已有的读取点，业务人员在界面上绑定它不需要开发介入：
 *   period              统计周期：workflow.yaml 的 $form.period、准入校验与指标计算
 *   allowMissingMinutes 无纪要继续：引擎命令 continue_without_minutes 置位，准入校验读取
 *   allowPartialPeriod  按已有数据继续：引擎命令 continue_with_partial 置位，准入校验读取
 */
export const FORM_PARAMS = {
  period: "统计周期",
  allowMissingMinutes: "无纪要继续",
  allowPartialPeriod: "按已有数据继续",
} as const;
export type FormParamName = keyof typeof FORM_PARAMS;
export const FORM_PARAM_NAMES = Object.keys(FORM_PARAMS) as FormParamName[];

export function isFormParamName(value: unknown): value is FormParamName {
  return typeof value === "string" && Object.prototype.hasOwnProperty.call(FORM_PARAMS, value);
}

export type FormPurpose = { kind: "writing_requirement" } | { kind: "param"; param: FormParamName };

export type FormPurposeResult = { ok: true; purpose?: FormPurpose } | { ok: false; message: string };

const SUPPORTED_PURPOSES = `写作要求用 ${FORM_PURPOSE_WRITING_REQUIREMENT}，绑定参数用 ${FORM_PURPOSE_PARAM_PREFIX}<参数名>；已支持的参数：${FORM_PARAM_NAMES.join("、")}`;

/** 解析一个 x-purpose 取值。缺省（undefined / null / 空串）视为没有用途。 */
export function parseFormPurpose(value: unknown): FormPurposeResult {
  if (value === undefined || value === null || value === "") return { ok: true };
  if (typeof value !== "string") return { ok: false, message: `x-purpose 必须是字符串（${SUPPORTED_PURPOSES}）` };
  if (value === FORM_PURPOSE_WRITING_REQUIREMENT) return { ok: true, purpose: { kind: "writing_requirement" } };
  if (value.startsWith(FORM_PURPOSE_PARAM_PREFIX)) {
    const param = value.slice(FORM_PURPOSE_PARAM_PREFIX.length);
    if (!isFormParamName(param)) {
      return { ok: false, message: `x-purpose 绑定了程序不支持的参数 "${param}"（${SUPPORTED_PURPOSES}）` };
    }
    return { ok: true, purpose: { kind: "param", param } };
  }
  return { ok: false, message: `无法识别的 x-purpose "${value}"（${SUPPORTED_PURPOSES}）` };
}

export function formatFormPurpose(purpose: FormPurpose | undefined): string | undefined {
  if (!purpose) return undefined;
  return purpose.kind === "writing_requirement" ? FORM_PURPOSE_WRITING_REQUIREMENT : `${FORM_PURPOSE_PARAM_PREFIX}${purpose.param}`;
}

/** 表单里的写作要求字段：标题 + 取值注入判断步骤上下文。 */
export interface WritingRequirement {
  /** 字段路径，嵌套用点号，数组元素用 [i] */
  field: string;
  title: string;
  value: unknown;
  /** 给模型看的单行文本，如“重点关注区域：华东” */
  text: string;
}

