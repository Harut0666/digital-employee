import { describe, expect, test } from "bun:test";
import { baseName, workspaceGroups } from "../src/view/workspace-picker";

const huadong = { id: "ws-1", name: "华东区", path: "D:\\de\\华东区" };
const huanan = { id: "ws-2", name: "华南区", path: "D:\\de\\华南区", missing: true, missingText: "目录缺失" };

describe("工作区分组（Spec 0006：工作区只是目录，没有切换）", () => {
  test("每个工作区一组，带目录；目录缺失的标出来", () => {
    const view = workspaceGroups({ readOnly: false, workspaces: [huadong, huanan] });
    expect(view.groups.map((g) => [g.name, g.missing, g.hint])).toEqual([
      ["华东区", false, "D:\\de\\华东区"],
      ["华南区", true, "目录缺失"],
    ]);
    expect(view.canManage).toBe(true);
    expect(view.empty).toBe(false);
  });

  test("单目录模式只读：说明原因", () => {
    const view = workspaceGroups({ readOnly: true, reason: "工作区由环境变量 DE_WORKSPACE 指定", workspaces: [{ id: "single", name: "演示", path: "D:\\demo\\演示" }] });
    expect(view.canManage).toBe(false);
    expect(view.hint).toBe("工作区由环境变量 DE_WORKSPACE 指定");
    expect(view.groups).toHaveLength(1);
  });

  test("列表还没读到或为空", () => {
    expect(workspaceGroups(undefined).groups).toEqual([]);
    expect(workspaceGroups(undefined).empty).toBe(false);
    const empty = workspaceGroups({ readOnly: false, workspaces: [] });
    expect(empty.empty).toBe(true);
    expect(empty.canManage).toBe(true);
  });

  test("目录名取最后一段", () => {
    expect(baseName("C:\\Users\\a\\Documents\\数字员工")).toBe("数字员工");
    expect(baseName("/home/a/ws")).toBe("ws");
  });
});
