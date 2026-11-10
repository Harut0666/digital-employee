// 右栏标题行右端的“关闭”按钮（交付物、补充材料、待开工三种右栏共用）
import { Button, Tooltip } from "antd";
import { CloseOutlined } from "@ant-design/icons";

export function PanelClose({ onClose }: { onClose: () => void }) {
  return (
    <Tooltip title="关闭右栏">
      <Button className="panel-close" type="text" size="small" icon={<CloseOutlined />} aria-label="关闭右栏" onClick={onClose} />
    </Tooltip>
  );
}
