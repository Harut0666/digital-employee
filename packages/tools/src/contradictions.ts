// detect_contradictions：判断纪要里的可核验说法与实际数据是否矛盾（docs/design.md §10.3）。
// 模型只负责抽取说法（claims），判定完全由程序完成：规则与阈值见 contract/tools.ts 与 qc.yaml 的 contradiction 节。
import type {
  Claim,
  ClaimSubject,
  ContradictionRule,
  ContradictionThresholds,
  DetectContradictionsInput,
  DetectContradictionsOutput,
  DetectedContradiction,
  WeeklyFacts,
} from "@de/core/contract";

interface ResolvedSubject {
  level: ClaimSubject["level"];
  key: string;
  /** 写进周报时用的展示名：区域名 / 门店名 / 品类名 / 全公司 */
  name: string;
  /** 实际环比；null 表示不可比（如第 36 周没有上周数据） */
  wow: number | null;
}

/** 按说法对象在事实里取值；对象在事实里找不到时返回 undefined。 */
function resolveSubject(facts: WeeklyFacts, subject: ClaimSubject): ResolvedSubject | undefined {
  switch (subject.level) {
    case "company":
      return { level: "company", key: "company", name: "全公司", wow: facts.company.wow };
    case "region": {
      const metric = Object.hasOwn(facts.regions, subject.key) ? facts.regions[subject.key] : undefined;
      return metric ? { level: "region", key: subject.key, name: subject.key, wow: metric.wow } : undefined;
    }
    case "store": {
      const store = facts.stores.find((s) => s.code === subject.key);
      return store ? { level: "store", key: subject.key, name: store.name, wow: store.wow } : undefined;
    }
    case "category": {
      const category = facts.categories.find((c) => c.category === subject.key);
      return category ? { level: "category", key: subject.key, name: category.category, wow: category.wow } : undefined;
    }
  }
}

/**
 * 判定一条说法是否构成矛盾。三条规则按顺序判，命中即返回：
 *   flat       说“持平”，但 |实际环比| > flatTolerance
 *   direction  说的方向与实际相反，且 |实际环比| > directionMin
 *   magnitude  给出幅度时，|实际幅度 − 纪要幅度| > magnitudeTolerance
 */
function judge(claim: Claim, actualWow: number, thresholds: ContradictionThresholds): ContradictionRule | undefined {
  const actualMagnitude = Math.abs(actualWow);
  if (claim.direction === "flat" && actualMagnitude > thresholds.flatTolerance) return "flat";
  const opposite = claim.direction === "up" ? actualWow < 0 : claim.direction === "down" ? actualWow > 0 : false;
  if (opposite && actualMagnitude > thresholds.directionMin) return "direction";
  if (typeof claim.magnitude === "number" && Math.abs(actualMagnitude - claim.magnitude) > thresholds.magnitudeTolerance) {
    return "magnitude";
  }
  return undefined;
}

export function detectContradictions(input: DetectContradictionsInput): DetectContradictionsOutput {
  const { claims, facts, thresholds } = input;
  const contradictions: DetectedContradiction[] = [];
  const usedIds = new Set<string>();
  let skippedCount = 0;

  for (const claim of claims) {
    const subject = resolveSubject(facts, claim.subject);
    // 对象在事实里找不到（如客流、口感这类数据里没有的说法），或实际环比不可比，一律不判，跳过并计数。
    if (!subject || subject.wow === null) {
      skippedCount += 1;
      continue;
    }
    const rule = judge(claim, subject.wow, thresholds);
    if (!rule) continue;

    const base = `contradiction:${subject.level}:${subject.key}`;
    let id = base;
    for (let i = 2; usedIds.has(id); i++) id = `${base}:${i}`;
    usedIds.add(id);

    contradictions.push({
      id,
      subject: { level: subject.level, key: subject.key, name: subject.name },
      claim: claim.magnitude === undefined ? { direction: claim.direction } : { direction: claim.direction, magnitude: claim.magnitude },
      actualWow: subject.wow,
      rule,
      sourceWeek: claim.sourceWeek,
      quote: claim.quote,
    });
  }

  return { contradictions, skippedCount };
}
