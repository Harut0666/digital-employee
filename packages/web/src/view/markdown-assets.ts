// Markdown 简报里的图表路径映射。纯函数。
// 简报里图表写成相对路径 assets/<文件名>（交付到“交付”文件夹后在本机任何 Markdown 查看器里都能打开）；
// 在界面里预览时，改成服务端的图表地址 /api/tasks/<任务>/assets/<文件名>。

/** 从交付物下载地址（…/api/tasks/<id>/deliverables/md[?version=n]）得到图表地址的前缀（…/api/tasks/<id>/assets/）。 */
export function assetBaseOf(deliverableUrl: string | undefined): string | undefined {
  if (!deliverableUrl) return undefined;
  const match = /^(.*\/api\/tasks\/[^/?#]+)\/deliverables\/md(?:[?#].*)?$/.exec(deliverableUrl);
  return match ? `${match[1]}/assets/` : undefined;
}

/** 相对路径 assets/x.png → 图表地址；别的地址（绝对地址、data:）原样返回；没有前缀时也原样返回。 */
export function resolveAssetSrc(src: string | undefined, base: string | undefined): string | undefined {
  if (!src) return src;
  const match = /^(?:\.\/)?assets\/([^/\\?#]+)$/.exec(src);
  if (!match || !base) return src;
  return base + encodeURIComponent(decodeURIComponent(match[1] ?? ""));
}
