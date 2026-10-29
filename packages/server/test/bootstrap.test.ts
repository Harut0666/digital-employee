import { describe, expect, test } from "bun:test";
import { toolServerCommand } from "../src/bootstrap";
describe("toolServerCommand", () => {
  test("under bun (development) it runs packages/tools/src/stdio.ts", () => {
    const c = toolServerCommand({ execPath: "C:\\bun\\bun.exe", toolsEntry: "D:\\repo\\packages\\tools\\src\\stdio.ts" });
    expect(c).toEqual({ command: "C:\\bun\\bun.exe", args: ["D:\\repo\\packages\\tools\\src\\stdio.ts"] });
  });
  test("a packaged exe starts itself with the mcp-server subcommand", () => {
    const c = toolServerCommand({ execPath: "C:\\app\\digital-employee.exe", toolsEntry: "ignored" });
    expect(c).toEqual({ command: "C:\\app\\digital-employee.exe", args: ["mcp-server"] });
  });
});
