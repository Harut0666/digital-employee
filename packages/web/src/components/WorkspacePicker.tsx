// 左上角的工作区选择：切换已有工作区或新建一个。切换生效后整页重新加载。
import { useCallback, useEffect, useMemo, useState } from "react";
import { App as AntApp, Dropdown, Input, Modal, Tooltip, type MenuProps } from "antd";
import { CheckOutlined, DownOutlined, FolderOutlined, PlusOutlined } from "@ant-design/icons";
import { IS_MOCK } from "../api";
import { createMockWorkspaceApi, createWorkspaceApi, type WorkspaceList } from "../api/workspaces";
import { switchOutcome, workspacePicker } from "../view/workspace-picker";

const NEW_KEY = "__new__";

export function WorkspacePicker({ workspaceDir }: { workspaceDir?: string }) {
  const { message } = AntApp.useApp();
  const api = useMemo(() => (IS_MOCK ? createMockWorkspaceApi() : createWorkspaceApi()), []);
  const [list, setList] = useState<WorkspaceList>();
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    api.list().then(setList, () => {});
  }, [api]);
  useEffect(reload, [reload]);

  const view = workspacePicker(list, workspaceDir);

  const switchTo = async (id: string) => {
    setBusy(true);
    try {
      const outcome = switchOutcome(await api.switchTo(id));
      if (outcome.kind === "error") {
        message.error(outcome.message);
        return;
      }
      if (outcome.kind === "reload") {
        window.location.reload();
        return;
      }
      reload();
    } catch (e) {
      message.error(e instanceof Error ? e.message : "切换工作区失败");
    } finally {
      setBusy(false);
    }
  };

  const create = async () => {
    setBusy(true);
    try {
      const result = await api.create(name);
      if (!result.ok) {
        message.error(result.message);
        return;
      }
      setCreating(false);
      setName("");
      await switchTo(result.workspace.id);
    } finally {
      setBusy(false);
    }
  };

  const box = (
    <div className={`workspace-pick ${view.canSwitch ? "switchable" : ""}`} aria-label="工作区">
      <FolderOutlined />
      <span className="workspace-name">{view.title}</span>
      {view.canSwitch && <DownOutlined style={{ fontSize: 10, color: "var(--muted)" }} />}
    </div>
  );

  if (!view.canSwitch) {
    return <Tooltip title={view.hint ? `${view.tooltip}（${view.hint}）` : view.tooltip}>{box}</Tooltip>;
  }

  const items: MenuProps["items"] = [
    ...view.items.map((w) => ({
      key: w.id,
      icon: w.current ? <CheckOutlined /> : <span style={{ display: "inline-block", width: 14 }} />,
      label: (
        <Tooltip title={w.path} placement="right">
          <span>{w.name}</span>
        </Tooltip>
      ),
    })),
    { type: "divider" as const },
    { key: NEW_KEY, icon: <PlusOutlined />, label: "新建工作区" },
  ];

  return (
    <>
      <Dropdown
        trigger={["click"]}
        disabled={busy}
        menu={{
          items,
          selectedKeys: list ? [list.current] : [],
          onClick: ({ key }) => {
            if (key === NEW_KEY) setCreating(true);
            else if (key !== list?.current) void switchTo(key);
          },
        }}
      >
        {/* 不套 Tooltip：它会盖住展开的菜单；完整路径在菜单项上悬停可见 */}
        {box}
      </Dropdown>
      <Modal
        title="新建工作区"
        open={creating}
        okText="新建并切换"
        cancelText="取消"
        confirmLoading={busy}
        okButtonProps={{ disabled: !name.trim() }}
        onOk={() => void create()}
        onCancel={() => setCreating(false)}
        destroyOnHidden
      >
        <p style={{ color: "var(--muted)", marginTop: 0 }}>新工作区有自己的设置、员工配置、任务、会话和知识库。有任务在执行时不能切换。</p>
        <Input autoFocus placeholder="工作区名称，如：华南区" maxLength={40} value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => name.trim() && void create()} />
      </Modal>
    </>
  );
}
