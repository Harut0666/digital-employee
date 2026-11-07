// 知识库接口（Go 后端 internal/server/knowledge.go）。TS 后端没有这些路由，直接用 fetch。
// 入库、删除是人的操作：业务错误也作为结果返回，界面直接提示；读取出错时抛异常。

import type { FetchLike } from "./sessions";
import type { KnowledgeDetail, KnowledgeEntry, KnowledgeFiling, KnowledgeResult, UiRole } from "./types";

export type DeleteResult = { ok: true } | { ok: false; error: string; message: string };

export interface KnowledgeApi {
  listKnowledge(): Promise<KnowledgeEntry[]>;
  readKnowledge(id: string): Promise<KnowledgeDetail>;
  /** 入库：只有发起人能做，描述不能为空 */
  fileKnowledge(filing: KnowledgeFiling): Promise<KnowledgeResult>;
  deleteKnowledge(id: string): Promise<DeleteResult>;
}

type Json = Record<string, unknown>;

const failureOf = (body: unknown, status: number) => {
  const value = (typeof body === "object" && body !== null ? body : {}) as Json;
  return {
    ok: false as const,
    error: typeof value.error === "string" ? value.error : "internal",
    message: typeof value.message === "string" ? value.message : `请求失败（${status}）`,
  };
};

export function createKnowledgeApi(origin: string, getRole: () => UiRole, fetchImpl: FetchLike = (input, init) => fetch(input, init)): KnowledgeApi {
  const url = (id?: string) => `${origin}/api/knowledge${id === undefined ? "" : `/${encodeURIComponent(id)}`}`;

  async function send(target: string, init?: RequestInit): Promise<{ status: number; ok: boolean; body: unknown }> {
    const res = await fetchImpl(target, init);
    return { status: res.status, ok: res.ok, body: await res.json().catch(() => undefined) };
  }

  async function read<T>(target: string): Promise<T> {
    const res = await send(target);
    if (!res.ok) throw new Error(failureOf(res.body, res.status).message);
    return res.body as T;
  }

  return {
    listKnowledge: () => read<KnowledgeEntry[]>(url()),
    readKnowledge: (id) => read<KnowledgeDetail>(url(id)),
    async fileKnowledge(filing) {
      const res = await send(url(), {
        method: "POST",
        headers: { "x-role": getRole(), "content-type": "application/json" },
        body: JSON.stringify(filing),
      });
      return res.ok ? { ok: true, entry: res.body as KnowledgeEntry } : failureOf(res.body, res.status);
    },
    async deleteKnowledge(id) {
      const res = await send(url(id), { method: "DELETE", headers: { "x-role": getRole() } });
      return res.ok ? { ok: true } : failureOf(res.body, res.status);
    },
  };
}
