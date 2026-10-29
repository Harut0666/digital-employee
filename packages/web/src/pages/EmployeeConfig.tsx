// 员工配置页（T11）：岗位说明、工作步骤、表单字段、质检规则、前台 Agent 五个标签页，
// 右上角「查看源文件」与「版本历史」，保存后对新任务生效（docs/design.md §4.1、docs/ui/final/05-config.png）。
//
// 数据来源全部是员工目录里的配置文件：表单页改的是解析结果，保存时由服务端校验后写回；
// 没保存前改动留在 drafts 里，切换标签页不会丢。
import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Empty, Select, Space, Spin, Switch, Tabs, Typography } from "antd";
import { HistoryOutlined, SaveOutlined } from "@ant-design/icons";
import type { EmployeeConfigResponse } from "@de/core/contract";
import type { EmployeeInfo } from "../api";
import { ConfigRequestError } from "../api/config";
import { useApi } from "../hooks/data";
import {
  DutiesTab,
  FieldsTab,
  FrontdeskTab,
  QcTab,
  SourceFilesView,
  StepsTab,
  VersionsDrawer,
  type DraftMap,
  type TabProps,
} from "./config";

const TAB_ITEMS = [
  { key: "duties", label: "岗位说明" },
  { key: "steps", label: "工作步骤" },
  { key: "fields", label: "表单字段" },
  { key: "qc", label: "质检规则" },
  { key: "frontdesk", label: "前台 Agent" },
] as const;

export function EmployeeConfigPage({ employees }: { employees: EmployeeInfo[] }) {
  const api = useApi();
  const { message } = App.useApp();
  const [employeeId, setEmployeeId] = useState<string>(employees[0]?.id ?? "weekly-report");
  const [config, setConfig] = useState<EmployeeConfigResponse | undefined>();
  const [loading, setLoading] = useState(false);
  const [drafts, setDrafts] = useState<DraftMap>({});
  const [saving, setSaving] = useState(false);
  const [saveErrors, setSaveErrors] = useState<string[]>([]);
  const [sourceMode, setSourceMode] = useState(false);
  const [versionsOpen, setVersionsOpen] = useState(false);

  // 员工列表异步到达后，选中项可能还不在列表里
  useEffect(() => {
    if (employees.length > 0 && !employees.some((employee) => employee.id === employeeId)) {
      setEmployeeId(employees[0]!.id);
    }
  }, [employees, employeeId]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const payload = await api.getEmployeeConfig(employeeId);
      setConfig(payload);
      setDrafts({});
      setSaveErrors([]);
      if (payload.error) setSourceMode(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : "读取员工配置失败");
    } finally {
      setLoading(false);
    }
  }, [api, employeeId, message]);

  useEffect(() => {
    void load();
  }, [load]);

  const setData = useCallback((path: string, data: unknown) => {
    setDrafts((prev) => ({ ...prev, [path]: { data } }));
  }, []);
  const setContent = useCallback((path: string, content: string) => {
    setDrafts((prev) => ({ ...prev, [path]: { content } }));
  }, []);

  const dirtyPaths = useMemo(() => Object.keys(drafts), [drafts]);

  const save = async () => {
    const entries = Object.entries(drafts);
    if (entries.length === 0) {
      message.info("没有需要保存的改动");
      return;
    }
    setSaving(true);
    setSaveErrors([]);
    const errors: string[] = [];
    for (const [path, input] of entries) {
      try {
        await api.saveEmployeeConfig(employeeId, path, input);
      } catch (error) {
        const detail = error instanceof ConfigRequestError ? error.errors : [];
        errors.push(`${path}：${error instanceof Error ? error.message : String(error)}`, ...detail.map((item) => `· ${item}`));
        break;
      }
    }
    await load();
    setSaving(false);
    if (errors.length > 0) {
      setSaveErrors(errors);
      message.error("保存失败，配置没有被改动");
    } else {
      message.success("已保存，对新任务生效");
    }
  };

  const tabProps: TabProps | undefined = config ? { config, drafts, setData, setContent } : undefined;
  const versionCount = config?.versions.length ?? 0;

  return (
    <div className="chat-body">
      <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
        <h2 style={{ margin: 0, fontSize: 24, flex: "none" }}>员工配置</h2>
        <Select
          style={{ width: 260 }}
          value={employeeId}
          onChange={setEmployeeId}
          options={employees.map((employee) => ({ value: employee.id, label: employee.name }))}
          placeholder="选择员工"
        />
        <div style={{ flex: 1 }} />
        <Space size={10} wrap>
          <Space size={6}>
            <Typography.Text type="secondary">查看源文件</Typography.Text>
            <Switch checked={sourceMode} onChange={setSourceMode} />
          </Space>
          <Button icon={<HistoryOutlined />} onClick={() => setVersionsOpen(true)}>
            版本历史{versionCount > 0 ? ` v${versionCount}` : ""}
          </Button>
          <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={() => void save()} disabled={!config}>
            保存
          </Button>
        </Space>
      </div>

      {config?.error ? (
        <Alert
          type="error"
          showIcon
          message="该员工的配置现在无法加载"
          description={`${config.error}。请打开右上角「查看源文件」修复后保存。`}
        />
      ) : null}
      {config && config.formSchemaErrors.length > 0 ? (
        <Alert type="warning" showIcon message="表单字段用途有问题" description={config.formSchemaErrors.join("；")} />
      ) : null}
      {saveErrors.length > 0 ? (
        <Alert type="error" showIcon message="保存未通过校验，文件没有被改动" description={<pre style={{ margin: 0, whiteSpace: "pre-wrap" }}>{saveErrors.join("\n")}</pre>} />
      ) : null}
      {dirtyPaths.length > 0 ? (
        <Alert
          type="info"
          showIcon
          message={`有 ${dirtyPaths.length} 个文件未保存：${dirtyPaths.join("、")}`}
        />
      ) : null}

      {loading && !config ? (
        <div style={{ padding: 40, textAlign: "center" }}>
          <Spin />
        </div>
      ) : !config || !tabProps ? (
        <Empty description="没有可配置的员工" />
      ) : sourceMode ? (
        <SourceFilesView {...tabProps} />
      ) : (
        <Tabs
          items={TAB_ITEMS.map((item) => {
            switch (item.key) {
              case "duties":
                return { key: item.key, label: item.label, children: <DutiesTab {...tabProps} /> };
              case "steps":
                return { key: item.key, label: item.label, children: <StepsTab {...tabProps} /> };
              case "fields":
                return { key: item.key, label: item.label, children: <FieldsTab {...tabProps} /> };
              case "qc":
                return { key: item.key, label: item.label, children: <QcTab {...tabProps} /> };
              default:
                return { key: item.key, label: item.label, children: <FrontdeskTab {...tabProps} /> };
            }
          })}
        />
      )}

      <Alert
        type="info"
        showIcon
        message="保存后对新任务生效，运行中的任务使用启动时的配置。"
      />

      {config ? <VersionsDrawer open={versionsOpen} employeeId={employeeId} onClose={() => setVersionsOpen(false)} onRestored={() => void load()} /> : null}
    </div>
  );
}
