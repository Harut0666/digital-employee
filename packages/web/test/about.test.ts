import { describe, expect, test } from "bun:test";
import { ABOUT, platformLabel, runtimeLabel } from "../src/view/about";

describe("runtimeLabel", () => {
  test("后端字段叫 bun，实际是 Go 版本号时按 Go 显示", () => {
    expect(runtimeLabel({ bun: "go1.27.3", platform: "windows", arch: "amd64" })).toBe("Go 1.27.3 · Windows · amd64");
  });

  test("旧的 Bun 版本号照旧显示", () => {
    expect(runtimeLabel({ bun: "1.4.2", platform: "win32", arch: "x64" })).toBe("Bun 1.4.2 · Windows · x64");
  });

  test("系统名", () => {
    expect([platformLabel("windows"), platformLabel("win32"), platformLabel("darwin"), platformLabel("linux"), platformLabel("freebsd")]).toEqual([
      "Windows",
      "Windows",
      "macOS",
      "Linux",
      "freebsd",
    ]);
  });
});

describe("关于页文案", () => {
  test("四条工作原则都有标题和说明，且不留空", () => {
    expect(ABOUT.principles.length).toBe(4);
    for (const item of ABOUT.principles) {
      expect(item.title.trim()).not.toBe("");
      expect(item.text.trim().length).toBeGreaterThan(20);
    }
  });

  test("不再出现已经过时的说法（Bun、Electrobun）", () => {
    const all = [ABOUT.name, ABOUT.tagline, ABOUT.summary, ABOUT.privacy, ABOUT.docs, ...ABOUT.principles.flatMap((p) => [p.title, p.text])].join("\n");
    expect(all).not.toMatch(/Bun|Electrobun/);
  });

  test("隐私说明讲清 Key 只存本机、只显示末 4 位", () => {
    expect(ABOUT.privacy).toContain("本机");
    expect(ABOUT.privacy).toContain("末 4 位");
  });
});
