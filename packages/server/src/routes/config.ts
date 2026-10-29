// 员工配置页后端（T11 填充）：配置读写、校验、版本与回滚。
import { Elysia } from "elysia";
import type { RouteDeps } from "./deps";

export function createConfigRoutes(_deps: RouteDeps) {
  return new Elysia({ prefix: "/api/employees" }).get("/:id/config", ({ params }) => ({ ok: true as const, stub: "config", id: params.id }));
}
