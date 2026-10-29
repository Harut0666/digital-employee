import { describe, expect, test } from "bun:test";
import { setup } from "./helpers";

describe("GET /api/employees", () => {
  test("lists employees with id, name, description and formSchema", async () => {
    const { call } = setup();
    const res = await call("/api/employees");
    expect(res.status).toBe(200);
    const list = (await res.json()) as {
      id: string;
      name: string;
      description: string;
      formSchema: unknown;
      formUi?: unknown;
      reviewSections: { id: string; heading: string }[];
    }[];
    expect(list.map((e) => e.id).sort()).toEqual(["patent-brief", "weekly-report"]);
    const weekly = list.find((e) => e.id === "weekly-report")!;
    expect(weekly.name).toBe("员工-weekly-report");
    expect(weekly.description).toBe("描述-weekly-report");
    expect(weekly.formSchema).toEqual({ type: "object", title: "weekly-report" });
  });

  test("only directories with a workflow.yaml are employees (employees/test is skipped); formUi and reviewSections are included", async () => {
    const { call } = setup();
    const list = (await (await call("/api/employees")).json()) as { id: string; formUi?: unknown; reviewSections: unknown }[];
    expect(list.map((e) => e.id)).not.toContain("test");
    const weekly = list.find((e) => e.id === "weekly-report")!;
    expect(weekly.formUi).toEqual({ "ui:order": ["a"] });
    expect(weekly.reviewSections).toEqual([{ id: "summary", heading: "摘要-weekly-report" }]);
  });
});
