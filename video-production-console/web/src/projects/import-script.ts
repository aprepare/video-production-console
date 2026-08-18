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
