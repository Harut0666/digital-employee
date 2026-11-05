// 设置页（T10）：模型连接、执行与预算（T22）、演示与调试、诊断与关于（T22）、工作区。
// 后续 ticket（T21 外观与交互、T23 权限与审批）在同一份 NAV 里各加一项。
import { useMemo, useState } from "react";
import { ClockCircleOutlined, DatabaseOutlined, FolderOutlined, InfoCircleOutlined, PlayCircleOutlined } from "@ant-design/icons";
import { createSettingsApi } from "../api/settings";
import { DemoDebug } from "./settings/DemoDebug";
import { DiagnosticsSection } from "./settings/DiagnosticsSection";
import { ExecutionSection } from "./settings/ExecutionSection";
import { ModelConnections } from "./settings/ModelConnections";
import { WorkspaceSection } from "./settings/WorkspaceSection";

type Section = "models" | "execution" | "demo" | "diagnostics" | "workspace";

const NAV: { key: Section; label: string; icon: React.ReactNode }[] = [
  { key: "models", label: "模型连接", icon: <DatabaseOutlined /> },
  { key: "execution", label: "执行与预算", icon: <ClockCircleOutlined /> },
  { key: "demo", label: "演示与调试", icon: <PlayCircleOutlined /> },
  { key: "diagnostics", label: "诊断与关于", icon: <InfoCircleOutlined /> },
  { key: "workspace", label: "工作区", icon: <FolderOutlined /> },
];

export function SettingsPage() {
  const api = useMemo(() => createSettingsApi(), []);
  const [section, setSection] = useState<Section>("models");

  return (
    <div style={{ display: "flex", flex: 1, minHeight: 0 }}>
      {/* 设置页内部的二级导航（全局左栏已有员工/会话/入口） */}
      <div style={{ width: 200, flex: "none", borderRight: "1px solid var(--line)", padding: "18px 10px", display: "flex", flexDirection: "column", gap: 4 }}>
        <h1 style={{ fontSize: 26, margin: "0 10px 18px" }}>设置</h1>
        {NAV.map((item) => {
          const active = item.key === section;
          return (
            <div
              key={item.key}
              onClick={() => setSection(item.key)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 10,
                padding: "10px 12px",
                borderRadius: 10,
                cursor: "pointer",
                color: active ? "var(--primary)" : "var(--text)",
                background: active ? "var(--primary-soft)" : "transparent",
                fontWeight: active ? 600 : 400,
              }}
            >
              {item.icon}
              {item.label}
            </div>
          );
        })}
      </div>
      <div style={{ flex: 1, minWidth: 0, overflow: "auto", padding: "20px 32px 32px" }}>
        {section === "models" && <ModelConnections api={api} />}
        {section === "execution" && <ExecutionSection api={api} />}
        {section === "demo" && <DemoDebug api={api} />}
        {section === "diagnostics" && <DiagnosticsSection api={api} />}
        {section === "workspace" && <WorkspaceSection api={api} />}
      </div>
    </div>
  );
}
