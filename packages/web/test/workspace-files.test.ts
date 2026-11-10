import { describe, expect, test } from "bun:test";
import type { FolderEntry } from "../src/api/types";
import { formatSize, parentOf, previewKind, treeRows } from "../src/view/workspace-files";

const dir = (path: string): FolderEntry => ({ path, name: path.split("/").at(-1) ?? path, dir: true });
const file = (path: string, size = 10): FolderEntry => ({ path, name: path.split("/").at(-1) ?? path, size });

describe("treeRows", () => {
  const children = {
    "": [dir("交付"), dir("资料"), file("a.txt")],
    "资料": [dir("资料/子目录"), file("资料/汇总.xlsx")],
    "资料/子目录": [file("资料/子目录/明细.csv")],
  };

  test("都没展开时只有根这一层", () => {
    expect(treeRows(children, new Set()).map((r) => [r.entry.path, r.depth, r.open])).toEqual([
      ["交付", 0, false],
      ["资料", 0, false],
      ["a.txt", 0, false],
    ]);
  });

  test("展开的文件夹把内容缩进列在它下面，可以逐层展开", () => {
    const rows = treeRows(children, new Set(["资料", "资料/子目录"]));
    expect(rows.map((r) => `${"  ".repeat(r.depth)}${r.entry.name}`)).toEqual(["交付", "资料", "  子目录", "    明细.csv", "  汇总.xlsx", "a.txt"]);
    expect(rows.find((r) => r.entry.path === "资料")?.open).toBe(true);
  });

  test("展开了但还没加载的文件夹后面没有行；文件的“展开”不算", () => {
    expect(treeRows({ "": [dir("交付"), file("a.txt")] }, new Set(["交付", "a.txt"])).map((r) => [r.entry.path, r.open])).toEqual([
      ["交付", true],
      ["a.txt", false],
    ]);
  });
});

describe("previewKind", () => {
  test("按扩展名决定怎么预览", () => {
    expect(["简报.md", "说明.MD", "周报.docx", "图.PNG", "x.jpeg", "报告.pdf", "明细.csv", "配置.yaml", "汇总.xlsx", "无扩展名"].map(previewKind)).toEqual([
      "markdown",
      "markdown",
      "docx",
      "image",
      "image",
      "pdf",
      "text",
      "text",
      "none",
      "none",
    ]);
  });
});

describe("小工具", () => {
  test("大小和所在文件夹", () => {
    expect([formatSize(undefined), formatSize(10), formatSize(2048), formatSize(3 * 1024 * 1024)]).toEqual(["", "10 B", "2 KB", "3.0 MB"]);
    expect([parentOf("a.txt"), parentOf("资料/汇总.xlsx"), parentOf("a/b/c.md")]).toEqual(["", "资料", "a/b"]);
  });
});
