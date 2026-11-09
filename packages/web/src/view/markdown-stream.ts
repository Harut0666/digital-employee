// 流式 Markdown 的两个纯函数（不依赖 React）：
// - closeOpenMarkdown：模型还在往外吐字时，文本末尾常有没写完的结构（围栏、`、**、链接、表格分隔行……）。
//   渲染前把它们临时补全（或去掉半截），界面上不会闪成怪样；真正的文本不改，下一帧用更长的文本重新补。
// - splitBlocks：按块切分，已经“稳定”的前面块字符串不变，渲染层据此 memo，每帧只重渲染最后一两个块。

const FENCE_OPEN = /^( {0,3})(`{3,}|~{3,})(.*)$/;
const LIST_ITEM = /^\s{0,3}([-*+]|\d{1,9}[.)])\s/;

/** 补全说到一半的 Markdown，返回可以安全渲染的文本。 */
export function closeOpenMarkdown(text: string): string {
  if (!text) return text;
  const lines = text.split("\n");

  // ---- 1. 围栏：逐行扫描，记下是否停在围栏里，以及最后一段“正文”从哪一行开始 ----
  let fence: { char: string; len: number } | undefined;
  let tailStart = 0;
  lines.forEach((line, index) => {
    if (fence) {
      const close = line.match(/^ {0,3}(`{3,}|~{3,})\s*$/);
      if (close && close[1]![0] === fence.char && close[1]!.length >= fence.len) {
        fence = undefined;
        tailStart = index + 1;
      }
      return;
    }
    const open = line.match(FENCE_OPEN);
    // 反引号围栏的信息串里不能再有反引号，否则是行内代码
    if (open && !(open[2]![0] === "`" && open[3]!.includes("`"))) {
      fence = { char: open[2]![0]!, len: open[2]!.length };
      return;
    }
    if (line.trim() === "") tailStart = index + 1;
  });

  const lastIndex = lines.length - 1;
  const last = lines[lastIndex] ?? "";
  if (fence) {
    // 闭合行只写了前一两个反引号：先去掉，等写完
    if (fence.char === "`" && /^ {0,3}`{1,2}$/.test(last)) lines[lastIndex] = "";
    const body = lines.join("\n");
    return `${body}${body.endsWith("\n") ? "" : "\n"}${fence.char.repeat(fence.len)}`;
  }

  // ---- 2. 整行的半截结构 ----
  // 开围栏只写了前一两个反引号
  if (/^ {0,3}`{1,2}$/.test(last)) {
    lines[lastIndex] = "";
    return closeOpenMarkdown(lines.join("\n"));
  }
  const prev = lines[lastIndex - 1];
  // 表格分隔行写到一半：补齐列数，免得整张表塌回段落
  if (lastIndex >= 1 && prev !== undefined && prev.includes("|") && /^[\s|:-]+$/.test(last) && last.includes("-") && lastIndex - 1 >= tailStart) {
    const cells = (line: string) => {
      const trimmed = line.trim().replace(/^\|/, "").replace(/\|$/, "");
      return trimmed.split("|");
    };
    const header = cells(prev).length;
    const delimiter = cells(last).map((cell) => (/^\s*:?-+:?\s*$/.test(cell) ? cell.trim() : "---"));
    while (delimiter.length < header) delimiter.push("---");
    lines[lastIndex] = `|${delimiter.join("|")}|`;
    return lines.join("\n");
  }
  // 段落下一行只有 - 或 =：会被当成标题下划线，先去掉
  if (lastIndex >= 1 && prev !== undefined && prev.trim() !== "" && /^ {0,3}(-+|=+)\s*$/.test(last) && lastIndex - 1 >= tailStart && !FENCE_OPEN.test(prev) && !LIST_ITEM.test(prev)) {
    lines[lastIndex] = "";
    return lines.join("\n");
  }

  // ---- 3. 最后一段的行内结构 ----
  if (tailStart >= lines.length) return text;
  const tail = closeInline(lines.slice(tailStart).join("\n"));
  return [...lines.slice(0, tailStart), tail].join("\n");}

/** 补全一段文字里没写完的行内结构：链接半截、行内代码、强调。 */
function closeInline(tail: string): string {
  const text = cutOpenLink(tail);
  const isSpace = (ch: string | undefined) => ch === undefined || /\s/.test(ch);
  const isWord = (ch: string | undefined) => ch !== undefined && /[\p{L}\p{N}]/u.test(ch);

  const stack: { char: string; len: number; at: number }[] = [];
  let code: { len: number; at: number } | undefined;
  let out = text;
  let i = 0;
  while (i < text.length) {
    const ch = text[i]!;
    if (ch === "\\") {
      i += 2;
      continue;
    }
    if (ch === "`") {
      let j = i;
      while (text[j] === "`") j++;
      const len = j - i;
      if (!code) code = { len, at: i };
      else if (code.len === len) code = undefined;
      i = j;
      continue;
    }
    if (code) {
      i++;
      continue;
    }
    if (ch === "*" || ch === "_" || ch === "~") {
      let j = i;
      while (text[j] === ch) j++;
      const run = j - i;
      if (ch === "~" && run < 2) {
        i = j;
        continue;
      }
      const before = text[i - 1];
      const after = text[j];
      const canOpen = !isSpace(after) && (ch !== "_" || !isWord(before));
      const canClose = !isSpace(before) && (ch !== "_" || !isWord(after));
      // 一个分隔符串按 1 或 2 个一组处理（*** 是 ** 加 *）；删除线只认两个
      let left = ch === "~" ? 2 : run;
      const atEnd = j === text.length;
      if (canClose) {
        while (left > 0) {
          const size = ch === "~" ? 2 : left >= 2 && stack.some((s) => s.char === ch && s.len === 2) ? 2 : 1;
          const found = stack.map((s) => s.char === ch && s.len === size).lastIndexOf(true);
          if (found < 0) break;
          stack.length = found;
          left -= size;
        }
      }
      if (left > 0 && canOpen) {
        while (left > 0) {
          const size = ch === "~" ? 2 : left >= 2 ? 2 : 1;
          stack.push({ char: ch, len: size, at: i });
          left -= size;
        }
      } else if (left > 0 && atEnd && stack.some((s) => s.char === ch)) {
        // 结尾处闭合标记只写了一半（**粗体*）：去掉半截，下面统一补全
        out = text.slice(0, i);
      }
      i = j;
      continue;
    }
    i++;
  }

  let result = out;
  if (code) {
    // 刚写下开头的反引号、后面还没有内容：先去掉
    if (code.at + code.len >= text.length) {
      result = result.slice(0, code.at);
      // 去掉之后，这个位置之后的强调标记也不再有效
      return closeInline(result);
    }
    result += "`".repeat(code.len);
  }
  for (const open of [...stack].reverse()) {
    // out 被截断时，截断位置之后的标记已经不在文本里
    if (open.at >= out.length) continue;
    result += open.char.repeat(open.len);
  }
  return result;
}

/** 链接或图片的地址写到一半（[文字](http://exa）：去掉 [ 和半截地址，只留文字。 */
function cutOpenLink(text: string): string {
  const at = text.lastIndexOf("](");
  if (at < 0 || text.indexOf(")", at) >= 0) return text;
  const open = text.lastIndexOf("[", at);
  if (open < 0) return text;
  const bang = open > 0 && text[open - 1] === "!" ? open - 1 : open;
  return text.slice(0, bang) + text.slice(open + 1, at);
}

/** 按块切分（空行分隔，围栏里的空行不分；列表里的空行不拆开）。块之间用空行连回去等于原文的块结构。 */
export function splitBlocks(text: string): string[] {
  const raw: string[] = [];
  let current: string[] = [];
  let fence: { char: string; len: number } | undefined;
  const flush = () => {
    if (current.length > 0) raw.push(current.join("\n"));
    current = [];
  };
  for (const line of text.split("\n")) {
    if (fence) {
      current.push(line);
      const close = line.match(/^ {0,3}(`{3,}|~{3,})\s*$/);
      if (close && close[1]![0] === fence.char && close[1]!.length >= fence.len) fence = undefined;
      continue;
    }
    const open = line.match(FENCE_OPEN);
    if (open && !(open[2]![0] === "`" && open[3]!.includes("`"))) {
      fence = { char: open[2]![0]!, len: open[2]!.length };
      current.push(line);
      continue;
    }
    if (line.trim() === "") flush();
    else current.push(line);
  }
  flush();

  // 同一个列表的各项和缩进内容并成一块
  const blocks: string[] = [];
  for (const block of raw) {
    const before = blocks.at(-1);
    const continues = /^( {2,}|\t)/.test(block) || LIST_ITEM.test(block);
    if (before !== undefined && LIST_ITEM.test(before) && continues) blocks[blocks.length - 1] = `${before}\n\n${block}`;
    else blocks.push(block);
  }
  return blocks;
}
