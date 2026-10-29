import { describe, expect, test } from "bun:test";
import { choosePort, isPortAvailable } from "../src/port";

describe("choosePort", () => {
  test("起始端口空闲时直接用起始端口", async () => {
    expect(await choosePort({ start: 8787, isAvailable: async () => true })).toBe(8787);
  });

  test("起始端口被占用时尝试下一个（并跳过连续被占用的端口）", async () => {
    const tried: number[] = [];
    const port = await choosePort({
      start: 8787,
      isAvailable: async (p) => {
        tried.push(p);
        return p === 8790;
      },
    });
    expect(port).toBe(8790);
    expect(tried).toEqual([8787, 8788, 8789, 8790]);
  });

  test("全部被占用时报错并说明试过哪些端口", async () => {
    await expect(choosePort({ start: 8787, attempts: 3, isAvailable: async () => false })).rejects.toThrow(/8787/);
  });
});

describe("isPortAvailable（真实监听）", () => {
  test("端口被占用时返回 false，释放后返回 true", async () => {
    const server = Bun.serve({ port: 0, hostname: "127.0.0.1", fetch: () => new Response("busy") });
    const port = server.port!;
    try {
      expect(await isPortAvailable(port)).toBe(false);
    } finally {
      await server.stop(true);
    }
    // 停止监听后端口应重新可用（Windows 上偶有延迟，最多等 3 秒）
    let available = false;
    for (let i = 0; i < 30 && !available; i++) {
      available = await isPortAvailable(port);
      if (!available) await Bun.sleep(100);
    }
    expect(available).toBe(true);
  });
});
