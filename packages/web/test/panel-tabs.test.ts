import { describe, expect, test } from "bun:test";
import { closedTabs, closeTab, INITIAL_TABS, openTab, tabLabel } from "../src/view/panel-tabs";

describe("右栏标签页", () => {
  test("一开始只有默认页", () => {
    expect(INITIAL_TABS).toEqual({ extra: [], active: "main" });
    expect(closedTabs(INITIAL_TABS)).toEqual(["steps", "files"]);
  });

  test("点开一页：排在后面并切过去；已经开着只是切过去，不重复", () => {
    const one = openTab(INITIAL_TABS, "steps");
    expect(one).toEqual({ extra: ["steps"], active: "steps" });
    const two = openTab(one, "files");
    expect(two).toEqual({ extra: ["steps", "files"], active: "files" });
    expect(openTab(two, "steps")).toEqual({ extra: ["steps", "files"], active: "steps" });
    expect(openTab(two, "main").active).toBe("main");
    expect(closedTabs(one)).toEqual(["files"]);
  });

  test("关掉正看着的页：切到左边相邻的一页，没有就回默认页", () => {
    expect(closeTab({ extra: ["steps", "files"], active: "files" }, "files")).toEqual({ extra: ["steps"], active: "steps" });
    expect(closeTab({ extra: ["steps"], active: "steps" }, "steps")).toEqual({ extra: [], active: "main" });
    expect(closeTab({ extra: ["steps", "files"], active: "steps" }, "steps")).toEqual({ extra: ["files"], active: "main" });
  });

  test("关掉没看着的页：不改变正看着的页；关一个没开的页什么都不变", () => {
    expect(closeTab({ extra: ["steps", "files"], active: "files" }, "steps")).toEqual({ extra: ["files"], active: "files" });
    const idle = { extra: ["steps" as const], active: "main" as const };
    expect(closeTab(idle, "files")).toBe(idle);
  });

  test("页的名字", () => {
    expect([tabLabel("steps"), tabLabel("files")]).toEqual(["任务步骤", "工作区文件"]);
  });
});
