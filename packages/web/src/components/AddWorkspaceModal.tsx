// “添加工作区”弹窗：名称 + 目录。目录输入框旁有“浏览…”，调系统的选择目录对话框（POST /api/workspaces/pick-folder）；
// 选中后路径填进输入框，名称为空时用目录名补全。手填路径仍可用。
import { useEffect, useState } from "react";
import { App as AntApp, Button, Input, Modal } from "antd";
import { FolderOpenOutlined } from "@ant-design/icons";
import type { WorkspaceApi, WorkspaceItem } from "../api/workspaces";
import { applyPickedFolder, pickFolderOutcome, workspaceSubmission, type WorkspaceForm } from "../view/add-workspace";

export function AddWorkspaceModal({
  open,
  hint,
  api,
  onClose,
  onCreated,
}: {
  open: boolean;
  /** 弹窗里多说一句为什么（比如从“新对话”引导过来） */
  hint?: string;
  api: WorkspaceApi;
  onClose: () => void;
  onCreated: (workspace: WorkspaceItem) => void;
}) {
  const { message } = AntApp.useApp();
  const [form, setForm] = useState<WorkspaceForm>({ name: "", path: "" });
  const [picking, setPicking] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      setForm({ name: "", path: "" });
    }
  }, [open]);

  const submission = workspaceSubmission(form);

  const browse = async () => {
    if (picking) return;
    setPicking(true);
    try {
      const outcome = pickFolderOutcome(await api.pickFolder());
      // 取消时不改输入框；失败提示原因，手填路径仍可用
      if (outcome.kind === "picked") setForm((current) => applyPickedFolder(current, outcome.path));
      else if (outcome.kind === "error") message.error(outcome.message);
    } catch (e) {
      message.error(e instanceof Error ? e.message : "选择目录失败，请直接输入目录");
    } finally {
      setPicking(false);
    }
  };

  const save = async () => {
    if (!submission || saving) return;
    setSaving(true);
    try {
      const res = await api.create(submission.name, submission.path);
      if (!res.ok) {
        message.error(res.message);
        return;
      }
      onCreated(res.workspace);
      onClose();
    } catch (e) {
      message.error(e instanceof Error ? e.message : "添加工作区失败");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title="添加工作区"
      okText="添加"
      cancelText="取消"
      okButtonProps={{ disabled: !submission }}
      confirmLoading={saving}
      onOk={() => void save()}
      onCancel={onClose}
      destroyOnHidden
    >
      <div className="ws-dialog">
        {hint && <div className="ws-dialog-hint">{hint}</div>}
        <p className="chat-sub">工作区就是一个本机目录：选一个目录，起个名字。对话里的文件和会话记录放在这个目录里；设置、员工和知识库是所有工作区共用的。</p>
        <div className="ws-field">
          <span>目录</span>
          <div className="ws-field-row">
            <Input
              autoFocus
              value={form.path}
              onChange={(e) => setForm({ ...form, path: e.target.value })}
              onPressEnter={() => void save()}
              placeholder="请选择工作区"
              aria-label="工作区目录"
            />
            <Button icon={<FolderOpenOutlined />} disabled={picking} onClick={() => void browse()}>
              {picking ? "选择中…" : "浏览…"}
            </Button>
          </div>
          <span className="ws-field-tip">不选目录时，会在应用目录下新建一个。</span>
        </div>
        <div className="ws-field">
          <span>名称</span>
          <Input
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            onPressEnter={() => void save()}
            placeholder="请输入工作区名称"
            maxLength={40}
            aria-label="工作区名称"
          />
          <span className="ws-field-tip">不填时使用目录名。</span>
        </div>
      </div>
    </Modal>
  );
}
