// 危险操作确认框（Spec 0004「危险操作」、T23）。
//
// 统一入口：模拟崩溃、恢复默认员工配置、导入设置、导出含密钥、切完全访问 /
// 从不询问都走这里。确认按钮文案写清后果（例如“仍要导入”）；取消只关闭弹窗，
// 按约定不发请求，因此服务端不会留下“取消”审计（服务端只知道成功/失败）。
import { App as AntApp } from "antd";

export interface DangerConfirmOptions {
  title: string;
  content: React.ReactNode;
  /** 确认按钮文案：写清后果 */
  okText: string;
  onConfirm: () => void | Promise<void>;
}

/** 返回一个 confirm 函数；在组件里调用 `const confirmDanger = useDangerConfirm()`。 */
export function useDangerConfirm(): (options: DangerConfirmOptions) => void {
  const { modal } = AntApp.useApp();
  return (options) => {
    // 用 Modal.confirm 而不是 window.confirm：样式与字号跟随主题，也方便写长文案
    void modal.confirm({
      title: options.title,
      content: options.content,
      okText: options.okText,
      cancelText: "取消",
      okButtonProps: { danger: true },
      centered: true,
      onOk: options.onConfirm,
    });
  };
}
