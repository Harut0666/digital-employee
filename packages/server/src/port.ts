// 端口选择：默认 8787，被占用时往下一个端口试（设计 §14：控制台打印实际地址）。
// isPortAvailable 用真实监听探测；choosePort 把探测函数作为参数，便于单测。

export interface ChoosePortOptions {
  start: number;
  /** 最多尝试多少个连续端口，缺省 20 */
  attempts?: number;
  isAvailable: (port: number) => Promise<boolean>;
}

/** 从 start 开始找第一个空闲端口；都不可用时报错（错误里带上试过的区间）。 */
export async function choosePort(options: ChoosePortOptions): Promise<number> {
  const attempts = Math.max(1, options.attempts ?? 20);
  for (let i = 0; i < attempts; i++) {
    const port = options.start + i;
    if (await options.isAvailable(port)) return port;
  }
  const last = options.start + attempts - 1;
  throw new Error(`端口 ${options.start}-${last} 都被占用，请用 DE_PORT 指定一个空闲端口`);
}

/** 试听一个端口：能 Bind 就立刻释放并返回 true。 */
export async function isPortAvailable(port: number): Promise<boolean> {
  try {
    const probe = Bun.serve({ port, hostname: "0.0.0.0", fetch: () => new Response("") });
    await probe.stop(true);
    return true;
  } catch {
    return false;
  }
}
