// 打包时嵌进 exe 的三份资源归档（构建期由 scripts/gen-embedded.ts 生成）。
// `with { type: "file" }` 让 Bun 把归档文件本身打进可执行文件：exe 里拿到的是 Bun 的虚拟路径，
// 源码运行时拿到的是仓库里的真实路径，两种情况下都用 Bun.file(path) 读。
import webBundle from "../assets/web.bundle" with { type: "file" };
import employeesBundle from "../assets/employees.bundle" with { type: "file" };
import samplesBundle from "../assets/samples.bundle" with { type: "file" };
import type { EmbeddedBundlePaths } from "./workspace";

export const EMBEDDED_BUNDLES: EmbeddedBundlePaths = {
  web: webBundle,
  employees: employeesBundle,
  samples: samplesBundle,
};
