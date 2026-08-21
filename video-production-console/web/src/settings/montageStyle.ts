import type { MontageStyle } from "../types";

export const montageFonts = [
  "新青年体",
  "俪金黑",
  "大字报",
  "抖音美好体",
  "汉仪英雄体",
  "站酷酷黑体",
  "宋体",
  "圆体",
  "毛笔行楷",
  "台北黑体_Bold",
] as const;

export const montageStyleDefaults: Required<MontageStyle> = {
  caption_size: 20,
  caption_color: "#F9F3C4",
  caption_position: "middle",
  caption_y: 0,
  caption_font: "新青年体",
  plain_size: 17,
  keyword_size: 23,
  keyword_color: "#FF1515",
  title_hidden: false,
  title_size: 16,
  title_color: "#FFDB1A",
  title_y: 0.6,
  subtitle_hidden: false,
  subtitle_size: 9.2,
  subtitle_color: "#FFFFFF",
  subtitle_y: 0.49,
  bgm_id: "builtin",
  bgm_volume: 0.2512,
};

// The backend omits zero-value fields when serializing, so absent keys fall
// back to the defaults above; the merged object is always sent back complete.
export function normalizeStyleColor(value: string): string {
  const trimmed = value.trim();
  return /^#[0-9a-fA-F]{6}$/.test(trimmed) ? trimmed.toUpperCase() : trimmed;
}

export function withMontageStyleDefaults(style?: MontageStyle): Required<MontageStyle> {
  const merged: Required<MontageStyle> = { ...montageStyleDefaults, ...style };
  (Object.keys(montageStyleDefaults) as Array<keyof MontageStyle>).forEach((key) => {
    if (merged[key] === undefined) {
      (merged as Record<string, unknown>)[key] = montageStyleDefaults[key];
    }
  });
  merged.caption_color = normalizeStyleColor(merged.caption_color);
  merged.keyword_color = normalizeStyleColor(merged.keyword_color);
  merged.title_color = normalizeStyleColor(merged.title_color);
  merged.subtitle_color = normalizeStyleColor(merged.subtitle_color);
  return merged;
}

export function formatTrackDuration(seconds: number): string {
  const total = Math.max(0, Math.round(seconds));
  const minutes = Math.floor(total / 60);
  const rest = total % 60;
  return `${minutes}:${String(rest).padStart(2, "0")}`;
}
