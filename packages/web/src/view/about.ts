// “关于”页的文案：给人读的产品说明。每一句都对应系统里真实存在的行为（见 README、docs/design.md），不写没做到的事。
// 单独放一个文件，改文案不用碰界面代码，单测也能守住措辞里的关键事实。

import type { DiagnosticsRuntime } from "@de/core/contract";

export const ABOUT = {
  name: "迷你数字员工",
  tagline: "可审核、可交付的岗位业务 Agent",
  summary:
    "把大模型做成流程可控、结果可检、出错能停的岗位员工：接收明确的岗位任务，调用受控工具完成工作，产出可直接使用的交付文件，并在交付前接受人工审核。",
  principles: [
    {
      title: "程序把关，模型只做判断",
      text: "能否送审、能否交付等关键控制点由程序判定；模型只负责需要理解和表达的环节，并受轮次与时长上限约束。",
    },
    {
      title: "自动质检，人工审核",
      text: "交付前按员工的质检规则逐项检查，不通过先自动修正，超过重试上限转人工；审核人可通过或退回，退回意见会在重做时被采纳。",
    },
    {
      title: "出错能停，也能续",
      text: "缺材料、工具故障、模型超限时任务暂停并说明原因，不编造结果；补充或重试后从中断的步骤继续，已完成的步骤不重复执行。",
    },
    {
      title: "员工即配置",
      text: "岗位职责、工作步骤、提示词、质检规则和资料库都是可读的配置文件，业务人员不改代码即可调整。",
    },
  ],
  privacy:
    "任务、会话、交付物和设置都保存在本机的应用目录里。API Key 只保存在本机设置文件中，界面和诊断导出只显示末 4 位，不输出明文；模型请求只发往你在“模型连接”里配置的服务。",
  docs: "完整的设计说明见项目根目录的 README.md 与 docs/design.md。",
} as const;

/** 运行环境的一行说明。后端字段沿用旧名 bun，实际装的是运行时版本：Go 版本号以 go 开头，其余按 Bun 显示。 */
export function runtimeLabel(runtime: DiagnosticsRuntime): string {
  const version = runtime.bun;
  const engine = version.startsWith("go") ? `Go ${version.slice(2)}` : `Bun ${version}`;
  return `${engine} · ${platformLabel(runtime.platform)} · ${runtime.arch}`;
}

export function platformLabel(platform: string): string {
  switch (platform) {
    case "win32":
    case "windows":
      return "Windows";
    case "darwin":
      return "macOS";
    case "linux":
      return "Linux";
    default:
      return platform;
  }
}
