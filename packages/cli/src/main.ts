#!/usr/bin/env bun
// de 命令行入口；实现在 commands.ts（打包后的 exe 也调用同一份 runCli）。
import { runCli } from "./commands";

const code = await runCli(process.argv.slice(2));
// mcp-server 会一直运行；其余命令跑完即退出
if (process.argv[2] !== "mcp-server") process.exit(code);
