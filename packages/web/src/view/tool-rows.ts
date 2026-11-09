// 对话里 Agent 的工具调用行（DSH 风格的紧凑行）：图标 + 动词标题 + 摘要，状态用样式表达。纯函数。
//
// 后端会陆续加工具（glob、grep、shell、todo_write、ask_user_question…）：工具名到“图标 + 动词”的映射写成下面的表，
// 加一个工具就加一行；表里没有的工具用通用图标和原名显示，不报错。

import type { AgentToolView } from "../api/types";

/** 图标的键；组件里按键取 antd 图标 */
export type ToolIconKey = "file" | "edit" | "folder" | "search" | "terminal" | "todo" | "question" | "team" | "tool";

export interface ToolStyle {
  verb: string;
  icon: ToolIconKey;
}

/** 工具名 → 图标和动词。要支持新工具，在这里加一行。 */
export const TOOL_TABLE: Record<string, ToolStyle> = {
  read_file: { verb: "读取文件", icon: "file" },
  write_file: { verb: "写入文件", icon: "edit" },
  edit_file: { verb: "修改文件", icon: "edit" },
  list_files: { verb: "列出文件", icon: "folder" },
  glob: { verb: "查找文件", icon: "search" },
  grep: { verb: "搜索", icon: "search" },
  shell: { verb: "运行命令", icon: "terminal" },
  bash: { verb: "运行命令", icon: "terminal" },
  run_shell: { verb: "运行命令", icon: "terminal" },
  todo_write: { verb: "更新待办", icon: "todo" },
  ask_user_question: { verb: "提问", icon: "question" },
  use_folder_file: { verb: "使用目录文件", icon: "file" },
  run_employee: { verb: "交给员工", icon: "team" },
};

const GENERIC: ToolStyle["icon"] = "tool";

export function toolStyle(name: string): ToolStyle {
  if (!name) return { verb: "工具", icon: GENERIC };
  return TOOL_TABLE[name] ?? { verb: name, icon: GENERIC };
}

const record = (value: unknown): Record<string, unknown> | undefined =>
  typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;

const firstString = (source: Record<string, unknown> | undefined, keys: readonly string[]): string => {
  for (const key of keys) {
    const value = source?.[key];
    if (typeof value === "string" && value.trim() !== "") return value;
  }
  return "";
};

const SUMMARY_LIMIT = 120;
const PATH_KEYS = ["path", "file_path", "file", "filename"] as const;
const COMMAND_KEYS = ["description", "command", "cmd"] as const;
const SEARCH_KEYS = ["pattern", "query", "q", "glob"] as const;

/** 路径显示成相对路径：反斜杠换斜杠、去掉 ./；绝对路径只留末尾三段 */
function displayPath(raw: string): string {
  const path = raw.replace(/\\/g, "/").replace(/^\.\//, "");
  if (/^([a-zA-Z]:)?\//.test(path)) {
    const parts = path.split("/").filter((part) => part !== "" && !/^[a-zA-Z]:$/.test(part));
    return parts.length > 3 ? `…/${parts.slice(-3).join("/")}` : parts.join("/");
  }
  return path;
}

const clip = (text: string, limit = SUMMARY_LIMIT) => {
  const line = text.split("\n")[0] ?? "";
  return line.length > limit ? `${line.slice(0, limit - 1)}…` : line;
};

/** 一行里标题后面的摘要：相对路径 / 命令说明 / 搜索词；认不出就留空 */
export function toolSummary(call: AgentToolView): string {
  const args = record(call.args);
  const path = firstString(args, PATH_KEYS);
  if (path) return clip(displayPath(path));
  const search = firstString(args, SEARCH_KEYS);
  if (search) return clip(search);
  const command = firstString(args, COMMAND_KEYS);
  if (command) return clip(command);
  return "";
}

const countLines = (text: string) => (text === "" ? 0 : text.replace(/\n$/, "").split("\n").length);

/** 写入类工具成功后的行数变化：write_file 全算新增，edit_file 数差异里的 +/- 行；其它工具没有 */
export function lineDelta(call: AgentToolView): { added: number; removed: number } | undefined {
  if (!call.ended || call.isErr) return undefined;
  const args = record(call.args);
  if (call.tool === "write_file") {
    const content = args?.content;
    return typeof content === "string" ? { added: countLines(content), removed: 0 } : undefined;
  }
  if (call.tool === "edit_file") {
    const diff = record(call.result)?.diff;
    if (typeof diff === "string") {
      let added = 0;
      let removed = 0;
      for (const line of diff.split("\n")) {
        if (line.startsWith("+++") || line.startsWith("---")) continue;
        if (line.startsWith("+")) added++;
        else if (line.startsWith("-")) removed++;
      }
      return { added, removed };
    }
    const before = firstString(args, ["old_string", "old_text", "old"]);
    const after = firstString(args, ["new_string", "new_text", "new"]);
    if (before || after) return { added: countLines(after), removed: countLines(before) };
  }
  return undefined;
}

export type ToolStatus = "pending" | "running" | "done" | "error";

/** 状态：准备（参数还没到）/ 运行中 / 完成 / 出错。准备和运行中整行闪烁，出错时摘要变错误色。 */
export function toolStatus(call: AgentToolView): ToolStatus {
  if (!call.ended) return call.args && Object.keys(call.args).length > 0 ? "running" : "pending";
  return call.isErr ? "error" : "done";
}

/** 出错时在行上显示的结果首行 */
export function errorLine(call: AgentToolView): string {
  if (!call.isErr) return "";
  if (call.reason) return clip(call.reason);
  const result = call.result;
  if (typeof result === "string") return clip(result);
  const body = record(result);
  const text = firstString(body, ["message", "error", "reason"]);
  return text ? clip(text) : "";
}

/** 展开体里的“输入”：参数 JSON */
export function formatInput(call: AgentToolView): string {
  const args = call.args;
  if (!args || Object.keys(args).length === 0) return "（没有参数）";
  return JSON.stringify(args, null, 2);
}

export const OUTPUT_LIMIT = 2000;

/** 展开体里的“输出”：结果文本；过长截断（full 是完整内容，点“展开全部”时用） */
export function formatOutput(call: AgentToolView, limit: number = OUTPUT_LIMIT): { text: string; truncated: boolean; full: string } {
  let full = "";
  if (call.ended) {
    if (call.isErr && call.reason) full = call.reason;
    else if (call.result !== undefined && call.result !== null) full = typeof call.result === "string" ? call.result : JSON.stringify(call.result, null, 2);
    else if (call.reason) full = call.reason;
  }
  if (full.length <= limit) return { text: full, truncated: false, full };
  return { text: `${full.slice(0, limit)}\n…`, truncated: true, full };
}

/** 用时：不足一秒按一秒；一分钟以上写“M 分 SS 秒” */
export function formatDuration(ms: number): string {
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${String(seconds % 60).padStart(2, "0")} 秒`;
}

/** 一轮结束后折叠的一行：已完成 · 用时 X · N 步（有出错的写出来） */
export function turnSummary(calls: readonly AgentToolView[], startedAt: number | undefined, endedAt: number | undefined): string {
  const failed = calls.filter((call) => call.ended && call.isErr).length;
  const parts = ["已完成"];
  if (startedAt !== undefined && endedAt !== undefined && endedAt >= startedAt) parts.push(`用时 ${formatDuration(endedAt - startedAt)}`);
  parts.push(`${calls.length} 步${failed > 0 ? `，${failed} 步出错` : ""}`);
  return parts.join(" · ");
}
