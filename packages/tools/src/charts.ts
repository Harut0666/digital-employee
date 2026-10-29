// 图表：ECharts 服务端渲染出 SVG，再用 resvg-wasm 转成 PNG（Word 里嵌 PNG 最稳）。
/// <reference path="./env.d.ts" />
import * as echarts from "echarts";
import { Resvg, initWasm } from "@resvg/resvg-wasm";
import { existsSync, readFileSync } from "node:fs";
import type { WeeklyFacts } from "@de/core/contract";
// 让 bun build --compile 把 wasm 一并打进可执行文件
import resvgWasmPath from "@resvg/resvg-wasm/index_bg.wasm" with { type: "file" };

export interface FontOptions {
  /** 字体文件路径（ttf/otf/ttc）。默认依次尝试环境变量 DE_FONT_PATHS 与常见系统中文字体 */
  paths?: string[];
  /** 直接给字体二进制（例如打包进 exe 的思源黑体子集） */
  buffers?: Uint8Array[];
}

const SYSTEM_FONT_CANDIDATES = [
  "C:\\Windows\\Fonts\\msyh.ttc",
  "C:\\Windows\\Fonts\\simhei.ttf",
  "C:\\Windows\\Fonts\\Deng.ttf",
  "/System/Library/Fonts/PingFang.ttc",
  "/System/Library/Fonts/STHeiti Medium.ttc",
  "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
  "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
  "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
];

let wasmReady: Promise<void> | undefined;
const ensureWasm = () => (wasmReady ??= initWasm(readFileSync(resvgWasmPath)));

const fontCache = new Map<string, Uint8Array>();
function loadFontBuffers(opts: FontOptions): Uint8Array[] {
  if (opts.buffers?.length) return opts.buffers;
  const fromEnv = (process.env.DE_FONT_PATHS ?? "").split(process.platform === "win32" ? ";" : ":").filter(Boolean);
  const wanted = opts.paths?.length ? opts.paths : fromEnv;
  const paths = wanted.length ? wanted : SYSTEM_FONT_CANDIDATES.filter((p) => existsSync(p)).slice(0, 1);
  const out: Uint8Array[] = [];
  for (const p of paths) {
    if (!existsSync(p)) continue;
    let buf = fontCache.get(p);
    if (!buf) fontCache.set(p, (buf = new Uint8Array(readFileSync(p))));
    out.push(buf);
  }
  return out; // 找不到字体时图表里的中文不会显示，但图本身仍可生成
}

const W = 640;
const H = 300;
const COLORS = ["#2f6fdb", "#e0873b", "#3a9d6e", "#a05bd0", "#d2506b"];
const wan = (v: number) => (v >= 10000 ? `${Math.round(v / 1000) / 10}万` : String(v));
const weekLabel = (w: { year: number; week: number }) => `第${w.week}周`;

function baseOption(): echarts.EChartsCoreOption {
  return {
    animation: false,
    backgroundColor: "#ffffff",
    textStyle: { fontFamily: "sans-serif", fontSize: 13, color: "#333" },
    grid: { left: 56, right: 44, top: 56, bottom: 36 },
    legend: { top: 28, itemWidth: 14, itemHeight: 8 },
  };
}

/** 图 1：各区域本周与上周实收对比 */
export function regionBarOption(f: WeeklyFacts): echarts.EChartsCoreOption {
  const names = Object.keys(f.regions);
  const cur = names.map((n) => Math.round(f.regions[n]!.revenueCents / 100));
  const hasPrev = names.every((n) => f.regions[n]!.prevRevenueCents !== null);
  const prev = names.map((n) => Math.round((f.regions[n]!.prevRevenueCents ?? 0) / 100));
  return {
    ...baseOption(),
    title: { text: `各区域实收对比（${weekLabel(f.period)}，元）`, left: "center", top: 4, textStyle: { fontSize: 15 } },
    xAxis: { type: "category", data: names },
    yAxis: { type: "value", axisLabel: { formatter: (v: number) => wan(v) } },
    series: [
      ...(hasPrev ? [{ name: "上周", type: "bar", data: prev, itemStyle: { color: "#b7c4d9" }, label: { show: true, position: "top", fontSize: 11 } }] : []),
      { name: "本周", type: "bar", data: cur, itemStyle: { color: COLORS[0] }, label: { show: true, position: "top", fontSize: 11 } },
    ],
  };
}

/** 图 2：近 4 周实收趋势（全公司与各区域） */
export function trendLineOption(f: WeeklyFacts): echarts.EChartsCoreOption {
  const weeks = f.trend.map((t) => weekLabel(t.week));
  const regionNames = Object.keys(f.regions);
  const line = (name: string, data: number[], color: string, dashed = false) => ({
    name,
    type: "line",
    data,
    symbolSize: 7,
    itemStyle: { color },
    lineStyle: { color, width: 2, type: dashed ? "dashed" : "solid" },
  });
  return {
    ...baseOption(),
    title: { text: "近 4 周实收趋势（元）", left: "center", top: 4, textStyle: { fontSize: 15 } },
    legend: { top: 28, itemWidth: 14, itemHeight: 8, data: ["全公司", ...regionNames] },
    xAxis: { type: "category", data: weeks, boundaryGap: false },
    yAxis: { type: "value", axisLabel: { formatter: (v: number) => wan(v) } },
    series: [
      line("全公司", f.trend.map((t) => Math.round(t.companyCents / 100)), "#333333", true),
      ...regionNames.map((n, i) => line(n, f.trend.map((t) => Math.round((t.regions[n] ?? 0) / 100)), COLORS[i % COLORS.length]!)),
    ],
  };
}

export async function renderChartPng(option: echarts.EChartsCoreOption, fonts: FontOptions = {}): Promise<Uint8Array> {
  const chart = echarts.init(null as never, null as never, { renderer: "svg", ssr: true, width: W, height: H });
  let svg: string;
  try {
    chart.setOption(option);
    svg = chart.renderToSVGString();
  } finally {
    chart.dispose();
  }
  await ensureWasm();
  const fontBuffers = loadFontBuffers(fonts);
  const resvg = new Resvg(svg, {
    fitTo: { mode: "width", value: W * 2 },
    background: "#ffffff",
    font: { fontBuffers, loadSystemFonts: false },
  });
  const png = resvg.render().asPng();
  resvg.free();
  return png;
}

export const CHART_SIZE = { width: W, height: H };
