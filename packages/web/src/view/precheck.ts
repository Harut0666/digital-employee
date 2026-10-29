// 右栏“准入预检”清单：发起前在浏览器里按已选文件做提示，最终以服务端的准入校验为准。

export interface PrecheckInput {
  period: { year: number; week: number } | undefined;
  /** 销售明细区上传的文件（含单独上传的门店、目标维表） */
  sales: readonly { name: string }[];
  minutes: readonly { name: string }[];
}

export interface PrecheckItem {
  key: "sales" | "stores" | "targets" | "period" | "minutes";
  label: string;
  ok: boolean;
}

const isXlsx = (n: string) => /\.xlsx?$/i.test(n);
const isTable = (n: string) => isXlsx(n) || /\.csv$/i.test(n);

export function computePrecheck(input: PrecheckInput): PrecheckItem[] {
  const names = input.sales.map((f) => f.name);
  // xlsx 工作簿内含维表；CSV 需要按文件名单独提供
  const hasWorkbook = names.some(isXlsx);
  const sales = names.some((n) => isTable(n) && !/门店|目标/.test(n));
  return [
    { key: "sales", label: "销售明细", ok: sales },
    { key: "stores", label: "门店信息", ok: hasWorkbook || names.some((n) => /门店/.test(n)) },
    { key: "targets", label: "销售目标", ok: hasWorkbook || names.some((n) => /目标/.test(n)) },
    { key: "period", label: "周次在数据范围内", ok: input.period !== undefined },
    { key: "minutes", label: "本周纪要", ok: input.minutes.length > 0 },
  ];
}
