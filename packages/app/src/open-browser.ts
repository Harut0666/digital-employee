// 用系统默认浏览器打开界面。打不开不影响服务端运行（用户可以从控制台复制地址）。
import { spawn } from "node:child_process";

export function openBrowser(url: string): void {
  const [command, args] =
    process.platform === "win32"
      ? ["cmd", ["/c", "start", "", url]]
      : process.platform === "darwin"
        ? ["open", [url]]
        : ["xdg-open", [url]];
  try {
    const child = spawn(command, args, { detached: true, stdio: "ignore" });
    child.on("error", () => {
      /* 无浏览器/无桌面环境：忽略 */
    });
    child.unref();
  } catch {
    /* 同上 */
  }
}
