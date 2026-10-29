// 设置页（T10）：模型连接、演示与调试、工作区三个子页（docs/ui/final/07-settings.png）。
import { useMemo, useState } from "react";
import { DatabaseOutlined, FolderOutlined, PlayCircleOutlined } from "@ant-design/icons";
import { createSettingsApi } from "../api/settings";
import { DemoDebug } from "./settings/DemoDebug";
import { ModelConnections } from "./settings/ModelConnections";
import { WorkspaceSection } from "./settings/WorkspaceSection";

type Section = "models" | "demo" | "workspace";

const NAV: { key: Section; label: string; icon: React.ReactNode }[] = [
  { key: "models", label: "模型连接", icon: <DatabaseOutlined /> },
  { key: "demo", label: "演示与调试", icon: <PlayCircleOutlined /> },
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
        {section === "demo" && <DemoDebug api={api} />}
        {section === "workspace" && <WorkspaceSection api={api} />}
      </div>
    </div>
  );
}
