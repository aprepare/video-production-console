/** 句级 diff：把口播正文按句切开做 LCS，标出审稿删了哪句、加了哪句。 */

export type DiffOp = { type: "same" | "del" | "add"; text: string };

/**
 * 按中文句末标点和换行切句，标点和换行留在句尾，拼回去就是原文。
 * 段落之间的空行会成为独立的 "\n" 句，渲染时用 pre-wrap 还原分段。
 */
export function splitSentences(text: string): string[] {
  const out: string[] = [];
  let buf = "";
  for (const ch of text) {
    buf += ch;
    if ("。！？；!?\n".includes(ch)) {
      out.push(buf);
      buf = "";
    }
  }
  if (buf) out.push(buf);
  return out;
}

/** 比较时忽略首尾空白，改了个空格不算改。 */
function key(sentence: string): string {
  return sentence.trim();
}

export function diffSentences(before: string, after: string): DiffOp[] {
  const a = splitSentences(before);
  const b = splitSentences(after);
  const n = a.length;
  const m = b.length;
  // LCS 长度表：dp[i][j] = a[i:] 与 b[j:] 的最长公共句数。
  const dp: Uint16Array[] = [];
  for (let i = 0; i <= n; i += 1) dp.push(new Uint16Array(m + 1));
  for (let i = n - 1; i >= 0; i -= 1) {
    const ka = key(a[i]);
    for (let j = m - 1; j >= 0; j -= 1) {
      dp[i][j] = ka === key(b[j]) ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const ops: DiffOp[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (key(a[i]) === key(b[j])) {
      ops.push({ type: "same", text: b[j] });
      i += 1;
      j += 1;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      ops.push({ type: "del", text: a[i] });
      i += 1;
    } else {
      ops.push({ type: "add", text: b[j] });
      j += 1;
    }
  }
  while (i < n) ops.push({ type: "del", text: a[i++] });
  while (j < m) ops.push({ type: "add", text: b[j++] });
  return ops;
}

export type DiffStats = { deleted: number; added: number; unchanged: number };

/** 只数有字的句子，换行不算。 */
export function diffStats(ops: DiffOp[]): DiffStats {
  const stats: DiffStats = { deleted: 0, added: 0, unchanged: 0 };
  for (const op of ops) {
    if (!op.text.trim()) continue;
    if (op.type === "del") stats.deleted += 1;
    else if (op.type === "add") stats.added += 1;
    else stats.unchanged += 1;
  }
  return stats;
}

export type DiffChunk =
  | { kind: "change"; ops: DiffOp[] }
  | { kind: "context"; ops: DiffOp[]; collapsible: boolean; head: DiffOp[]; hidden: DiffOp[]; tail: DiffOp[] };

/**
 * 把 ops 分成「改动块」和「未改的上下文块」。上下文太长时只留头尾各 keep 句，
 * 中间折叠起来，让人一眼看到改动而不用在整篇里找。
 */
export function chunkDiff(ops: DiffOp[], keep = 1): DiffChunk[] {
  const chunks: DiffChunk[] = [];
  let run: DiffOp[] = [];
  let runType: "same" | "change" | null = null;
  const flush = () => {
    if (run.length === 0) return;
    if (runType === "change") {
      chunks.push({ kind: "change", ops: run });
    } else {
      const meaningful = run.filter((op) => op.text.trim());
      const collapsible = meaningful.length > keep * 2 + 1;
      if (!collapsible) {
        chunks.push({ kind: "context", ops: run, collapsible: false, head: run, hidden: [], tail: [] });
      } else {
        // 头尾按「有字的句子」数，换行归到相邻块里。
        let headEnd = 0;
        let seen = 0;
        while (headEnd < run.length && seen < keep) {
          if (run[headEnd].text.trim()) seen += 1;
          headEnd += 1;
        }
        let tailStart = run.length;
        seen = 0;
        while (tailStart > headEnd && seen < keep) {
          tailStart -= 1;
          if (run[tailStart].text.trim()) seen += 1;
        }
        chunks.push({
          kind: "context",
          ops: run,
          collapsible: true,
          head: run.slice(0, headEnd),
          hidden: run.slice(headEnd, tailStart),
          tail: run.slice(tailStart),
        });
      }
    }
    run = [];
  };
  for (const op of ops) {
    const type = op.type === "same" ? "same" : "change";
    if (runType !== null && type !== runType) flush();
    runType = type;
    run.push(op);
  }
  flush();
  return chunks;
}

/** 列表字段：另一版里没有的条目视为改动。 */
export function listItemChanged(item: string, other: string[]): boolean {
  const k = key(item);
  return !other.some((candidate) => key(candidate) === k);
}
