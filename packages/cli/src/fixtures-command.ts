// de fixtures [--out <dir>] [--source <xlsx>]：生成变异数据到 samples/fixtures/（生成物入库）。
import { join, resolve } from "node:path";
import { generateFixtures } from "./fixtures";
import { REPO_ROOT, resolveSamplesDir } from "./paths";

export interface FixturesCommandArgs {
  outDir?: string;
  sourcePath?: string;
}

const formatBytes = (bytes: number): string => `${(bytes / 1024 / 1024).toFixed(2)}MB`;

export async function fixturesCommand(args: FixturesCommandArgs): Promise<number> {
  const samplesDir = resolveSamplesDir();
  const sourcePath = resolve(args.sourcePath ?? join(samplesDir, "scenario-a", "销售明细.xlsx"));
  const outDir = resolve(args.outDir ?? join(samplesDir, "fixtures"));
  const manifest = await generateFixtures({ sourcePath, outDir, repoRoot: REPO_ROOT });
  console.log(`源数据：${manifest.source}（sha256 ${manifest.sourceSha256.slice(0, 12)}…）`);
  for (const fixture of manifest.fixtures) {
    const structure =
      fixture.coverage === null
        ? "损坏文件"
        : `${fixture.sheets.length} 个工作表、销售明细 ${fixture.salesRows} 行（${fixture.coverage.from} 至 ${fixture.coverage.to}）`;
    console.log(`  ${fixture.file}  ${formatBytes(fixture.bytes)}  ${structure}`);
  }
  console.log(`已生成 ${manifest.fixtures.length} 份夹具：${outDir}`);
  return 0;
}
