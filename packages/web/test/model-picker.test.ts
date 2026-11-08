// T37 拉取模型：拉取结果与已有模型列表合并成勾选列表，勾选结果写回连接的模型列表。
import { describe, expect, test } from "bun:test";
import {
  mergeFetchedModels,
  modelCheckOptions,
  selectedModels,
  toggleModelSelection,
} from "../src/pages/settings/model-picker";

describe("拉取结果与已有模型列表合并", () => {
  test("新拉取、已在列表、已删除的模型分开：只有已在列表的默认勾上", () => {
    const merged = mergeFetchedModels(["deepseek-chat", "deepseek-reasoner", "qwen-plus"], ["deepseek-chat"], ["gpt-4o"]);
    expect(merged.map((m) => [m.id, m.inList, m.checked, m.retired])).toEqual([
      ["deepseek-chat", true, true, false],
      ["deepseek-reasoner", false, false, false],
      ["qwen-plus", false, false, false],
      ["gpt-4o", false, false, true],
    ]);
  });

  test("顺序：已在列表的按原列表顺序在前，其余按拉取顺序，已删除的排最后", () => {
    const merged = mergeFetchedModels(["a", "b", "c", "d"], ["c", "b"], ["z", "y"]);
    expect(merged.map((m) => m.id)).toEqual(["c", "b", "a", "d", "z", "y"]);
  });
  test("重复 ID 与空白 ID 去掉；列表为空时全部未勾选", () => {
    const merged = mergeFetchedModels(["a", " a ", "", "b", "b"], [], []);
    expect(merged.map((m) => [m.id, m.checked])).toEqual([
      ["a", false],
      ["b", false],
    ]);
  });

  test("已有列表里的模型这次没拉到：保留原条目并勾上，不会被悄悄删掉", () => {
    const merged = mergeFetchedModels(["a"], ["b", "a"], ["c"]);
    expect(merged.map((m) => [m.id, m.inList, m.checked, m.retired])).toEqual([
      ["b", true, true, true],
      ["a", true, true, false],
      ["c", false, false, true],
    ]);
  });

  test("两边都为空：空列表", () => {
    expect(mergeFetchedModels([], [], [])).toEqual([]);
  });
});

describe("勾选与写回模型列表", () => {
  test("勾选：点一下切换，新拉取的勾上后会追加到末尾", () => {
    let rows = mergeFetchedModels(["a", "b"], [], []);
    rows = toggleModelSelection(rows, "b");
    expect(selectedModels(rows)).toEqual(["b"]);
    rows = toggleModelSelection(rows, "a");
    expect(selectedModels(rows)).toEqual(["a", "b"]);
    rows = toggleModelSelection(rows, "a");
    expect(selectedModels(rows)).toEqual(["b"]);
  });

  test("写回顺序与原列表一致，新模型按拉取顺序排在后面", () => {
    let rows = mergeFetchedModels(["new-1", "old-2", "new-2", "old-1"], ["old-1", "old-2"], []);
    expect(selectedModels(rows)).toEqual(["old-1", "old-2"]);
    rows = toggleModelSelection(rows, "new-2");
    rows = toggleModelSelection(rows, "new-1");
    // 勾选顺序是 new-2 再 new-1，但写回按列表里出现的顺序：new-1 在 new-2 前面
    expect(selectedModels(rows)).toEqual(["old-1", "old-2", "new-1", "new-2"]);
  });
  test("取消勾选列表里已有的模型：只剩勾选的", () => {
    let rows = mergeFetchedModels(["a", "b"], ["a", "b"], []);
    rows = toggleModelSelection(rows, "a");
    expect(selectedModels(rows)).toEqual(["b"]);
  });

  test("勾选未知 ID 不改变结果", () => {
    const rows = mergeFetchedModels(["a"], ["a"], []);
    expect(selectedModels(toggleModelSelection(rows, "nope"))).toEqual(["a"]);
  });

  test("全部取消：空列表（页面据此提示至少保留一个模型）", () => {
    const rows = mergeFetchedModels(["a"], ["a"], []).map((m) => ({ ...m, checked: false }));
    expect(selectedModels(rows)).toEqual([]);
  });
});

describe("勾选框选项", () => {
  test("已删除的模型标为“已删除”；已在列表的标为“已在列表”", () => {
    const options = modelCheckOptions(mergeFetchedModels(["a", "b"], ["a"], ["gone"]));
    expect(options.map((o) => o.value)).toEqual(["a", "b", "gone"]);
    expect(options[0]!.label).toContain("已在列表");
    expect(options[1]!.label).not.toContain("已在列表");
    expect(options[2]!.label).toContain("已删除");
  });
});
