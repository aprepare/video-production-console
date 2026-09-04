export type UnwrappedImport =
  | { ok: true; script: string; fromJSON: boolean; hasPublishing: boolean }
  | { ok: false; message: string };

const remixJSONHint = "请粘贴连续正文，或换说法模型返回的 JSON（必须含 continuous_script）。";

export function unwrapImportedScript(raw: string): UnwrappedImport {
  const text = stripCodeFence(raw.trim());
  if (!text) {
    return { ok: true, script: "", fromJSON: false, hasPublishing: false };
  }
  if (!text.startsWith("{")) {
    return { ok: true, script: text, fromJSON: false, hasPublishing: false };
  }
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start < 0 || end <= start) {
    return { ok: false, message: remixJSONHint };
  }
  try {
    const draft = JSON.parse(text.slice(start, end + 1)) as {
      continuous_script?: unknown;
      titles?: unknown;
      short_titles?: unknown;
      descriptions?: unknown;
      topics?: unknown;
      cta?: unknown;
    };
    const script = typeof draft.continuous_script === "string" ? draft.continuous_script.trim() : "";
    if (!script) {
      return { ok: false, message: remixJSONHint };
    }
    return {
      ok: true,
      script,
      fromJSON: true,
      hasPublishing: hasFilled(draft.titles)
        || hasFilled(draft.short_titles)
        || hasFilled(draft.descriptions)
        || hasFilled(draft.topics)
        || (typeof draft.cta === "string" && draft.cta.trim() !== ""),
    };
  } catch {
    return { ok: false, message: remixJSONHint };
  }
}

// applyBoardTitles 把导入框里填的主/副标题并进 writer JSON 的 short_titles——
// 混剪背景板正是取 short_titles[0]/[1] 当主/副标题。两个都没填就原样返回；
// 粘贴的是 JSON 时保留其余发布字段，只把填写的标题顶到 short_titles 最前。
export function applyBoardTitles(raw: string, title: string, subtitle: string): string {
  const typed = [title.trim(), subtitle.trim()].filter((item) => item !== "");
  if (typed.length === 0) return raw;
  const text = stripCodeFence(raw.trim());
  if (!text.startsWith("{")) {
    return JSON.stringify({ continuous_script: text, short_titles: typed });
  }
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start < 0 || end <= start) return raw;
  try {
    const draft = JSON.parse(text.slice(start, end + 1)) as Record<string, unknown>;
    const existing = Array.isArray(draft.short_titles)
      ? draft.short_titles.filter((item): item is string =>
        typeof item === "string" && item.trim() !== "" && !typed.includes(item.trim()))
      : [];
    draft.short_titles = [...typed, ...existing];
    return JSON.stringify(draft);
  } catch {
    return raw;
  }
}

function stripCodeFence(raw: string) {
  let text = raw.trim();
  if (!text.startsWith("```")) return text;
  text = text.slice(3);
  const newline = text.indexOf("\n");
  if (newline >= 0) text = text.slice(newline + 1);
  if (text.endsWith("```")) text = text.slice(0, -3);
  return text.trim();
}

function hasFilled(value: unknown) {
  return Array.isArray(value) && value.some((item) => typeof item === "string" && item.trim() !== "");
}
