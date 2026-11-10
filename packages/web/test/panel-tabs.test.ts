import { describe, expect, test } from "bun:test";
import { closedTabs, closeTab, INITIAL_TABS, openTab, tabLabel } from "../src/view/panel-tabs";

describe("右栏标签页", () => {
  test("一开始只有默认页", () => {
    expect(INITIAL_TABS).toEqual({ extra: [], active: "main" });
    expect(closedTabs(INITIAL_TABS)).toEqual(["steps", "detail"]);
  });

  test("点开一页：排在后面并切过去；已经开着只是切过去，不重复", () => {
    const one = openTab(INITIAL_TABS, "steps");
    expect(one).toEqual({ extra: ["steps"], active: "steps" });
    const two = openTab(one, "detail");
    expect(two).toEqual({ extra: ["steps", "detail"], active: "detail" });
    expect(openTab(two, "steps")).toEqual({ extra: ["steps", "detail"], active: "steps" });
    expect(openTab(two, "main").active).toBe("main");
    expect(closedTabs(one)).toEqual(["detail"]);
  });

  test("关掉正看着的页：切到左边相邻的一页，没有就回默认页", () => {
    const both = { extra: ["steps", "detail"] as const, active: "detail" as const };
    expect(closeTab({ ...both, extra: [...both.extra] }, "detail")).toEqual({ extra: ["steps"], active: "steps" });
    expect(closeTab({ extra: ["steps"], active: "steps" }, "steps")).toEqual({ extra: [], active: "main" });
    expect(closeTab({ extra: ["steps", "detail"], active: "steps" }, "steps")).toEqual({ extra: ["detail"], active: "main" });
  });

  test("关掉没看着的页：不改变正看着的页；关一个没开的页什么都不变", () => {
    expect(closeTab({ extra: ["steps", "detail"], active: "detail" }, "steps")).toEqual({ extra: ["detail"], active: "detail" });
    const idle = { extra: ["steps" as const], active: "main" as const };
    expect(closeTab(idle, "detail")).toBe(idle);
  });

  test("页的名字", () => {
    expect([tabLabel("steps"), tabLabel("detail")]).toEqual(["任务步骤", "任务详情"]);
  });
});
