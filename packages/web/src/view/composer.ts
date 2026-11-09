// 输入区的状态：占位语、是否禁用、只读提示；新对话页的示例起点。纯函数。

export interface ComposerInput {
  /** 由旧任务推导出的只读会话 */
  readOnly: boolean;
  /** 正在说话：再发的话排进这一轮 */
  streaming: boolean;
  /** 会话已经绑定任务 */
  hasTask: boolean;
  /** 工作区目录里有文件可以用 @ 选 */
  canMention: boolean;
}

export interface ComposerView {
  placeholder: string;
  disabled: boolean;
  /** 输入框上方的明确提示（只读时） */
  notice?: string;
  /** 右下角显示“停止” */
  showStop: boolean;
}

export const READ_ONLY_NOTICE = "这是旧任务的只读记录：可以查看和归档，不能发消息、换模型。要继续工作请开新对话。";

export function composerView(input: ComposerInput): ComposerView {
  if (input.readOnly) {
    return { placeholder: "只读记录，不能发消息", disabled: true, notice: READ_ONLY_NOTICE, showStop: false };
  }
  const base = input.hasTask ? "问问这个任务…" : "说说要做什么，或问问员工…";
  const placeholder = input.streaming ? "接着说…（会在下一步之前送达）" : input.canMention ? `${base}（输入 @ 选目录里的文件）` : base;
  return { placeholder, disabled: false, showStop: input.streaming };
}

export interface Starter {
  key: "weekly" | "sample" | "ask";
  title: string;
  description: string;
  /** fill：把话填进输入框，人改一改再发；sample：载入公开样例（预填一张员工表单） */
  action: "fill" | "sample";
  text?: string;
}

/** 新对话页的示例起点：点一下就有个开头，不用从空白输入框开始想。 */
export const STARTERS: readonly Starter[] = [
  { key: "weekly", title: "帮我出第 44 周的周报", description: "说一句话，我来找合适的员工接手，再帮你备齐材料", action: "fill", text: "帮我出第 44 周的周报" },
  { key: "sample", title: "载入公开样例", description: "把第 44 周的样例数据预填成员工表单，确认开工就能看到完整流程", action: "sample" },
  { key: "ask", title: "问问你能做什么", description: "先了解有哪些员工、各自负责什么", action: "fill", text: "你能帮我做哪些事？" },
];

/** 输入区下方的快捷键提示：按“回车行为”偏好说清哪个键发送、哪个键换行。 */
export function enterHint(behavior: "send" | "newline"): string {
  return behavior === "send" ? "Enter 发送 · Shift+Enter 换行" : "Ctrl+Enter 发送 · Enter 换行";
}
