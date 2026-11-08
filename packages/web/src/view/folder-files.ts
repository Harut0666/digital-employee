// 输入框里的 @ 选目录文件（T41）：纯函数，不依赖 React 与网络。
// 光标前最近的一个 @ 词触发候选列表；选中后把那一个 @词 换成 @路径，人按发送才算确认。

/** 光标前正在输入的 @词；没有在输入 @词时返回 undefined。词的结尾之后只允许空格（人已经接着打字的不算）。 */
export function atQuery(text: string, caret: number): string | undefined {
  const before = text.slice(0, Math.max(0, Math.min(caret, text.length)));
  const at = before.lastIndexOf("@");
  if (at < 0) return undefined;
  // @ 前面必须是开头或空白，e.g. 邮箱、价格里的 @ 不算
  if (at > 0 && !/\s/.test(before[at - 1]!)) return undefined;
  const word = before.slice(at + 1);
  if (/\s/.test(word)) return undefined;
  return word;
}

/** 按子串过滤目录文件（大小写不敏感）；词为空时全部列出。 */
export function filterFolderPaths(paths: readonly string[], word: string): string[] {
  const needle = word.trim().toLowerCase();
  return paths.filter((path) => needle === "" || path.toLowerCase().includes(needle));
}

/** 选中一个文件：把光标所在的那个 @词 换成 @路径 ，返回新文本和光标位置。 */
export function insertAtPath(text: string, caret: number, path: string): { text: string; caret: number } {
  const position = Math.max(0, Math.min(caret, text.length));
  const at = text.lastIndexOf("@", position - 1);
  const wordStart = Math.max(at, 0);
  let start = wordStart;
  while (start > 0 && !/\s/.test(text[start - 1]!)) start--;
  const tail = text.slice(position);
  // 替换完后面没有字就不留空格（一按发送就发出去了）；后面还有字时补一格分开
  const spacer = tail === "" ? "" : tail.startsWith(" ") ? "" : " ";
  const inserted = `@${path}${spacer}`;
  const next = text.slice(0, start) + inserted + tail;
  return { text: next, caret: start + inserted.length };
}
