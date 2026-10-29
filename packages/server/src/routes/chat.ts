// 前台 Agent 的对话接口（T12 填充，可砍范围的第一项）。
import { Elysia } from "elysia";
import type { RouteDeps } from "./deps";

export function createChatRoutes(_deps: RouteDeps) {
  return new Elysia({ prefix: "/api/chat" }).get("/:sessionId", ({ params }) => ({ ok: true as const, stub: "chat", sessionId: params.sessionId }));
}
