// Markdown 交付物的预览（场景 B 的研判简报）：取回 Markdown 正文，按 GFM 渲染表格；
// 简报里图表是旁边 assets 文件夹里的 PNG、用相对路径引用，这里把它们映射成服务端的图表地址。
import { useEffect, useMemo, useState } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import { Alert, Spin } from "antd";
import { assetBaseOf, resolveAssetSrc } from "../view/markdown-assets";
import { COMPONENTS, MARKDOWN_PLUGINS } from "./Markdown";

type Load = { state: "loading" } | { state: "error"; message: string } | { state: "ready"; text: string };

export function MarkdownPreview({ url }: { url: string | undefined }) {
  const [load, setLoad] = useState<Load>({ state: "loading" });
  useEffect(() => {
    if (!url) {
      setLoad({ state: "error", message: "没有可预览的文件（模拟模式没有文件）。" });
      return;
    }
    let alive = true;
    setLoad({ state: "loading" });
    fetch(url)
      .then(async (res) => {
        if (!res.ok) throw new Error(`读取失败（${res.status}）`);
        const text = await res.text();
        if (alive) setLoad({ state: "ready", text });
      })
      .catch((e: unknown) => alive && setLoad({ state: "error", message: e instanceof Error ? e.message : "读取失败" }));
    return () => {
      alive = false;
    };
  }, [url]);

  const base = assetBaseOf(url);
  const components: Components = useMemo(
    () => ({
      ...COMPONENTS,
      img: ({ src, alt }) => <img src={resolveAssetSrc(typeof src === "string" ? src : undefined, base)} alt={alt ?? ""} loading="lazy" />,
    }),
    [base],
  );

  if (load.state === "loading") {
    return (
      <div className="review-empty">
        <Spin />
      </div>
    );
  }
  if (load.state === "error") return <Alert type="warning" showIcon message="预览不可用" description={load.message} />;
  return (
    <div className="md md-preview">
      <ReactMarkdown remarkPlugins={MARKDOWN_PLUGINS} components={components}>
        {load.text}
      </ReactMarkdown>
    </div>
  );
}
