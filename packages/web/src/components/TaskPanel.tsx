import { useEffect, useState } from "react";
import { Button, Progress, Segmented, Tag } from "antd";
import { CheckCircleFilled, CloseCircleFilled, DownloadOutlined, FileTextOutlined, FolderOpenOutlined, LoadingOutlined, MinusCircleOutlined, ClockCircleOutlined, ThunderboltOutlined } from "@ant-design/icons";
import type { TaskSnapshot } from "../api";
import type { SessionView } from "../view/session-view";
import { DocxPreview } from "./DocxPreview";
import { useLinkProps } from "./Preferences";

const fmt = (ms: number) => {
  const s = Math.floor(ms / 1000);
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
};

const KIND = { compute: "计算", judge: "判断", qc: "质检" } as const;
const DELIVERABLE_NAMES: Record<string, string> = { docx: "周报 Word", md: "Markdown 报告", qc_record: "检验记录" };

export function StepIcon({ status }: { status: string }) {
  if (status === "done") return <CheckCircleFilled style={{ color: "var(--primary)" }} />;
  if (status === "failed") return <CloseCircleFilled style={{ color: "var(--danger)" }} />;
  if (status === "running") return <LoadingOutlined style={{ color: "var(--info)" }} />;
  return <MinusCircleOutlined style={{ color: "#b8c0c9" }} />;
}

export function TaskPanel({
  snapshot,
  view,
  downloadUrl,
}: {
  snapshot: TaskSnapshot | undefined;
  view: SessionView;
  downloadUrl: (kind: "docx" | "md" | "qc_record") => string | undefined;
}) {
  const hasDoc = view.deliverables.some((d) => d.kind === "docx");
  const reviewing = view.state === "awaiting_review" || view.state === "delivered";
  const [tab, setTab] = useState<"overview" | "preview">("overview");
  const linkProps = useLinkProps();
  // 到了待审核 / 已交付自动切到预览；其他状态回到概览
  useEffect(() => setTab(reviewing && hasDoc ? "preview" : "overview"), [reviewing, hasDoc, snapshot?.id]);

  // 预算由引擎判定，界面显示以快照为准，不从事件自行推算（重试、退回后预算窗口会重新计算）
  const calls = snapshot?.budget.modelCalls ?? { used: 0, limit: 30 };
  const time = snapshot?.budget.elapsedMs ?? { used: 0, limit: 600_000 };
  const usedCalls = calls.used;
  const usedTime = time.used;

  return (
    <div>
      {hasDoc && (
        <Segmented
          block
          style={{ marginBottom: 12 }}
          value={tab}
          onChange={(v) => setTab(v as "overview" | "preview")}
          options={[
            { label: "概览", value: "overview" },
            { label: "交付物预览", value: "preview" },
          ]}
        />
      )}
      {tab === "preview" && hasDoc ? (
        <>
          <DocxPreview url={downloadUrl("docx")} />
          <div style={{ marginTop: 12 }}>
            {downloadUrl("docx") ? (
              <Button block icon={<DownloadOutlined />} href={downloadUrl("docx")} {...linkProps(downloadUrl("docx"))}>下载</Button>
            ) : (
              <Button block icon={<DownloadOutlined />} disabled>下载（模拟模式无文件）</Button>
            )}
          </div>
        </>
      ) : (
        <>
          <div className="panel-card">
            <h4><ThunderboltOutlined /> 模型调用</h4>
            <div className="stat">{usedCalls} <small>/ {calls.limit}</small></div>
            <Progress percent={Math.min(100, (usedCalls / calls.limit) * 100)} showInfo={false} size="small" strokeColor="#1f8a70" />
          </div>
          <div className="panel-card">
            <h4><ClockCircleOutlined /> 用时</h4>
            <div className="stat">{fmt(usedTime)} <small>/ {fmt(time.limit)}</small></div>
            <Progress percent={Math.min(100, (usedTime / time.limit) * 100)} showInfo={false} size="small" strokeColor="#1f8a70" />
          </div>
          <div className="panel-card">
            <h4>步骤进度</h4>
            {view.steps.length === 0 && <div className="empty-box">尚未开始</div>}
            {view.steps.map((s) => (
              <div key={s.id} className="check-row">
                <StepIcon status={s.status} />
                {s.label}
                <Tag style={{ marginLeft: "auto", marginInlineEnd: 0 }} color={s.kind === "judge" ? "purple" : "default"} variant="filled">{KIND[s.kind]}</Tag>
              </div>
            ))}
          </div>
          <div className="panel-card">
            <h4><FileTextOutlined /> 检验记录</h4>
            {view.qc ? (
              view.qc.checks.map((c) => (
                <div key={c.id} className={`check-row ${c.passed ? "" : "missing"}`}>
                  {c.passed ? <CheckCircleFilled style={{ color: "var(--primary)" }} /> : <CloseCircleFilled />}
                  {c.label}
                </div>
              ))
            ) : (
              <div className="empty-box">尚未质检</div>
            )}
          </div>
          <div className="panel-card">
            <h4><FolderOpenOutlined /> 交付物</h4>
            {view.deliverables.length === 0 ? (
              <div className="empty-box">尚未生成</div>
            ) : (
              view.deliverables.map((d) => (
                <div key={d.kind} className="check-row">
                  {DELIVERABLE_NAMES[d.kind] ?? d.kind}
                  <Button size="small" style={{ marginLeft: "auto" }} icon={<DownloadOutlined />} href={downloadUrl(d.kind)} disabled={!downloadUrl(d.kind)} {...linkProps(downloadUrl(d.kind))}>
                    下载
                  </Button>
                </div>
              ))
            )}
          </div>
        </>
      )}
    </div>
  );
}
