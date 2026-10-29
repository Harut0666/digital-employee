import { describe, expect, test } from "bun:test";
import { availableCommands, isCommandAllowed } from "../src/view/permissions";

describe("availableCommands", () => {
  test("待人工处理：只有操作员能重试和终止", () => {
    expect(availableCommands("awaiting_operator", "operator")).toEqual(["retry", "terminate"]);
    expect(availableCommands("awaiting_operator", "initiator")).toEqual([]);
    expect(availableCommands("awaiting_operator", "reviewer")).toEqual([]);
  });

  test("待审核：只有审核人能通过和退回", () => {
    expect(availableCommands("awaiting_review", "reviewer").sort()).toEqual(["approve", "return"]);
    expect(availableCommands("awaiting_review", "initiator")).toEqual([]);
  });

  test("待发起人补充：发起人可补充、选择继续或终止", () => {
    expect(availableCommands("awaiting_initiator", "initiator").sort()).toEqual([
      "continue_with_partial",
      "continue_without_minutes",
      "supplement",
      "terminate",
    ]);
  });

  test("草稿：发起人可提交；终态没有任何命令", () => {
    expect(availableCommands("draft", "initiator")).toEqual(["submit"]);
    expect(availableCommands("delivered", "initiator")).toEqual([]);
    expect(availableCommands("terminated", "operator")).toEqual([]);
  });

  test("暂停原因给出的选项会进一步收窄命令", () => {
    const pause = { code: "minutes_missing", message: "缺纪要", options: ["supplement", "terminate"] } as const;
    expect(availableCommands("awaiting_initiator", "initiator", pause).sort()).toEqual(["supplement", "terminate"]);
  });

  test("isCommandAllowed", () => {
    expect(isCommandAllowed("approve", "awaiting_review", "initiator")).toBe(false);
    expect(isCommandAllowed("approve", "awaiting_review", "reviewer")).toBe(true);
  });
});
