import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { cleanupAll, createHarness } from "./helpers/harness";

afterEach(cleanupAll);

describe("create：员工 id 校验", () => {
  test("含路径穿越或分隔符的 id 在做任何文件操作前被拒绝", async () => {
    const h = await createHarness();
    // 在 employeesDir 的上一级放一个合法的员工目录：穿越成功就会被复制
    const outside = join(dirname(h.employeesDir), "outside");
    mkdirSync(outside, { recursive: true });
    writeFileSync(join(outside, "workflow.yaml"), "name: x\n");

    const bad = ["../outside", "..", ".", "mini/..", "a/b", "a\\b", outside, "MINI", "mini ", ""];
    for (const employee of bad) {
      await expect(h.engine.create({ employee, form: {}, attachments: [], actor: "initiator" })).rejects.toThrow(
        /员工 id|员工配置/,
      );
    }
    const tasksDir = join(h.workspaceDir, "tasks");
    expect(existsSync(tasksDir) ? readdirSync(tasksDir) : []).toEqual([]);
  });

  test("目录存在但没有 workflow.yaml → 明确的错误", async () => {
    const h = await createHarness();
    mkdirSync(join(h.employeesDir, "empty-one"), { recursive: true });
    await expect(
      h.engine.create({ employee: "empty-one", form: {}, attachments: [], actor: "initiator" }),
    ).rejects.toThrow(/workflow\.yaml/);
  });

  test("合法 id 照常创建", async () => {
    const h = await createHarness();
    expect((await h.create()).employee).toBe("mini");
  });
});
