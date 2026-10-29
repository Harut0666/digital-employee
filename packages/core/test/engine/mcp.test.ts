import { describe, expect, test } from "bun:test";
import { createMiniTools, ToolFailure } from "./helpers/mini-tools";

describe("createMcpToolClient", () => {
  test("listTools 返回工具描述", async () => {
    const tools = await createMiniTools();
    const listed = await tools.client.listTools();
    expect(listed.map((t) => t.name)).toContain("load_sales");
    expect(listed.find((t) => t.name === "load_sales")?.description).toContain("load_sales");
    await tools.client.close();
  });

  test("成功调用读取 structuredContent", async () => {
    const tools = await createMiniTools();
    const result = await tools.client.call("load_sales", { n: 7 });
    expect(result).toEqual({ ok: true, data: { rows: 7 } });
    await tools.client.close();
  });

  test("isError 归一化为 ok=false，默认可重试", async () => {
    const tools = await createMiniTools();
    tools.setHandler("load_sales", () => {
      throw new ToolFailure("磁盘不可读");
    });
    const result = await tools.client.call("load_sales", {});
    expect(result).toEqual({ ok: false, error: "磁盘不可读", retryable: true });
    await tools.client.close();
  });

  test("工具声明不可重试时 retryable=false", async () => {
    const tools = await createMiniTools();
    tools.setHandler("load_sales", () => {
      throw new ToolFailure("参数非法", false);
    });
    const result = await tools.client.call("load_sales", {});
    expect(result).toMatchObject({ ok: false, retryable: false });
    await tools.client.close();
  });

  test("调用不存在的工具返回 ok=false 而不是抛出", async () => {
    const tools = await createMiniTools();
    const result = await tools.client.call("no_such_tool", {});
    expect(result.ok).toBe(false);
    await tools.client.close();
  });
});
