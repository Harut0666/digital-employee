import { describe, expect, test } from "bun:test";
import { runApp, type AppDeps } from "../src/dispatch";

function fakeDeps(): AppDeps & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    serve: async () => {
      calls.push("serve");
    },
    mcpServer: async () => {
      calls.push("mcp-server");
    },
    initWorkspace: async () => {
      calls.push("init");
      return "/tmp/ws";
    },
    cli: async (argv) => {
      calls.push(`cli:${argv.join(" ")}`);
      return 0;
    },
  };
}

describe("runApp 子命令分派", () => {
  test("无参数时启动服务端（不跑 CLI）", async () => {
    const deps = fakeDeps();
    expect(await runApp([], deps)).toBe(0);
    expect(deps.calls).toEqual(["serve"]);
  });

  test("mcp-server 启动工具服务器（工具子进程用这个入口）", async () => {
    const deps = fakeDeps();
    expect(await runApp(["mcp-server"], deps)).toBe(0);
    expect(deps.calls).toEqual(["mcp-server"]);
  });

  test("init 只初始化工作区并打印路径", async () => {
    const deps = fakeDeps();
    expect(await runApp(["init"], deps)).toBe(0);
    expect(deps.calls).toEqual(["init"]);
  });

  test("run-case / run-cases 参数原样转交给 CLI，并沿用它的退出码", async () => {
    const deps = fakeDeps();
    deps.cli = async (argv) => {
      deps.calls.push(`cli:${argv.join(" ")}`);
      return 1;
    };
    expect(await runApp(["run-case", "samples/cases/A-01.json", "--scripted", "s.json"], deps)).toBe(1);
    expect(deps.calls).toEqual(["cli:run-case samples/cases/A-01.json --scripted s.json"]);
  });

  test("其他子命令（check / fixtures / replay）也交给 CLI", async () => {
    const deps = fakeDeps();
    expect(await runApp(["check", "out/A-01", "samples/cases/A-01.json"], deps)).toBe(0);
    expect(deps.calls).toEqual(["cli:check out/A-01 samples/cases/A-01.json"]);
  });
});
