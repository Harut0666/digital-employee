// 审批策略的运行时（Spec 0004、T23）：App 启动时从设置文档读取，供确认框按策略决定是否弹框。
//
// 权限设置页保存或导入后调用 `setPolicy` 同步，避免“刚把审批改成从不询问，确认框却还按旧值弹”。
import { createContext, useContext, useMemo, useState } from "react";
import type { ApprovalPolicy } from "@de/core/contract";

export interface ApprovalPolicyValue {
  policy: ApprovalPolicy;
  setPolicy: (policy: ApprovalPolicy) => void;
}

/** 默认按最保守的 ask：即使忘记包 Provider，也不会悄悄跳过确认。 */
const ApprovalPolicyContext = createContext<ApprovalPolicyValue>({ policy: "ask", setPolicy: () => undefined });

export function ApprovalPolicyProvider({
  initialPolicy,
  onPolicyChange,
  children,
}: {
  initialPolicy: ApprovalPolicy;
  /** 权限页保存后回写 App 层，保证重新挂载时用最新值 */
  onPolicyChange?: (policy: ApprovalPolicy) => void;
  children: React.ReactNode;
}) {
  const [policy, setPolicyState] = useState<ApprovalPolicy>(initialPolicy);
  const value = useMemo<ApprovalPolicyValue>(
    () => ({
      policy,
      setPolicy: (next) => {
        setPolicyState(next);
        onPolicyChange?.(next);
      },
    }),
    [policy, onPolicyChange],
  );
  return <ApprovalPolicyContext.Provider value={value}>{children}</ApprovalPolicyContext.Provider>;
}

export function useApprovalPolicy(): ApprovalPolicy {
  return useContext(ApprovalPolicyContext).policy;
}

/** 权限设置页保存后同步确认框使用的策略。 */
export function useSetApprovalPolicy(): (policy: ApprovalPolicy) => void {
  return useContext(ApprovalPolicyContext).setPolicy;
}
