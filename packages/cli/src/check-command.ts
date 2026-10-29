// de check <result> <expect>：只核对一次运行的交付物，不重跑。
import { resolve } from "node:path";
import { checkRun, readChecksFromCaseFile } from "./checks";

export interface CheckCommandArgs {
  resultDir: string;
  expectFile: string;
}

export async function checkCommand(args: CheckCommandArgs): Promise<number> {
  const expectFile = resolve(args.expectFile);
  const resultDir = resolve(args.resultDir);
  const checks = readChecksFromCaseFile(expectFile);
  const report = await checkRun(resultDir, checks);
  const failed = report.outcomes.filter((outcome) => !outcome.passed).length;
  console.log(`核对 ${resultDir}`);
  for (const outcome of report.outcomes) {
    console.log(`  ${outcome.passed ? "✓" : "✗"} ${outcome.kind} ${outcome.target}  ${outcome.detail}`);
  }
  console.log(`${report.outcomes.length} 项核对：${report.outcomes.length - failed} 项通过，${failed} 项未通过`);
  return report.passed ? 0 : 1;
}
