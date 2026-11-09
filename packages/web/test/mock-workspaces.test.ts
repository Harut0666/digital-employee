import { describe, expect, test } from "bun:test";
import { createMockApi } from "../src/api/mock";
import type { MockWebApi } from "../src/api/types";
import { createMockWorkspaceApi } from "../src/api/workspaces";

describe("模拟模式的工作区接口", () => {
  const api = createMockWorkspaceApi(createMockApi(() => "initiator") as unknown as MockWebApi);

  test("选目录返回一个示例路径，名称可以用目录名补全", async () => {
    const res = await api.pickFolder();
    expect(res.ok).toBe(true);
    if (res.ok && "path" in res) expect(res.path.length).toBeGreaterThan(0);
    else throw new Error("模拟模式的选目录应当返回路径");
  });

  test("选出来的路径可以直接新建工作区", async () => {
    const picked = await api.pickFolder();
    if (!picked.ok || !("path" in picked)) throw new Error("没有选中目录");
    const created = await api.create("样例目录", picked.path);
    expect(created.ok).toBe(true);
  });
});
