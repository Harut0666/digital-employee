import { describe, expect, test } from "bun:test";
import { assetBaseOf, resolveAssetSrc } from "../src/view/markdown-assets";

describe("Markdown 简报的图表地址", () => {
  test("从交付物下载地址得到图表前缀，带不带版本号都行", () => {
    expect(assetBaseOf("http://127.0.0.1:8787/api/tasks/t-1/deliverables/md")).toBe("http://127.0.0.1:8787/api/tasks/t-1/assets/");
    expect(assetBaseOf("/api/tasks/t-1/deliverables/md?version=2")).toBe("/api/tasks/t-1/assets/");
  });

  test("不是 md 交付物、或没有地址时没有前缀", () => {
    expect(assetBaseOf("/api/tasks/t-1/deliverables/docx")).toBeUndefined();
    expect(assetBaseOf(undefined)).toBeUndefined();
  });

  test("相对路径 assets/x.png 映射成图表地址，文件名里的特殊字符要转义", () => {
    const base = "/api/tasks/t-1/assets/";
    expect(resolveAssetSrc("assets/routes-0347150b0a.png", base)).toBe("/api/tasks/t-1/assets/routes-0347150b0a.png");
    expect(resolveAssetSrc("./assets/trend.png", base)).toBe("/api/tasks/t-1/assets/trend.png");
    expect(resolveAssetSrc("assets/a b.png", base)).toBe("/api/tasks/t-1/assets/a%20b.png");
  });

  test("别的地址和不在 assets 里的路径原样返回，不放行路径穿越", () => {
    const base = "/api/tasks/t-1/assets/";
    expect(resolveAssetSrc("https://example.com/x.png", base)).toBe("https://example.com/x.png");
    expect(resolveAssetSrc("data:image/png;base64,AAAA", base)).toBe("data:image/png;base64,AAAA");
    expect(resolveAssetSrc("assets/../secret.png", base)).toBe("assets/../secret.png");
    expect(resolveAssetSrc("assets/x.png", undefined)).toBe("assets/x.png");
    expect(resolveAssetSrc(undefined, base)).toBeUndefined();
  });
});
