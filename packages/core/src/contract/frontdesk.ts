// 前台 Agent 契约（docs/design.md §13）：人设写在 frontdesk.md，只读工具可以勾选。
//
// frontdesk.md 末尾用一个人可读的段落列出开放给它的只读工具：
//
//   ## 开放给你的只读工具
//
//   - query_task：只读查询任务
//
// 段落缺失或没有勾选时按下面列出的默认值处理；保存时由配置页写回。填表单草稿、提议卡片是固定能力，
// 不在只读工具里（它们不推进状态，见 ADR-0001）。工具表与 Go 的 contract.FrontdeskReadonlyTools 一致。

export const FRONTDESK_READONLY_TOOLS = [
  { name: "query_task", label: "只读查询任务" },
  { name: "kb_search", label: "搜索知识库索引" },
  { name: "kb_read", label: "读取知识库正文" },
] as const;

export type FrontdeskReadonlyToolName = (typeof FRONTDESK_READONLY_TOOLS)[number]["name"];

export const FRONTDESK_READONLY_TOOL_NAMES = FRONTDESK_READONLY_TOOLS.map((t) => t.name);

/** 段落缺失或没有勾选任何工具时开放的只读工具。知识库要人明确勾选才开放（ADR-0005）。 */
export const FRONTDESK_DEFAULT_TOOLS: readonly FrontdeskReadonlyToolName[] = ["query_task"];

export function isFrontdeskReadonlyTool(value: unknown): value is FrontdeskReadonlyToolName {
  return typeof value === "string" && FRONTDESK_READONLY_TOOL_NAMES.includes(value as FrontdeskReadonlyToolName);
}

/** 前台 Agent 固定具备的工具（运行员工），不能通过配置关闭。 */
export const FRONTDESK_FIXED_TOOLS = ["run_employee"] as const;
