// 开发时的入口：bun packages/tools/src/stdio.ts
// 这里用 Bun 文件导入拿到 wasm 的真实路径，再注入工具服务器（通用图表模块不再静态导入 wasm）。
import resvgWasm from "@resvg/resvg-wasm/index_bg.wasm" with { type: "file" };
import { runStdioServer } from "./index";

await runStdioServer({ resvgWasm });
