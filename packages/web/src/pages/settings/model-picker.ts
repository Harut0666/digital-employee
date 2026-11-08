// 拉取模型的勾选逻辑（T37）：把 /models 的拉取结果与连接的已有模型列表合并成勾选行，
// 再把勾选结果写回连接的模型列表。纯函数，视图层只负责渲染。
//
// 规则：
//   - 已在列表里的模型默认勾上，顺序按原列表；
//   - 新拉取到的模型默认不勾，按拉取顺序排在后面；
//   - 原列表里这次没拉到的模型和“已删除”（模型列表里有、但已不在列表中）的模型单独标出，
//     不会因为一次拉取就被悄悄删掉；删掉它们要人自己取消勾选后保存。

export interface ModelOption {
  /** 模型 ID */
  id: string;
  /** 连接当前的模型列表里有它 */
  inList: boolean;
  /** 勾选框状态 */
  checked: boolean;
  /** 拉取过一次，但这次没拉到；仍保留在列表里 */
  retired: boolean;
}

/**
 * 合并拉取结果与已有模型列表。
 * @param fetched 本次拉到的模型 ID（可含重复与空白）
 * @param current 连接当前的模型列表
 * @param previousRetired 上一次拉取后标为“已删除”的模型（重新拉取时保留，避免勾选丢失）
 */
export function mergeFetchedModels(fetched: readonly string[], current: readonly string[], previousRetired: readonly string[] = []): ModelOption[] {
  const clean = (ids: readonly string[]): string[] => {
    const out: string[] = [];
    for (const id of ids) {
      const trimmed = id.trim();
      if (trimmed && !out.includes(trimmed)) out.push(trimmed);
    }
    return out;
  };
  const fetchedIDs = clean(fetched);
  const currentIDs = clean(current);
  const retiredIDs = clean(previousRetired).filter((id) => !currentIDs.includes(id) && !fetchedIDs.includes(id));

  const rows: ModelOption[] = [];
  for (const id of currentIDs) {
    const stillOffered = fetchedIDs.includes(id);
    rows.push({ id, inList: true, checked: true, retired: !stillOffered });
  }
  for (const id of fetchedIDs) {
    if (currentIDs.includes(id)) continue;
    rows.push({ id, inList: false, checked: false, retired: false });
  }
  for (const id of retiredIDs) {
    rows.push({ id, inList: false, checked: false, retired: true });
  }
  return rows;
}

/** 切换一行的勾选；未知 ID 原样返回。 */
export function toggleModelSelection(rows: readonly ModelOption[], id: string): ModelOption[] {
  return rows.map((row) => (row.id === id ? { ...row, checked: !row.checked } : row));
}

/** 勾选结果 → 连接的模型列表：按勾选列表的显示顺序（原列表在前、新模型按拉取顺序在后）。 */
export function selectedModels(rows: readonly ModelOption[]): string[] {
  const known = rows.filter((row) => row.inList && row.checked && !row.retired).map((row) => row.id);
  const added = rows.filter((row) => !row.inList && row.checked && !row.retired).map((row) => row.id);
  return [...known, ...added];
}

export interface ModelCheckOption {
  value: string;
  label: string;
}

/** antd Checkbox.Group 的选项：给“已在列表 / 已删除”加标注。 */
export function modelCheckOptions(rows: readonly ModelOption[]): ModelCheckOption[] {
  return rows.map((row) => ({
    value: row.id,
    label: row.inList ? `${row.id}（已在列表）` : row.retired ? `${row.id}（已删除）` : row.id,
  }));
}
