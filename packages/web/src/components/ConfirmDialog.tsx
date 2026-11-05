// 危险操作确认框（Spec 0004「危险操作」、T23）。
//
// 统一入口：模拟崩溃、恢复默认员工配置、导入设置、导出含密钥、切完全访问 /
// 从不询问都走这里。是否弹框由设置里的 `permissions.approvalPolicy` 决定：
//   ask   → 先弹确认框，确认按钮文案写清后果（例如“仍要导入”），取消不发请求；
//   never → 不弹框、直接执行，服务端仍会写审计（服务端只知道成功/失败）。
import { App as AntApp } from "antd";
import { useApprovalPolicy } from "./ApprovalPolicy";
import { runDangerAction, type DangerConfirmRequest } from "../view/approval";

export type DangerConfirmOptions = DangerConfirmRequest;

/** 返回一个 confirm 函数；在组件里调用 `const confirmDanger = useDangerConfirm()`。 */
export function useDangerConfirm(): (options: DangerConfirmOptions) => void {
  const { modal } = AntApp.useApp();
  const policy = useApprovalPolicy();
  return (options) => {
    runDangerAction(policy, (request) => {
      // 用 Modal.confirm 而不是 window.confirm：样式与字号跟随主题，也方便写长文案
      void modal.confirm({
        title: request.title,
        content: request.content,
        okText: request.okText,
        cancelText: "取消",
        okButtonProps: { danger: true },
        centered: true,
        onOk: request.onConfirm,
      });
    }, options);
  };
}
