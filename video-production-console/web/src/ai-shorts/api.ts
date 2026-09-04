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

export type AiStylePreset = { key: string; name: string; prompt: string; usage: string };

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
  subject?: string;
  hero?: boolean;
  camera_move?: string;
  /** 按当前描述算出的生图提示词（预览）；image_prompt_used 是现有图真正用过的，不一致 = 图是旧的。 */
  image_prompt?: string;
  image_prompt_used?: string;
  video_prompt?: string;
  image_path?: string;
  image_status: string;
  video_path?: string;
  video_status: string;
  video_request_id?: string;
  error?: string;
  start_s?: number;
  end_s?: number;
};

export type AiShort = {
  id: string;
  account_id?: string;
  mode?: AiShortMode;
  /** 拆分镜用的文本模型；空 = 设置里的默认。 */
  text_model?: string;
  /** 解说模式先把整篇按话题切大段用的模型；空 = 按段落/字数机械切。 */
  segment_model?: string;
  title: string;
  headline: string;
  story: string;
  style: string;
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
  account_id?: string; mode?: AiShortMode; title?: string; story?: string; headline?: string; style?: string;
  /** 不传 = 不改；空串 = 改回默认。 */
  text_model?: string;
  segment_model?: string;
};

export type AiShortsMeta = { styles: AiStylePreset[]; defaultTextModel: string };

export async function fetchMeta(api: Api): Promise<AiShortsMeta> {
  const body = await json<{ items: AiStylePreset[]; default_text_model?: string }>(await api("/api/ai-shorts/styles"), "画风列表读取失败。");
  return { styles: body.items ?? [], defaultTextModel: body.default_text_model ?? "" };
}

/** 这镜是否已经出齐画面：解说模式的普通镜只要图，重点镜和寓言镜要视频。 */
export function shotReady(short: AiShort, shot: AiShot): boolean {
  const needsVideo = short.mode !== "explainer";
  return needsVideo ? shot.video_status === "done" && !!shot.video_path : shot.image_status === "done" && !!shot.image_path;
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

/** 素材文件（图/视频/配音）的访问地址：后端只按文件名回传短片目录内的文件。 */
export function assetURL(id: string, path?: string): string {
  if (!path) return "";
  const name = path.split(/[\\/]/).pop() ?? "";
  return `/api/ai-shorts/${id}/asset?name=${encodeURIComponent(name)}&v=${encodeURIComponent(path.length.toString())}`;
}
