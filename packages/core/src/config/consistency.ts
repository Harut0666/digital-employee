// 工作流配置的一致性检查（docs/design.md §4、ADR-0001）。
// 加载员工配置与保存 workflow.yaml 时共用，保证“保存时能过、启动时过不了”的配置不会被写回。

import type { EmployeeWorkflow } from "../contract";

/** 返回一致性问题的中文说明；空数组表示没有问题。 */
export function workflowConsistencyProblems(workflow: EmployeeWorkflow): string[] {
  const problems: string[] = [];
  const ids = workflow.steps.map((s) => s.id);
  if (new Set(ids).size !== ids.length) problems.push("workflow.yaml 中的步骤 id 不唯一");

  const admission = workflow.steps.find((s) => s.id === workflow.admissionStep);
  if (!admission) problems.push(`admissionStep "${workflow.admissionStep}" 不在 workflow.yaml 的步骤中`);
  else if (admission.kind !== "compute") problems.push(`admissionStep "${admission.id}" 必须是计算步骤`);

  const qcSteps = workflow.steps.filter((s) => s.kind === "qc");
  if (qcSteps.length > 1) problems.push("workflow.yaml 最多只能有一个质检步骤");
  if (qcSteps[0] && workflow.steps[workflow.steps.length - 1] !== qcSteps[0]) problems.push("质检步骤必须是最后一个步骤");

  // 质检修正步骤的解析值（含默认值）必须指向真实步骤：非场景 A 的员工若不显式配置，
  // 质检问题就落不到可重跑的步骤上，只能一路转人工，因此加载配置时直接报错。
  if (qcSteps.length > 0) {
    const fixSteps: Array<["write" | "causes", string]> = [
      ["write", workflow.qcFixSteps.write ?? workflow.qcSources.draft],
      ["causes", workflow.qcFixSteps.causes],
    ];
    for (const [key, id] of fixSteps) {
      if (!ids.includes(id)) {
        problems.push(`qcFixSteps.${key} 解析为 "${id}"，但不在 workflow.yaml 的步骤中；请显式配置 qcFixSteps.${key}`);
      }
    }
  }
  return problems;
}
