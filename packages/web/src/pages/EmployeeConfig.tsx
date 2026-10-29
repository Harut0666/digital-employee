// 员工配置页（T11 填充）：岗位说明、工作步骤、表单字段、质检规则、前台 Agent 五个标签页。
// 主 agent 只建空壳并接到 App 上，界面内容由 T11 实现。
import type { EmployeeInfo } from "../api";

export function EmployeeConfigPage({ employees }: { employees: EmployeeInfo[] }) {
  return (
    <div className="chat-body">
      <div className="placeholder-page">
        <h2>员工配置</h2>
        <p>表单式编辑、质检规则模板、配置版本与回滚（T11 实现）。</p>
        <p style={{ fontSize: 12 }}>当前员工：{employees.map((e) => e.id).join("、") || "（无）"}</p>
      </div>
    </div>
  );
}
