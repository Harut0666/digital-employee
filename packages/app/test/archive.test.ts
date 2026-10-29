import { describe, expect, test } from "bun:test";
import { decodeArchive, encodeArchive, type ArchiveEntry } from "../src/archive";

const utf8 = (s: string) => new TextEncoder().encode(s);
const text = (b: Uint8Array) => new TextDecoder().decode(b);

describe("encodeArchive / decodeArchive", () => {
  test("中文文件名与二进制内容原样往返", () => {
    const files: ArchiveEntry[] = [
      { key: "samples/scenario-a/销售明细.xlsx", data: new Uint8Array([0, 1, 2, 255, 254]) },
      { key: "samples/cases/scripts/A-01.json", data: utf8('{"中文": true}') },
      { key: "web/assets/index-abc123.js", data: utf8("console.log(1)") },
    ];
    const decoded = decodeArchive(encodeArchive(files));
    expect(decoded.map((e) => e.key)).toEqual([
      "samples/cases/scripts/A-01.json",
      "samples/scenario-a/销售明细.xlsx",
      "web/assets/index-abc123.js",
    ]);
    expect(decoded.find((e) => e.key.startsWith("samples/scenario-a"))!.data).toEqual(files[0]!.data);
    expect(text(decoded.find((e) => e.key.startsWith("samples/cases"))!.data)).toBe('{"中文": true}');
  });

  test("条目顺序不影响编码结果（构建可复现）", () => {
    const a: ArchiveEntry[] = [
      { key: "b.txt", data: utf8("B") },
      { key: "a.txt", data: utf8("A") },
    ];
    const b: ArchiveEntry[] = [
      { key: "a.txt", data: utf8("A") },
      { key: "b.txt", data: utf8("B") },
    ];
    expect(encodeArchive(a)).toEqual(encodeArchive(b));
  });

  test("空归档往返后是空列表", () => {
    expect(decodeArchive(encodeArchive([]))).toEqual([]);
  });

  test("内容被截断时报错，而不是悄悄返回残缺条目", () => {
    const bytes = encodeArchive([{ key: "a.txt", data: utf8("hello") }]);
    expect(() => decodeArchive(bytes.slice(0, bytes.length - 2))).toThrow(/归档/);
  });

  test("不是归档的文件报错", () => {
    expect(() => decodeArchive(utf8("this is not an archive"))).toThrow(/归档/);
  });
});
