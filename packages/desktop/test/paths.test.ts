import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { resolveDesktopAssetsDir, resolveDesktopResvgWasm } from "../src/paths";

describe("桌面资源路径", () => {
  test("默认资源根是 PATHS.RESOURCES_FOLDER + app（build.copy 落在 Resources/app 下）", () => {
    expect(resolveDesktopAssetsDir({ resourcesFolder: "C:\\App\\Resources" })).toBe(join("C:\\App\\Resources", "app"));
  });

  test("DE_DESKTOP_ASSETS 覆盖资源根（测试/开发）", () => {
    expect(
      resolveDesktopAssetsDir({ resourcesFolder: "C:\\App\\Resources", env: { DE_DESKTOP_ASSETS: "D:\\tmp\\assets" } }),
    ).toBe("D:\\tmp\\assets");
    // 空字符串视为未设置
    expect(resolveDesktopAssetsDir({ resourcesFolder: "C:\\App\\Resources", env: { DE_DESKTOP_ASSETS: "  " } })).toBe(
      join("C:\\App\\Resources", "app"),
    );
  });

  test("resvg wasm 默认在 <资源根>/assets/resvg.wasm，DE_RESVG_WASM 可覆盖", () => {
    expect(resolveDesktopResvgWasm("D:\\tmp\\assets")).toBe(join("D:\\tmp\\assets", "assets", "resvg.wasm"));
    expect(resolveDesktopResvgWasm("D:\\tmp\\assets", { DE_RESVG_WASM: "D:\\wasm\\resvg.wasm" })).toBe("D:\\wasm\\resvg.wasm");
  });
});
