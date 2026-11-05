// 审批策略的确认闸门（Spec 0004「审批的诚实边界」、T23）。
//
// `permissions.approvalPolicy` 的消费者：ask = 危险操作先弹确认框；never = 不弹框、
// 直接执行，仍然由服务端写审计（审计永远在服务端，前端不发“取消”请求）。
// 纯函数放在 view/ 下，组件只负责把 modal 与设置里的策略接上。
import type { ReactNode } from "react";
import type { ApprovalPolicy } from "@de/core/contract";

/** 一次危险操作确认请求：文案由调用方提供，确认后执行 onConfirm。 */
export interface DangerConfirmRequest {
  title: string;
  content: ReactNode;
  /** 确认按钮文案：写清后果 */
  okText: string;
  onConfirm: () => void | Promise<void>;
}

export type DangerConfirm = (options: DangerConfirmRequest) => void;

/** 当前策略下是否需要先弹确认框：只有 `ask` 需要；`never` 直接执行。 */
export function shouldConfirm(policy: ApprovalPolicy): boolean {
  return policy === "ask";
}

/**
 * 按审批策略执行一次危险操作：
 *   ask   → 交给 confirm 弹框，确认按钮触发 onConfirm；
 *   never → 不弹框，直接执行（服务端仍会写审计）。
 */
export function runDangerAction(policy: ApprovalPolicy, confirm: DangerConfirm, options: DangerConfirmRequest): void {
  if (shouldConfirm(policy)) {
    confirm(options);
    return;
  }
  void options.onConfirm();
}
