// 步骤入参里的 $ 引用解析（语法见 contract/employee-config.ts）：
//   $form.<path>  $steps.<stepId>.<path>  $rendered.<stepId>
//   $task.dir  $task.attachments  $task.feedback  $task.idempotencyKey  $config.<path>
// 路径段 "[*]" 对数组逐项取值；引用不存在（含步骤尚未执行）解析为 null；其余值原样传入。

export interface RefContext {
  form: Record<string, unknown>;
  taskDir: string;
  attachments: unknown;
  feedback: unknown;
  qcConfig: unknown;
  /** 某步骤最近一次完成的输出；没有时返回 undefined */
  stepOutput(stepId: string): unknown;
  /** 某步骤输出的报告草稿，已完成槽位渲染；没有时返回 undefined */
  renderedOutput(stepId: string): unknown;
  /** 幂等键占位符；引擎在得到入参哈希后再替换成真正的键 */
  idempotencyKey: string;
}

const REF_PATTERN = /^\$(form|steps|rendered|task|config)(\..+)?$/;

type Token = string | { index: number | "*" };

function tokenize(path: string): Token[] {
  const tokens: Token[] = [];
  for (const part of path.split(".")) {
    const match = /^([^[\]]*)((?:\[(?:\*|\d+)\])*)$/.exec(part);
    if (!match) {
      tokens.push(part);
      continue;
    }
    if (match[1]) tokens.push(match[1]);
    for (const bracket of match[2]!.matchAll(/\[(\*|\d+)\]/g)) {
      tokens.push({ index: bracket[1] === "*" ? "*" : Number(bracket[1]) });
    }
  }
  return tokens;
}

function walk(value: unknown, tokens: Token[]): unknown {
  if (tokens.length === 0) return value ?? null;
  if (value === null || value === undefined) return null;
  const [head, ...rest] = tokens as [Token, ...Token[]];
  if (typeof head === "string") {
    if (typeof value !== "object") return null;
    return walk((value as Record<string, unknown>)[head], rest);
  }
  if (!Array.isArray(value)) return null;
  if (head.index === "*") return value.map((item) => walk(item, rest));
  return walk(value[head.index], rest);
}

function resolveRef(ref: string, ctx: RefContext): unknown {
  const match = REF_PATTERN.exec(ref);
  if (!match) return ref;
  const root = match[1]!;
  const path = match[2] ? match[2].slice(1) : "";
  const tokens = path ? tokenize(path) : [];
  switch (root) {
    case "form":
      return walk(ctx.form, tokens);
    case "config":
      return walk(ctx.qcConfig, tokens);
    case "task": {
      const [first, ...rest] = tokens;
      if (first === "dir") return ctx.taskDir;
      if (first === "attachments") return walk(ctx.attachments, rest);
      if (first === "feedback") return walk(ctx.feedback, rest);
      if (first === "idempotencyKey") return ctx.idempotencyKey;
      return null;
    }
    case "steps":
    case "rendered": {
      const [stepId, ...rest] = tokens;
      if (typeof stepId !== "string") return null;
      const output = root === "steps" ? ctx.stepOutput(stepId) : ctx.renderedOutput(stepId);
      return walk(output, rest);
    }
    default:
      return ref;
  }
}

export function resolveInput(value: unknown, ctx: RefContext): unknown {
  if (typeof value === "string") return resolveRef(value, ctx);
  if (Array.isArray(value)) return value.map((v) => resolveInput(v, ctx));
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([k, v]) => [k, resolveInput(v, ctx)]));
  }
  return value;
}

/** 把入参里的字符串占位符整体替换为真值（只替换完全相等的字符串）。 */
export function replaceString(value: unknown, from: string, to: string): unknown {
  if (typeof value === "string") return value === from ? to : value;
  if (Array.isArray(value)) return value.map((v) => replaceString(v, from, to));
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([k, v]) => [k, replaceString(v, from, to)]));
  }
  return value;
}
