#!/usr/bin/env bun
// de 命令行：
//   de run-case <case.json> [--scripted <script.json>] [--out <dir>]
//   de replay <workspace> <taskId>
//   de mcp-server
import { parseArgs } from "node:util";

const USAGE = `用法：
  de run-case <case.json> [--scripted <script.json>] [--out <dir>]   按用例驱动引擎并保存结果
  de replay <workspace> <taskId>                                     按时间回放任务事件
  de mcp-server                                                      以 stdio 方式运行工具服务器
`;

async function main(argv: string[]): Promise<number> {
  const [command, ...rest] = argv;
  switch (command) {
    case "replay": {
      const { positionals } = parseArgs({ args: rest, allowPositionals: true });
      const [workspace, taskId] = positionals;
      if (!workspace || !taskId) return usage("replay 需要 <workspace> 和 <taskId>");
      const { replayLines } = await import("./replay");
      for (const line of replayLines(workspace, taskId)) console.log(line);
      return 0;
    }
    case "run-case": {
      const { values, positionals } = parseArgs({
        args: rest,
        allowPositionals: true,
        options: { scripted: { type: "string" }, out: { type: "string" } },
      });
      const [caseFile] = positionals;
      if (!caseFile) return usage("run-case 需要 <case.json>");
      const { runCaseCommand } = await import("./run-case-command");
      return runCaseCommand({ caseFile, scriptedFile: values.scripted, outDir: values.out });
    }
    case "mcp-server": {
      const { runStdioServer } = await import("@de/tools");
      await runStdioServer();
      return 0;
    }
    default:
      return usage(command ? `未知命令：${command}` : undefined);
  }
}

function usage(problem?: string): number {
  if (problem) console.error(problem);
  console.error(USAGE);
  return problem ? 2 : 0;
}

try {
  const code = await main(process.argv.slice(2));
  // mcp-server 会一直运行；其余命令跑完即退出
  if (process.argv[2] !== "mcp-server") process.exit(code);
} catch (err) {
  console.error(err instanceof Error ? err.message : String(err));
  process.exit(1);
}
