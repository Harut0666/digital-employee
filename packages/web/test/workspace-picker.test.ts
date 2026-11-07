import { describe, expect, test } from "bun:test";
import { switchOutcome, workspacePicker } from "../src/view/workspace-picker";

const huadong = { id: "ws-1", name: "华东区", path: "D:\\de\\华东区" };
const huanan = { id: "ws-2", name: "华南区", path: "D:\\de\\华南区" };

describe("左上角的工作区选择", () => {
  test("显示当前工作区名称，列表里标出当前项，可以切换和新建", () => {
    const view = workspacePicker({ current: "ws-2", readOnly: false, workspaces: [huadong, huanan] });
    expect(view.title).toBe("华南区");
    expect(view.tooltip).toBe("当前工作区：D:\\de\\华南区");
    expect(view.items.map((i) => [i.name, i.current])).toEqual([
      ["华东区", false],
      ["华南区", true],
    ]);
    expect(view.canSwitch).toBe(true);
    expect(view.canCreate).toBe(true);
  });

  test("环境变量指定工作区时只读：不能切换也不能新建，并说明原因", () => {
    const view = workspacePicker({ current: "env", readOnly: true, reason: "工作区由环境变量 DE_WORKSPACE 指定", workspaces: [{ id: "env", name: "演示", path: "D:\\demo\\演示" }] });
    expect(view.title).toBe("演示");
    expect(view.canSwitch).toBe(false);
    expect(view.canCreate).toBe(false);
    expect(view.hint).toBe("工作区由环境变量 DE_WORKSPACE 指定");
  });

  test("列表还没读到时先用工作区目录的最后一段", () => {
    const view = workspacePicker(undefined, "C:\\Users\\a\\AppData\\Roaming\\digital-employee");
    expect(view.title).toBe("digital-employee");
    expect(view.items).toEqual([]);
    expect(view.canSwitch).toBe(false);
  });
});

describe("切换结果", () => {
  test("切换生效时整页重新加载", () => {
    expect(switchOutcome({ ok: true, current: huanan, reload: true })).toEqual({ kind: "reload" });
  });

  test("选的就是当前工作区时什么也不做", () => {
    expect(switchOutcome({ ok: true, current: huanan, reload: false })).toEqual({ kind: "none" });
  });

  test("有任务在跑时被拒，提示后端给的原因", () => {
    expect(switchOutcome({ ok: false, error: "workspace_busy", message: "有任务正在准入校验、执行中或质检中", taskIds: ["t-1"] })).toEqual({
      kind: "error",
      message: "有任务正在准入校验、执行中或质检中",
    });
  });
});
