// 模拟数据模式的知识库：internal/knowledge 的手写简化版。只接受已交付任务，描述不能为空，只有发起人能入库和删除。
// 模拟任务没有真实文件，正文用一段说明代替。

import type { KnowledgeApi } from "./knowledge";
import type { KnowledgeDetail, KnowledgeEntry, UiRole, WebApi } from "./types";

export interface MockKnowledge extends KnowledgeApi {
  /** 某个任务最近一次入库的条目 ID */
  filedFrom(taskId: string): string | undefined;
}

export function createMockKnowledge(getRole: () => UiRole, tasks: Pick<WebApi, "getTask">): MockKnowledge {
  const entries: KnowledgeDetail[] = [];
  let counter = 0;
  const fail = (error: string, message: string) => ({ ok: false as const, error, message });
  const strip = ({ text: _text, ...entry }: KnowledgeDetail): KnowledgeEntry => entry;

  return {
    filedFrom: (taskId) => [...entries].reverse().find((e) => e.sourceTask === taskId)?.id,
    listKnowledge: async () => entries.map(strip),
    async readKnowledge(id) {
      const found = entries.find((e) => e.id === id);
      if (!found) throw new Error(`知识库里没有这个条目：${id}`);
      return found;
    },
    async fileKnowledge(filing) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人能把交付物放进知识库");
      const task = await tasks.getTask(filing.taskId);
      if (!task) return fail("not_found", `任务不存在：${filing.taskId}`);
      if (task.state !== "delivered") return fail("not_delivered", "任务还没有交付，它的文件不能放进知识库。");
      const description = filing.description.trim();
      if (!description) return fail("invalid_input", "请写一句描述：员工先看描述再决定要不要读正文。");
      const entry: KnowledgeDetail = {
        id: `kb-mock-${++counter}`,
        title: filing.title?.trim() || `交付物_${task.id}`,
        description,
        tags: [...new Set((filing.tags ?? []).map((t) => t.trim()).filter(Boolean))],
        sourceTask: task.id,
        sourceEmployee: task.employee,
        createdAt: new Date().toISOString(),
        fileName: "模拟交付物.docx",
        text: "模拟数据模式没有真实的交付物文件，这里是正文的占位说明。",
      };
      entries.push(entry);
      return { ok: true, entry: strip(entry) };
    },
    async deleteKnowledge(id) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人能删除知识库条目");
      const index = entries.findIndex((e) => e.id === id);
      if (index < 0) return fail("not_found", `知识库里没有这个条目：${id}`);
      entries.splice(index, 1);
      return { ok: true };
    },
  };
}
