import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { parseCase } from "../src/case";

const CASES_DIR = join(import.meta.dir, "..", "..", "..", "samples", "cases");
const FILES = readdirSync(CASES_DIR).filter((f) => /^A-0[1-5]\.json$/.test(f));

describe("samples/cases", () => {
  test("A-01 through A-05 are all present", () => {
    expect(FILES.sort()).toEqual(["A-01.json", "A-02.json", "A-03.json", "A-04.json", "A-05.json"]);
  });

  test.each(FILES)("%s parses and every referenced attachment exists", (file) => {
    const def = parseCase(JSON.parse(readFileSync(join(CASES_DIR, file), "utf8")), CASES_DIR, file);
    expect(def.id).toBe(file.replace(".json", ""));
    const all = [
      ...def.attachments,
      ...def.steps.flatMap((s) => (s.command.type === "supplement" ? (s.command.attachments ?? []) : [])),
    ];
    for (const a of all) expect(existsSync(a.sourcePath)).toBe(true);
  });

  test("A-03 pauses on missing_sales and then supplements the workbook; A-05 stops at period_out_of_range", () => {
    const a03 = parseCase(JSON.parse(readFileSync(join(CASES_DIR, "A-03.json"), "utf8")), CASES_DIR);
    expect(a03.steps[0]!.expect?.pauseCode).toBe("missing_sales");
    expect(a03.steps[1]!.command.type).toBe("supplement");
    const a05 = parseCase(JSON.parse(readFileSync(join(CASES_DIR, "A-05.json"), "utf8")), CASES_DIR);
    expect(a05.expect?.pauseCode).toBe("period_out_of_range");
    expect(a05.form).toEqual({ period: { year: 2026, week: 46 } });
  });
});
