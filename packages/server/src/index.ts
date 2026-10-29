// T3 实现：Elysia 服务。导出 App 类型供前端 Eden 使用。
import { Elysia } from "elysia";
import type { TaskEngine } from "@de/core";

export interface ServerOptions {
  engine: TaskEngine;
  employeesDir: string;
  /** 前端构建产物目录；不传则不托管静态资源 */
  webDistDir?: string;
}

export function createApp(_options: ServerOptions) {
  return new Elysia();
}

export type App = ReturnType<typeof createApp>;
