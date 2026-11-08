// 诊断信息文本的拼装（接缝 D：纯函数，web 单测直接调用）。
//
// “复制诊断信息”复制的是这一份文本：版本 + 运行时 + 工作区路径 + 脱敏设置快照。
// 脱敏是最后一层保险：设置快照本来就只有 apiKeyLast4，但即使上游漏出明文 Key，
// 复制/导出的文本里也不能出现它（Spec 0004「密钥不落响应」）。
import type { AppSettingsView, DiagnosticsInfo } from "@de/core/contract";

/** JSON 里的 apiKey 字段值 → “已脱敏”。`"apiKeyLast4"` 因为多一个字符不会被命中。 */
const API_KEY_FIELD = /("apiKey"\s*:\s*)"[^"]*"/g;
/** 游离在文本里的密钥（例如错误信息里带出来的）。 */
const SK_TOKEN = /sk-[A-Za-z0-9_-]{4,}/g;

export const REDACTED = "已脱敏";

/** 把文本里可能出现的密钥抹掉；不影响其它内容。 */
export function redactSecrets(text: string): string {
  return text.replace(API_KEY_FIELD, `$1"${REDACTED}"`).replace(SK_TOKEN, "sk-****");
}

function connectionSummary(view: AppSettingsView): string {
  const count = view.connections.length;
  if (count === 0) return "模型连接：0 个";
  const names = view.connections.map((c) => `${c.name}（${c.hasKey ? `末 4 位 ${c.apiKeyLast4}` : "未配置 Key"}）`);
  // 会话模型取代了“默认模型”（Spec 0006）：最近一次在会话里选的模型
  const last = view.lastModel ? `${view.lastModel.connection} / ${view.lastModel.model}` : "还没选过";
  return `模型连接：${count} 个：${names.join("、")}；最近选的模型 ${last}`;
}

function executionSummary(view: AppSettingsView): string {
  const e = view.execution;
  return [
    `执行与预算：步骤超时 ${e.stepTimeoutMs} ms`,
    `模型重试 ${e.modelMaxRetries} 次`,
    `退避 ${e.retryBackoffMs.join(" / ")} ms`,
    `质检自动修正 ${e.qcAutofix} 次`,
  ].join(" · ");
}

/**
 * 组装“复制诊断信息”的纯文本。
 * 输入是后端 `GET /api/settings/diagnostics` 的返回；输出可直接进剪贴板。
 */
export function formatDiagnosticsText(info: DiagnosticsInfo): string {
  const { app, runtime, workspace, settings } = info;
  const lines = [
    "digital-employee 诊断信息",
    `生成时间：${info.generatedAt}`,
    `应用版本：${app.version}（设置文档 v${app.settingsVersion}）`,
    `运行时：Bun ${runtime.bun} · ${runtime.platform} · ${runtime.arch}`,
    `工作区：${workspace.workspaceDir}`,
    `员工目录：${workspace.employeesDir}`,
    `设置文件：${workspace.settingsFile}（${workspace.settingsFileExists ? "存在" : "未创建"}）`,
    connectionSummary(settings),
    executionSummary(settings),
    `日志级别：${settings.diagnostics.logLevel}`,
    "设置快照：",
    JSON.stringify(settings, null, 2),
  ];
  return redactSecrets(lines.join("\n"));
}
