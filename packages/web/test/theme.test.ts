import { describe, expect, test } from "bun:test";
import { antdTheme } from "../src/view/theme";

describe("antd 主题取值", () => {
  test("浅色和深色各用一套主色，字号跟随设置", () => {
    const light = antdTheme("light", 14);
    const dark = antdTheme("dark", 16);
    expect(light.token?.colorPrimary).toBe("#1a7f67");
    expect(dark.token?.colorPrimary).toBe("#3fb596");
    expect(light.token?.fontSize).toBe(14);
    expect(dark.token?.fontSize).toBe(16);
  });

  test("深色主按钮用深色字，浅色用白字（对比度）", () => {
    expect(antdTheme("dark", 14).components?.Button?.primaryColor).toBe("#06241d");
    expect(antdTheme("light", 14).components?.Button?.primaryColor).toBe("#ffffff");
  });

  test("与 styles/tokens.css 的主色保持一致", async () => {
    const css = await Bun.file(new URL("../src/styles/tokens.css", import.meta.url)).text();
    expect(css).toContain(`--primary: ${antdTheme("light", 14).token?.colorPrimary};`);
    expect(css).toContain(`--primary: ${antdTheme("dark", 14).token?.colorPrimary};`);
  });
});
