// 构建单文件 exe：前端构建产物 → 生成嵌入归档 → bun build --compile。
//   bun run build:exe   → dist/digital-employee.exe
import { existsSync, mkdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { generateEmbedded, REPO_ROOT } from "./gen-embedded";

const OUT_FILE = join(REPO_ROOT, "dist", "digital-employee.exe");
const ENTRY = join(REPO_ROOT, "packages", "app", "src", "main.ts");

async function run(label: string, cmd: string[], cwd = REPO_ROOT): Promise<void> {
  console.log(`\n▶ ${label}`);
  const started = performance.now();
  const proc = Bun.spawn({ cmd, cwd, stdout: "inherit", stderr: "inherit" });
  const code = await proc.exited;
  if (code !== 0) throw new Error(`${label} 失败（退出码 ${code}）`);
  console.log(`  完成，用时 ${((performance.now() - started) / 1000).toFixed(1)}s`);
}

async function main(): Promise<void> {
  const started = performance.now();

  // 1. 前端构建产物：exe 里的界面就是这份
  await run("构建前端（@de/web）", [process.execPath, "run", "--filter", "@de/web", "build"]);

  // 2. 嵌入归档：web / employees / samples
  console.log("\n▶ 生成嵌入归档");
  await generateEmbedded({ strict: true });

  // 3. 编译成单文件
  mkdirSync(join(REPO_ROOT, "dist"), { recursive: true });
  await run("编译 exe", [process.execPath, "build", "--compile", "--target", "bun", ENTRY, "--outfile", OUT_FILE]);

  if (!existsSync(OUT_FILE)) throw new Error(`没有生成 ${OUT_FILE}`);
  const size = statSync(OUT_FILE).size;
  console.log(
    `\n✅ ${OUT_FILE}\n   体积 ${(size / 1024 / 1024).toFixed(1)}MB，整体用时 ${((performance.now() - started) / 1000).toFixed(1)}s`,
  );
}

try {
  await main();
} catch (err) {
  console.error(`\n✗ ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
}
