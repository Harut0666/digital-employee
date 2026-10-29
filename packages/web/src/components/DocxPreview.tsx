import { useEffect, useRef, useState } from "react";
import { Spin } from "antd";

/** 页面样式的占位预览：没有真实 docx（模拟模式、尚未生成）时使用，数字均为占位。 */
export function PlaceholderReport({ title = "经营周报 · 2026 年第 44 周" }: { title?: string }) {
  const rows: [string, string, string, string, string][] = [
    ["全公司", "12,560,830", "-8.6%", "28.4%", "92.1%"],
    ["华东", "4,812,320", "-6.1%", "29.1%", "94.3%"],
    ["华南", "3,105,660", "-11.8%", "26.7%", "88.5%"],
    ["华北", "2,789,450", "-9.3%", "27.6%", "90.2%"],
  ];
  return (
    <div className="doc-page">
      <h3>{title}</h3>
      <h5>一、本周摘要</h5>
      <div className="ph-line" />
      <div className="ph-line" style={{ width: "86%" }} />
      <div className="ph-line" />
      <div className="ph-line" style={{ width: "70%" }} />
      <h5>二、核心指标</h5>
      <table className="doc-table">
        <thead>
          <tr>
            <th>区域</th>
            <th>实收（元）</th>
            <th>环比</th>
            <th>毛利率</th>
            <th>目标达成率</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r[0]}>
              <td>{r[0]}</td>
              <td>{r[1]}</td>
              <td className="neg">{r[2]}</td>
              <td>{r[3]}</td>
              <td>{r[4]}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h5>各区域实收对比（元）</h5>
      <div className="bars">
        {[90, 62, 48, 44, 30].map((h, i) => (
          <span key={i} style={{ height: `${h}%` }} />
        ))}
      </div>
      <div className="chat-sub" style={{ marginTop: 8 }}>占位预览：数据均为示意</div>
    </div>
  );
}

/** Word 预览：用 docx-preview 渲染真实 .docx；没有 url 或加载失败时退回占位。 */
export function DocxPreview({ url }: { url: string | undefined }) {
  const host = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<"idle" | "loading" | "ready" | "failed">("idle");

  useEffect(() => {
    if (!url || !host.current) {
      setState("idle");
      return;
    }
    let alive = true;
    setState("loading");
    (async () => {
      try {
        const res = await fetch(url);
        if (!res.ok) throw new Error(String(res.status));
        const buf = await res.arrayBuffer();
        const { renderAsync } = await import("docx-preview");
        if (!alive || !host.current) return;
        host.current.innerHTML = "";
        await renderAsync(buf, host.current, undefined, { className: "docx", inWrapper: true, ignoreWidth: false });
        if (alive) setState("ready");
      } catch {
        if (alive) setState("failed");
      }
    })();
    return () => {
      alive = false;
    };
  }, [url]);

  const showPlaceholder = state === "idle" || state === "failed";
  return (
    <div>
      {state === "loading" && <Spin style={{ display: "block", margin: 20 }} />}
      <div ref={host} className="docx-host" style={{ display: state === "ready" || state === "loading" ? "block" : "none" }} />
      {showPlaceholder && <PlaceholderReport />}
      {state === "failed" && <div className="chat-sub">真实文档暂时无法加载，显示占位预览。</div>}
    </div>
  );
}
