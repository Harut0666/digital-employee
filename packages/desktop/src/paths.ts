// 桌面版的资源路径（纯函数，可单测；不依赖 Electrobun 运行时）。
// Electrobun 的 build.copy 目标落在 Resources/app/ 下（spike 实测），所以默认资源根是
// PATHS.RESOURCES_FOLDER + "app"；测试/开发用 DE_DESKTOP_ASSETS 覆盖。
import { join } from "node:path";

export type DesktopPathEnv = {
  [key: string]: string | undefined;
  DE_DESKTOP_ASSETS?: string;
  DE_RESVG_WASM?: string;
};

export interface DesktopPathOptions {
  /** Electrobun 的 PATHS.RESOURCES_FOLDER */
  resourcesFolder: string;
  env?: DesktopPathEnv;
}

/** 桌面资源根：默认 Resources/app；DE_DESKTOP_ASSETS 可覆盖（测试/开发）。 */
export function resolveDesktopAssetsDir(options: DesktopPathOptions): string {
  const override = options.env?.DE_DESKTOP_ASSETS?.trim();
  return override ? override : join(options.resourcesFolder, "app");
}

/** 随包拷贝的 resvg wasm：默认 <资源根>/assets/resvg.wasm；DE_RESVG_WASM 可覆盖。 */
export function resolveDesktopResvgWasm(assetsDir: string, env?: DesktopPathEnv): string {
  const override = env?.DE_RESVG_WASM?.trim();
  return override ? override : join(assetsDir, "assets", "resvg.wasm");
}
