export type Api = (path: string, init?: RequestInit) => Promise<Response>;

export type AiCharacter = {
  name: string;
  description: string;
  /** 声线标签（狡诈/憨厚/老者/少年/威严/温和/女声），组装时映射到音色。 */
  voice?: string;
  image_path?: string;
  status: string;
  error?: string;
};

export const NARRATOR = "旁白";

/** fable = 寓言动画（角色 + 每镜视频）；explainer = 财经解说（每镜一图 + 推拉，重点镜出视频）。 */
export type AiShortMode = "fable" | "explainer";

// note：给选画风的人看的判断（适合谁 / 能不能让人停下）；preview：同一测试场景下该画风的参考图地址。
export type AiStylePreset = { key: string; name: string; prompt: string; usage: string; note?: string; preview?: string };

export type VideoPlan = "none" | "opening" | "hooks" | "all" | "first_n";
/** 分段画风：各位置用哪套画风（style key），空 = 跟随项目底色。opening_shots 开头算几镜，默认 1。 */
export type SegmentStyles = { opening?: string; opening_shots?: number; money?: string; blessing?: string; scenario?: string };
export const SEGMENT_ROLES: { key: keyof Omit<SegmentStyles, "opening_shots">; label: string; hint: string }[] = [
  { key: "opening", label: "开头几镜", hint: "抓人用：年代老照片或版画" },
  { key: "money", label: "讲钱的镜", hint: "旁白里有利息、存款、金额的镜" },
  { key: "blessing", label: "祝福词那一镜", hint: "评论区留四个字那一句" },
  { key: "scenario", label: "结尾处境镜", hint: "课尾前“……的时候，您拿不拿得出来”那几镜" },
];
/** 07 里给用户的推荐方案。 */
export const RECOMMENDED_SEGMENT_STYLES: SegmentStyles = { opening: "retro_film", opening_shots: 1, money: "macro_money", blessing: "papercut", scenario: "oil_painting" };
export const SHOT_ROLE_LABEL: Record<string, string> = { opening: "开头", money: "讲钱", blessing: "祝福词", scenario: "结尾处境", course: "课尾", body: "" };
export type ShotKeyword = { text: string; kind: "number" | "concept" | "risk" };
export type CaptionCue = { text: string; start_s: number; end_s: number; keywords?: ShotKeyword[] };
export type VisualSettings = {
  fast_opening?: boolean; opening_video_seconds?: 0 | 30 | 60; video_model?: string;
  /** 视频计划：none 全图 / opening 前 30·60 秒 / hooks 开场＋中段钩子＋结尾三段 / all 全片 / first_n 前 N 镜。空值按 opening_video_seconds 推导。 */
  video_plan?: VideoPlan; video_first_n?: number;
  /** 字幕样式：outline 白字黑边 / band 白字＋半透明底块。 */
  caption_style?: "outline" | "band";
  /** 字幕与顶部标题字色 #RRGGBB，默认黄字 #FFDE00。 */
  caption_color?: string;
  /** 顶部大标题只在开头停几秒；-1 表示贯穿全片；缺省 10。 */
  headline_seconds?: number;
  /** 分段画风；缺省或空对象 = 全片跟随底色。 */
  segment_styles?: SegmentStyles;
  layout: "portrait_full" | "portrait_inset";
  caption_enabled: boolean; caption_position: "lower" | "middle" | "window"; caption_size: number;
  keywords_enabled: boolean; annotation_enabled: boolean;
  motion_strength: "gentle" | "standard" | "none"; transition: "cut" | "fade"; sfx_enabled: boolean;
};
export const DEFAULT_CAPTION_COLOR = "#FFDE00";
export const HEADLINE_FULL_VIDEO = -1;
export const defaultVisualSettings = (): VisualSettings => ({ layout: "portrait_full", caption_enabled: true, caption_position: "lower", caption_size: 18, caption_color: DEFAULT_CAPTION_COLOR, headline_seconds: 10, keywords_enabled: false, annotation_enabled: false, motion_strength: "gentle", transition: "fade", sfx_enabled: false, fast_opening:true, opening_video_seconds:0 });
export const visualOf = (short: AiShort): VisualSettings => short.visual_settings ?? { ...defaultVisualSettings(), layout: "portrait_inset", caption_position: "window", caption_size: 9, keywords_enabled: false, annotation_enabled: false, sfx_enabled: true, fast_opening:false, transition:"cut" };

export type AiShot = {
  index: number;
  narration: string;
  /** 「旁白」或角色名。 */
  speaker?: string;
  scene: string;
  motion: string;
  characters: string[];
  seconds: number;
  /** 解说模式：画风预设 key / 画面主体 / 重点镜（图生视频） / 推拉类型。 */
  style_key?: string;
  /** 镜头在全片里的位置角色（opening/money/blessing/scenario/course/body），分段画风按它选画风；style_pinned 表示手动定过画风。 */
  role?: string;
  style_pinned?: boolean;
  subject?: string;
  hero?: boolean;
  camera_move?: string;
  source_text?: string;
  visual_intent?: string;
  subject_type?: string;
  keywords?: ShotKeyword[];
  annotation?: string;
  caption_lines?: string[];
  aspect_ratio?: string;
  image_stale?: boolean;
  /** 按当前描述算出的生图提示词（预览）；image_prompt_used 是现有图真正用过的，不一致 = 图是旧的。 */
  image_prompt?: string;
  image_prompt_used?: string;
  video_prompt?: string;
  image_path?: string;
  image_status: string;
  video_path?: string;
  video_status: string;
  video_request_id?: string;
  video_submit_uncertain?: boolean;
  error?: string;
  start_s?: number;
  end_s?: number;
};

export type AiCover = {
  path?: string;
  status?: string;
  prompt?: string;
  prompt_used?: string;
  headline_used?: string;
  style_key?: string;
  error?: string;
  stale?: boolean;
};

export type AiShort = {
  text_reasoning_effort?: string;
  assembly_progress?: {stage:number;message:string;started_at:string;updated_at:string};
  cover?: AiCover;
  id: string;
  account_id?: string;
  mode?: AiShortMode;
  /** 拆分镜用的文本模型；空 = 设置里的默认。 */
  text_model?: string;
  /** 本片生图模型；空值使用后台默认，已有图片保留。 */
  image_model?: string;
  /** 解说模式先把整篇按话题切大段用的模型；空 = 按段落/字数机械切。 */
  segment_model?: string;
  title: string;
  headline: string;
  story: string;
  style: string;
  visual_settings?: VisualSettings;
  draft_stale?: boolean;
  storyboard_stale?: boolean;
  captions?: CaptionCue[];
  status: string;
  error?: string;
  characters: AiCharacter[];
  shots: AiShot[];
  narration_path?: string;
  srt_path?: string;
  draft_path?: string;
  draft_name?: string;
  duration_s?: number;
  created_at: string;
  updated_at: string;
};

async function readError(response: Response, fallback: string): Promise<string> {
  try {
    const body = (await response.json()) as { message?: string };
    if (body.message?.trim()) return body.message;
  } catch {
    // fall through
  }
  return fallback;
}

async function json<T>(response: Response, fallback: string): Promise<T> {
  if (!response.ok) throw new Error(await readError(response, fallback));
  return (await response.json()) as T;
}

const jsonInit = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export async function listShorts(api: Api): Promise<AiShort[]> {
  const body = await json<{ items: AiShort[] }>(await api("/api/ai-shorts"), "短片列表读取失败。");
  return body.items ?? [];
}

export async function getShort(api: Api, id: string): Promise<AiShort> {
  return json(await api(`/api/ai-shorts/${id}`), "短片读取失败。");
}

export type ShortTextInput = {
  text_reasoning_effort?: string;
  account_id?: string; mode?: AiShortMode; title?: string; story?: string; headline?: string; style?: string;
  /** 不传 = 不改；空串 = 改回默认。 */
  text_model?: string;
  segment_model?: string;
  image_model?: string;
  visual_settings?: VisualSettings;
};

export type AiShortsMeta = { styles: AiStylePreset[]; defaultTextModel: string; defaultImageModel: string; defaultVideoModel: string };

export async function fetchMeta(api: Api): Promise<AiShortsMeta> {
  const body = await json<{ items: AiStylePreset[]; default_text_model?: string; default_image_model?: string; default_video_model?: string }>(await api("/api/ai-shorts/styles"), "画风列表读取失败。");
  return { styles: body.items ?? [], defaultTextModel: body.default_text_model ?? "", defaultImageModel: body.default_image_model ?? "", defaultVideoModel:body.default_video_model ?? "" };
}

/** 这镜是否已经出齐画面：解说模式的普通镜只要图，重点镜和寓言镜要视频。 */
export function shotReady(short: AiShort, shot: AiShot): boolean {
  return shotNeedsVideo(short,shot) ? shot.video_status === "done" && !!shot.video_path && !shot.image_stale : shot.image_status === "done" && !!shot.image_path && !shot.image_stale;
}

/** 与后端 VisualSettings.Plan 一致：显式 video_plan 优先，否则按 opening_video_seconds 推导。 */
export function videoPlanOf(v?: VisualSettings | null): VideoPlan {
  const plan = v?.video_plan;
  if (plan === "none" || plan === "opening" || plan === "hooks" || plan === "all" || plan === "first_n") return plan;
  return (v?.opening_video_seconds || 0) > 0 ? "opening" : "none";
}

const HOOKS_OPENING_SECS = 60, HOOKS_CLOSING_SHOTS = 3, SHOT_GAP_SECS = 0.35;

/** 这镜的起始秒；未配音定时时按 0.23 秒/字估算，与后端 shotStart 同步。 */
function shotStartOf(short: AiShort, shot: AiShot): number {
  if ((shot.end_s || 0) > (shot.start_s || 0)) return shot.start_s || 0;
  let start = 0;
  for (const previous of short.shots || []) {
    if (previous.index === shot.index) break;
    start += previous.narration.replace(/[\s，。！？、；：“”‘’（）【】《》…—,.!?;:"'()\[\]-]/g, "").length * 0.23 + SHOT_GAP_SECS;
  }
  return start;
}

function isMidHookShot(short: AiShort, shot: AiShot): boolean {
  const shots = short.shots || [];
  const i = shots.findIndex((x) => x.narration.includes("四个字"));
  if (i < 0) return false;
  return shot.index === shots[i].index || (i + 1 < shots.length && shot.index === shots[i + 1].index);
}

export function shotNeedsVideo(short: AiShort, shot: AiShot): boolean {
  if(short.mode!=="explainer") return true;
  const v = short.visual_settings;
  switch (videoPlanOf(v)) {
    case "all": return true;
    case "first_n": return shot.index < (v?.video_first_n || 0);
    case "opening": return shotStartOf(short, shot) < (v?.opening_video_seconds || 60);
    case "hooks": return shotStartOf(short, shot) < HOOKS_OPENING_SECS || isMidHookShot(short, shot) || shot.index >= (short.shots?.length || 0) - HOOKS_CLOSING_SHOTS;
    default: return false;
  }
}

export async function createShort(api: Api, input: ShortTextInput): Promise<AiShort> {
  return json(await api("/api/ai-shorts", jsonInit("POST", input)), "创建失败。");
}

export async function updateShort(api: Api, id: string, input: ShortTextInput): Promise<AiShort> {
  return json(await api(`/api/ai-shorts/${id}`, jsonInit("PATCH", input)), "保存失败。");
}

export async function deleteShort(api: Api, id: string): Promise<void> {
  await json(await api(`/api/ai-shorts/${id}`, { method: "DELETE" }), "删除失败。");
}

export async function storyboardShort(api: Api, id: string): Promise<AiShort> {
  return json(await api(`/api/ai-shorts/${id}/storyboard`, { method: "POST" }), "拆分镜失败。");
}

export async function generateShort(api: Api, id: string): Promise<void> {
  await json(await api(`/api/ai-shorts/${id}/generate`, { method: "POST" }), "开始生成失败。");
}

export async function updateShot(
  api: Api,
  id: string,
  index: number,
  input: {
    scene?: string; motion?: string; narration?: string; speaker?: string; seconds?: number;
    style_key?: string; subject?: string; hero?: boolean;
    visual_intent?: string; subject_type?: string; camera_move?: string; annotation?: string; keywords?: ShotKeyword[];
  },
): Promise<AiShort> {
  return json(await api(`/api/ai-shorts/${id}/shots/${index}`, jsonInit("PATCH", input)), "保存分镜失败。");
}

export async function regenerateShot(api: Api, id: string, index: number, stage: "image" | "video"): Promise<void> {
  await json(await api(`/api/ai-shorts/${id}/shots/${index}/regenerate`, jsonInit("POST", { stage })), "重生失败。");
}

export async function regenerateCharacter(api: Api, id: string, index: number): Promise<void> {
  await json(await api(`/api/ai-shorts/${id}/characters/${index}/regenerate`, { method: "POST" }), "重画角色失败。");
}

export async function assembleShort(api: Api, id: string): Promise<void> {
  await json(await api(`/api/ai-shorts/${id}/assemble`, { method: "POST" }), "组装失败。");
}

export async function generateCover(api: Api, id: string): Promise<void> {
  await json(await api(`/api/ai-shorts/${id}/cover`, { method: "POST" }), "封面生成失败。");
}

/** 素材文件（图/视频/配音）的访问地址：后端只按文件名回传短片目录内的文件。 */
export function assetURL(id: string, path?: string): string {
  if (!path) return "";
  const name = path.split(/[\\/]/).pop() ?? "";
  return `/api/ai-shorts/${id}/asset?name=${encodeURIComponent(name)}&v=${encodeURIComponent(path.length.toString())}`;
}
