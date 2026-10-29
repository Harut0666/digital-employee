// 计算步骤入参里的 $ 引用解析：
//   $form.<path>  $steps.<stepId>.<path>  $task.dir  $config.<path>
// 其余值（包括以 $ 开头但不属于这四类的字符串）原样传入。

export interface RefContext {
  form: Record<string, unknown>;
  taskDir: string;
  qcConfig: unknown;
  /** 取某步骤的输出；步骤尚无输出时抛错 */
  stepOutput(stepId: string): unknown;
}

const REF_PATTERN = /^\$(form|steps|task|config)(\..+)?$/;

function walk(value: unknown, path: string[]): unknown {
  let current = value;
  for (const key of path) {
    if (current === null || current === undefined) return undefined;
    if (Array.isArray(current)) {
      current = current[Number(key)];
    } else if (typeof current === "object") {
      current = (current as Record<string, unknown>)[key];
    } else {
      return undefined;
    }
  }
  return current;
}

function resolveRef(ref: string, ctx: RefContext): unknown {
  const match = REF_PATTERN.exec(ref);
  if (!match) return ref;
  const root = match[1]!;
  const path = match[2] ? match[2].slice(1).split(".") : [];
  switch (root) {
    case "form":
      return walk(ctx.form, path);
    case "config":
      return walk(ctx.qcConfig, path);
    case "task": {
      if (path.length === 1 && path[0] === "dir") return ctx.taskDir;
      throw new Error(`无法解析引用 ${ref}：$task 只支持 $task.dir`);
    }
    case "steps": {
      const [stepId, ...rest] = path;
      if (!stepId) throw new Error(`无法解析引用 ${ref}：缺少步骤 id`);
      return walk(ctx.stepOutput(stepId), rest);
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
