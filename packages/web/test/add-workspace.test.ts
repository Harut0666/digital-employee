import { describe, expect, test } from "bun:test";
import { applyPickedFolder, pickFolderOutcome, workspaceSubmission } from "../src/view/add-workspace";

describe("添加工作区：选目录", () => {
  test("选中目录后填进输入框；名称为空时用目录名补全", () => {
    expect(applyPickedFolder({ name: "", path: "" }, "C:\\Users\\a\\Documents\\华南区")).toEqual({ name: "华南区", path: "C:\\Users\\a\\Documents\\华南区" });
  });

  test("已经写了名称就不覆盖", () => {
    expect(applyPickedFolder({ name: "我的区", path: "" }, "D:\\x\\华南区")).toEqual({ name: "我的区", path: "D:\\x\\华南区" });
  });

  test("目录名后面带斜杠或根目录也能取名", () => {
    expect(applyPickedFolder({ name: " ", path: "" }, "/home/a/ws/").name).toBe("ws");
    expect(applyPickedFolder({ name: "", path: "" }, "D:\\").name).toBe("D:");
  });

  test("接口返回的结果：选中、取消、失败", () => {
    expect(pickFolderOutcome({ ok: true, path: "D:\\x" })).toEqual({ kind: "picked", path: "D:\\x" });
    expect(pickFolderOutcome({ ok: true, cancelled: true })).toEqual({ kind: "cancelled" });
    expect(pickFolderOutcome({ ok: false, error: "unsupported", message: "当前不是桌面窗口，请直接输入目录" })).toEqual({ kind: "error", message: "当前不是桌面窗口，请直接输入目录" });
    // 选中但路径是空的，当取消处理
    expect(pickFolderOutcome({ ok: true, path: "  " })).toEqual({ kind: "cancelled" });
  });
});

describe("添加工作区：提交", () => {
  test("名称和目录都留空不能提交", () => {
    expect(workspaceSubmission({ name: " ", path: "" })).toBeUndefined();
  });

  test("只写目录：名称取目录名", () => {
    expect(workspaceSubmission({ name: "", path: "D:\\x\\华南区" })).toEqual({ name: "华南区", path: "D:\\x\\华南区" });
  });

  test("只写名称：在应用目录下新建，不带目录", () => {
    expect(workspaceSubmission({ name: " 华南区 ", path: "  " })).toEqual({ name: "华南区", path: undefined });
  });

  test("名称和目录都写了：原样提交（去掉首尾空白）", () => {
    expect(workspaceSubmission({ name: "华南", path: " D:\\x " })).toEqual({ name: "华南", path: "D:\\x" });
  });
});
