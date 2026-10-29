// 设置页后端（T10 填充）：模型连接、演示与调试、工作区。
import { Elysia } from "elysia";
import type { RouteDeps } from "./deps";

export function createSettingsRoutes(_deps: RouteDeps) {
  return new Elysia({ prefix: "/api/settings" }).get("/", () => ({ ok: true as const, stub: "settings" }));
}
