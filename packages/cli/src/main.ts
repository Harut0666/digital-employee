#!/usr/bin/env bun
// de 命令行：
//   de run-case <case.json> [--scripted <script.json>] [--out <dir>]
//   de run-cases <glob> [--scripted|--real] [--out <dir>]
//   de check <result> <expect>
//   de fixtures [--out <dir>] [--source <xlsx>]
//   de replay <workspace> <taskId>
//   de mcp-server
import { parseArgs } from "node:util";

const USAGE = `用法：
  de run-case <case.json> [--scripted <script.json>] [--out <dir>]   按用例驱动引擎并保存结果
  de run-cases <glob> [--scripted|--real] [--out <dir>]              批量跑用例并核对判定要点，退出码=未通过条数
  de check <result> <expect>                                        核对一次运行的交付物（不重跑）
  de fixtures [--out <dir>] [--source <xlsx>]                       从公开样例生成变异数据（确定性）
  de replay <workspace> <taskId>                                    按时间回放任务事件
  de mcp-server                                                     以 stdio 方式运行工具服务器
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
    case "run-cases": {
      const { values, positionals } = parseArgs({
        args: rest,
        allowPositionals: true,
        options: { scripted: { type: "boolean" }, real: { type: "boolean" }, out: { type: "string" } },
      });
      const [pattern] = positionals;
      if (!pattern) return usage("run-cases 需要 <glob>，例如 \"samples/cases/*.json\"");
      if (values.scripted && values.real) return usage("--scripted 与 --real 只能选一个");
      const { runCasesCommand } = await import("./run-cases-command");
      return runCasesCommand({
        pattern,
        mode: values.real ? "real" : "scripted",
        outDir: values.out,
      });
    }
    case "check": {
      const { positionals } = parseArgs({ args: rest, allowPositionals: true });
      const [resultDir, expectFile] = positionals;
      if (!resultDir || !expectFile) return usage("check 需要 <result> 和 <expect>");
      const { checkCommand } = await import("./check-command");
      return checkCommand({ resultDir, expectFile });
    }
    case "fixtures": {
      const { values } = parseArgs({
        args: rest,
        allowPositionals: true,
        options: { out: { type: "string" }, source: { type: "string" } },
      });
      const { fixturesCommand } = await import("./fixtures-command");
      return fixturesCommand({ outDir: values.out, sourcePath: values.source });
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
