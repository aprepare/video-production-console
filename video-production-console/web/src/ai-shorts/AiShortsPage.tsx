import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, Clapperboard, Film, Plus, RefreshCw, Trash2, Wand2 } from "lucide-react";
import type { Account, Theme } from "../types";
import {
  assembleShort,
  assetURL,
  createShort,
  deleteShort,
  generateShort,
  fetchMeta,
  getShort,
  listShorts,
  regenerateCharacter,
  regenerateShot,
  shotReady,
  shotNeedsVideo,
  storyboardShort,
  updateShort,
  updateShot,
  NARRATOR,
  defaultVisualSettings, visualOf,
  type VisualSettings, type ShotKeyword,
  type AiShort,
  type AiShortMode,
  type AiShot,
  type AiStylePreset,
  type Api,
} from "./api";
import { ModelCombo } from "../ModelSelect";
import { AssemblyStatus, ReasoningSelect } from "./WorkflowControls";
import { MediaSettingsPanel } from "./MediaSettingsPanel";
import { OpeningVideoFields, VisualSettingsFields, KeywordEditor, HighlightedText, FramePreview, subjectTypes, cameraMoves } from "./VisualControls";
import "./ai-shorts.css";
import "./workbench.css";

// AI 短片分镜板：左栏故事与角色，中栏分镜卡片，右栏成片。
// 页面只做展示和触发，长任务在后端跑，这里每 3 秒轮询一次进度。

type Props = {
  api: Api;
  shortID?: string;
  onNavigate: (href: string) => void;
  theme?: Theme;
  onThemeChange?: (theme: Theme) => void;
  onOpenSettings?: () => void;
  onLogout?: () => void;
};

const STATUS_LABEL: Record<string, string> = {
  draft: "待拆分镜",
  storyboard: "分镜就位",
  generating: "生成中",
  ready: "素材齐了",
  assembling: "组装中",
  assembled: "已进剪映",
  failed: "失败",
};

const SHOT_STATUS: Record<string, string> = {
  pending: "待生成",
  running: "生成中…",
  done: "完成",
  failed: "失败",
};

type ShotDraft = { scene: string; motion: string; narration: string; speaker: string; style_key: string; subject: string; hero: boolean; visual_intent: string; subject_type: string; camera_move: string; annotation: string; keywords: ShotKeyword[] };

const MODE_LABEL: Record<AiShortMode, string> = { fable: "寓言动画", explainer: "财经解说" };

const DEFAULT_STORY = "族谱上写着，我们祖上是凤凰，是被遗忘的飞禽。若不敢跳，便永世为鸡。自愿入局怨不得别人。我专程前来求取熬汤之法。只要他们笃信崖上标语，我们便有吃不完的肉，喝不尽的汤，哈哈哈。前辈全从这里跃下，化作凤凰乘风飞走。我们纵身一跃，定然也会成功！多好的食材啊，还自带奋斗的佐料。他们以为自己在飞跃，其实是在排队入局。";

const emptyShotDraft = (): ShotDraft => ({ scene: "", motion: "", narration: "", speaker: NARRATOR, style_key: "", subject: "", hero: false, visual_intent: "", subject_type: "object", camera_move: "zoom_in", annotation: "", keywords: [] });
const draftOf = (shot: AiShot): ShotDraft => ({
  scene: shot.scene, motion: shot.motion, narration: shot.narration, speaker: shot.speaker || NARRATOR,
  style_key: shot.style_key || "", subject: shot.subject || "", hero: !!shot.hero,
  visual_intent: shot.visual_intent || "", subject_type: shot.subject_type || "object", camera_move: shot.camera_move || "zoom_in", annotation: shot.annotation || "", keywords: shot.keywords || [],
});

type SavedDraft = { reasoning?:string; story?: string; headline?: string; style?: string; textModel?: string; segmentModel?: string; imageModel?: string; editingShot?: number | null; shot?: ShotDraft; mode?: AiShortMode; account?: string; visual?: VisualSettings };
const draftKey = (id: string) => `console:ai-short-draft:${id}`;
function readDraft(id: string): SavedDraft {
  try { return JSON.parse(sessionStorage.getItem(draftKey(id)) || "{}"); } catch { return {}; }
}
function storeDraft(id: string, value: SavedDraft | null) {
  try { if (value) sessionStorage.setItem(draftKey(id), JSON.stringify(value)); else sessionStorage.removeItem(draftKey(id)); } catch { /* Storage can be disabled; keep the in-memory editor usable. */ }
}

function sameVisualSettings(a: VisualSettings, b: VisualSettings) {
  const normalize = (value: VisualSettings) => ({ ...value, fast_opening: !!value.fast_opening, opening_video_seconds: value.opening_video_seconds ?? 0, video_model: (value.video_model ?? "").trim() });
  const left = normalize(a), right = normalize(b);
  return (Object.keys(left) as (keyof VisualSettings)[]).every(key => left[key] === right[key])
    && (Object.keys(right) as (keyof VisualSettings)[]).every(key => left[key] === right[key]);
}

export function AiShortsPage({ api, shortID, onNavigate }: Props) {
  const [shorts, setShorts] = useState<AiShort[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [current, setCurrent] = useState<AiShort | null>(null);
  const [message, setMessage] = useState("");
  const mediaDialog = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState("");
  const busyRef = useRef(false);
  const loadVersion = useRef(0);
  const [loadState, setLoadState] = useState("idle");
  const [loadError, setLoadError] = useState("");
  const [pollError, setPollError] = useState("");
  const [listState, setListState] = useState("loading");
  const [newDraft] = useState(() => readDraft("new"));
  // 新建表单
  const [newMode, setNewMode] = useState<AiShortMode>(newDraft.mode ?? "explainer");
  const [newStory, setNewStory] = useState(newDraft.story ?? "");
  const [newHeadline, setNewHeadline] = useState(newDraft.headline ?? "");
  const [newAccount, setNewAccount] = useState(newDraft.account ?? "");
  const [newReasoning,setNewReasoning]=useState(newDraft.reasoning ?? "");
  const [reasoningDraft,setReasoningDraft]=useState("");
  const [newTextModel, setNewTextModel] = useState(newDraft.textModel ?? "");
  const [newSegmentModel, setNewSegmentModel] = useState(newDraft.segmentModel ?? "");
  const [newImageModel, setNewImageModel] = useState(newDraft.imageModel ?? "");
  const [newStyle, setNewStyle] = useState(newDraft.style ?? "finance_editorial");
  const [newVisual, setNewVisual] = useState<VisualSettings>(newDraft.visual ?? defaultVisualSettings());
  const [visualDraft, setVisualDraft] = useState<VisualSettings>(defaultVisualSettings);
  const [styles, setStyles] = useState<AiStylePreset[]>([]);
  const [defaultTextModel, setDefaultTextModel] = useState("");
  const [defaultImageModel, setDefaultImageModel] = useState("");
  const [defaultVideoModel, setDefaultVideoModel] = useState("");
  // 编辑中的文案（当前短片）
  const [storyDraft, setStoryDraft] = useState("");
  const [headlineDraft, setHeadlineDraft] = useState("");
  const [styleDraft, setStyleDraft] = useState("");
  const [textModelDraft, setTextModelDraft] = useState("");
  const [segmentModelDraft, setSegmentModelDraft] = useState("");
  const [imageModelDraft, setImageModelDraft] = useState("");
  const [editingShot, setEditingShot] = useState<number | null>(null);
  const [shotDraft, setShotDraft] = useState<ShotDraft>(emptyShotDraft);
  const pollRef = useRef<number | undefined>(undefined);

  const reloadList = async () => {
    setListState("loading");
    try {
      setShorts(await listShorts(api));
      setListState("ready");
    } catch (error) {
      setListState("error");
      setMessage(error instanceof Error ? error.message : "列表读取失败。");
    }
  };

  useEffect(() => {
    void reloadList();
    void api("/api/accounts").then(async (res) => {
      if (res.ok) setAccounts(((await res.json()) as Account[]).filter((a) => a.status !== "inactive"));
    }).catch(() => undefined);
    void fetchMeta(api).then((meta) => { setStyles(meta.styles); setDefaultTextModel(meta.defaultTextModel); setDefaultImageModel(meta.defaultImageModel); setDefaultVideoModel(meta.defaultVideoModel); }).catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  const load = async (id: string) => {
    const version = ++loadVersion.current;
    setLoadState("loading");
    setLoadError("");
    setPollError("");
    setCurrent(null);
    setEditingShot(null);
    try {
      const short = await getShort(api, id);
      if (version !== loadVersion.current) return;
      setLoadState("ready");
      setCurrent(short);
      const saved = readDraft(id);
      setStoryDraft(saved.story ?? short.story);
      setHeadlineDraft(saved.headline ?? short.headline);
      setStyleDraft(saved.style ?? short.style);
      setReasoningDraft(saved.reasoning ?? short.text_reasoning_effort ?? "");
      setTextModelDraft(saved.textModel ?? short.text_model ?? "");
      setSegmentModelDraft(saved.segmentModel ?? short.segment_model ?? "");
      setImageModelDraft(saved.imageModel ?? short.image_model ?? "");
      setVisualDraft(saved.visual ?? visualOf(short));
      if (saved.editingShot != null && saved.shot && short.shots.some((shot) => shot.index === saved.editingShot)) {
        setEditingShot(saved.editingShot);
        setShotDraft({ ...emptyShotDraft(), ...saved.shot });
      }
    } catch (error) {
      if (version !== loadVersion.current) return;
      setLoadState("error");
      setLoadError(error instanceof Error ? error.message : "短片读取失败。");
    }
  };

  useEffect(() => {
    if (!shortID) {
      ++loadVersion.current;
      setLoadState("idle");
      setCurrent(null);
      return;
    }
    void load(shortID);
    // Invalidate the latest request on route change; this counter is not a DOM ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    return () => { ++loadVersion.current; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shortID]);

  // 生成中 / 组装中持续轮询，其余状态停。
  const active = current?.status === "generating" || current?.status === "assembling";
  useEffect(() => {
    if (!current || !active) return;
    const version = loadVersion.current;
    const id = current.id;
    let cancelled = false;
    pollRef.current = window.setInterval(async () => {
      try {
        const fresh = await getShort(api, id);
        if (cancelled || version !== loadVersion.current || fresh.id !== id) return;
        setPollError("");
        setCurrent(fresh);
        if (fresh.status !== "generating" && fresh.status !== "assembling") void reloadList();
      } catch {
        if (!cancelled && version === loadVersion.current) setPollError("任务状态刷新失败，正在每 3 秒重试；恢复连接后会自动更新按钮状态。");
      }
    }, 3000);
    return () => { cancelled = true; window.clearInterval(pollRef.current); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.id, active]);



  const run = async (label: string, action: () => Promise<void>) => {
    if (busyRef.current) return;
    busyRef.current = true;
    setMessage("");
    setBusy(label);
    try {
      await action();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : `${label}失败。`);
    } finally {
      busyRef.current = false;
      setBusy("");
    }
  };

  const create = () => run("创建", async () => {
    const short = await createShort(api, {
      mode: newMode, story: newStory, headline: newHeadline, account_id: newAccount,
      text_reasoning_effort:newReasoning, text_model: newTextModel, segment_model: newMode === "explainer" ? newSegmentModel : undefined,
      image_model: newImageModel.trim(),
      style: newMode === "explainer" ? newStyle : undefined,
      visual_settings: newMode === "explainer" ? newVisual : undefined,
    });
    storeDraft("new", null);
    setNewStory("");
    setNewHeadline("");
    await reloadList();
    onNavigate(`/ai-shorts/${short.id}`);
  });

  const textInput = () => ({ text_reasoning_effort:reasoningDraft, story: storyDraft, headline: headlineDraft, style: styleDraft, text_model: textModelDraft, segment_model: segmentModelDraft, image_model: imageModelDraft.trim(), visual_settings: current?.mode === "explainer" ? visualDraft : undefined });

  const acceptSavedText = (short: AiShort) => {
    setCurrent(short);
    setStoryDraft(short.story);
    setHeadlineDraft(short.headline);
    setStyleDraft(short.style);
    setReasoningDraft(short.text_reasoning_effort ?? "");
    setTextModelDraft(short.text_model ?? "");
    setSegmentModelDraft(short.segment_model ?? "");
    setImageModelDraft(short.image_model ?? "");
    setVisualDraft(visualOf(short));
  };

  const saveText = () => current && run("保存", async () => {
    acceptSavedText(await updateShort(api, current.id, textInput()));
    setMessage("已保存。改了旁白记得重新拆分镜。");
  });

  const storyboard = () => current && run("拆分镜", async () => {
    acceptSavedText(await updateShort(api, current.id, textInput()));
    acceptSavedText(await storyboardShort(api, current.id));
    setMessage("分镜拆好了，看一眼画面描述，没问题就点「生成全部」。");
  });

  const generate = () => current && run("生成", async () => {
    await generateShort(api, current.id);
    setCurrent({ ...current, status: "generating" });
  });

  const assemble = () => current && run("组装", async () => {
    await assembleShort(api, current.id);
    setCurrent({ ...current, status: "assembling" });
  });

  const regen = (index: number, stage: "image" | "video") => current && run("重生", async () => {
    await regenerateShot(api, current.id, index, stage);
    // 后端已经按「分镜」独立加锁；这里也只把当前卡标成运行中，
    // 其他卡继续可点，用户可以连续提交多镜（全局生图并发上限 20）。
    setCurrent({
      ...current,
      status: "generating",
      shots: current.shots.map((shot) => shot.index === index
        ? {
            ...shot,
            image_status: stage === "image" ? "running" : shot.image_status,
            video_status: stage === "video" ? "running" : "pending",
            error: undefined,
          }
        : shot),
    });
  });

  const regenCharacter = (index: number) => current && run("重画", async () => {
    await regenerateCharacter(api, current.id, index);
    setCurrent({ ...current, status: "generating" });
  });

  const saveShot = (index: number) => current && run("保存分镜", async () => {
    const { scene, motion, narration, speaker, style_key, subject, visual_intent, subject_type, camera_move, annotation, keywords } = shotDraft;
    const input = isExplainer
      ? {
          scene, motion, narration, subject, visual_intent, subject_type, camera_move, annotation, keywords,
          ...(current.style === "finance_editorial" ? { style_key } : {}),
        }
      : { scene, motion, narration, speaker };
    setCurrent(await updateShot(api, current.id, index, input));
    setEditingShot(null);
  });

  const remove = (id: string) => run("删除", async () => {
    await deleteShort(api, id);
    await reloadList();
    if (current?.id === id) onNavigate("/ai-shorts");
  });

  const accountName = (id?: string) => accounts.find((a) => a.id === id)?.name ?? "";
  const isExplainer = current?.mode === "explainer";
  const contentDirty = !!current && (reasoningDraft !== (current.text_reasoning_effort??"") || storyDraft !== current.story || headlineDraft !== current.headline || styleDraft !== current.style || textModelDraft !== (current.text_model ?? "") || segmentModelDraft !== (current.segment_model ?? ""));
  const imageModelDirty = !!current && imageModelDraft.trim() !== (current.image_model ?? "");
  const videoConfigDirty=!!current&&isExplainer&&((visualDraft.opening_video_seconds||0)!==(current.visual_settings?.opening_video_seconds||0)||(visualDraft.video_model||"").trim()!==(current.visual_settings?.video_model||""));
  const textDirty = contentDirty || imageModelDirty || (!!current && isExplainer && !sameVisualSettings(visualDraft, visualOf(current)));
  useEffect(() => {
    if (!current) return;
    storeDraft(current.id, textDirty || editingShot !== null ? { reasoning:reasoningDraft, story: storyDraft, headline: headlineDraft, style: styleDraft, textModel: textModelDraft, segmentModel: segmentModelDraft, imageModel: imageModelDraft, editingShot, shot: shotDraft, visual: visualDraft } : null);
  }, [current, textDirty, reasoningDraft, storyDraft, headlineDraft, styleDraft, textModelDraft, segmentModelDraft, imageModelDraft, editingShot, shotDraft, visualDraft]);
  useEffect(() => {
    storeDraft("new", newStory || newHeadline || newImageModel ? { reasoning:newReasoning, story: newStory, headline: newHeadline, mode: newMode, account: newAccount, textModel: newTextModel, segmentModel: newSegmentModel, imageModel: newImageModel, style: newStyle, visual: newVisual } : null);
  }, [newReasoning, newStory, newHeadline, newMode, newAccount, newTextModel, newSegmentModel, newImageModel, newStyle, newVisual]);
  const doneShots = useMemo(() => (current ? current.shots.filter((s) => shotReady(current, s)).length : 0), [current]);

  return (
    <div className="ai-shorts">
      {message ? <div className="ai-shorts__toast" role="status">{message}</div> : null}
      <header className="ai-shorts__header">
        <div className="ai-shorts__brand">
          <span className="ai-shorts__mark"><Clapperboard size={18} strokeWidth={2} /></span>
          <div>
            <span className="eyebrow">制作工作区</span>
            <h1>AI 短片</h1>
            <p>先审分镜，再生成画面，最后配音并交付剪映草稿。</p>
          </div>
        </div>
        <div className="ai-shorts__actions">
          <span className="ai-shorts__muted">{current ? current.title : "从定稿到剪映草稿"}</span>
        </div>
      </header>

      {shortID && loadState === "loading" ? <div className="ai-shorts__load" role="status">正在读取短片…</div> : shortID && loadState === "error" ? <div className="ai-shorts__load" role="alert"><h2>短片读取失败</h2><p>{loadError}</p><button type="button" className="header-button" onClick={() => void load(shortID)}>重试读取短片</button></div> : <>
      <ol className="ai-shorts__progress" aria-label="短片制作进度">
        {["准备文案", "审阅分镜", "生成画面", "交付草稿"].map((label, index) => <li key={label} aria-current={index === (!current ? 0 : current.status === "assembled" || current.status === "assembling" ? 3 : current.shots.length ? 2 : 1) ? "step" : undefined}><span>{String(index + 1).padStart(2, "0")}</span>{label}</li>)}
      </ol>
      <div className="ai-shorts__command-bar" aria-label="短片快捷操作">
        <div className="ai-shorts__command-head">
          <strong>{current ? `${doneShots} / ${current.shots.length} 镜完成` : "创建短片"}</strong>
          <button type="button" className="header-button" onClick={() => mediaDialog.current?.showModal()}>接口与并发设置</button>
        </div>
        {current && <>
          <AssemblyStatus short={current} />
          {pollError && <p className="ai-shorts__dirty" role="alert">{pollError}</p>}
          <div className="ai-shorts__quick-models">
                <label>
                  生图模型（保存后用于后续生图）
                  <input aria-label="生图模型" value={imageModelDraft} onChange={(e) => setImageModelDraft(e.target.value)} placeholder={`默认：${defaultImageModel || "后台配置的生图模型"}`} disabled={!!busy || active} />
                </label>
                <p className="ai-shorts__muted">留空使用默认模型；已有图片保留，点单镜「重生图」可用新模型重画。模型需由当前中转接口支持。</p>

            {isExplainer && <OpeningVideoFields value={visualDraft} onChange={setVisualDraft} disabled={!!busy || active} defaultVideoModel={defaultVideoModel} />}
          </div>
          <div className="ai-shorts__command-actions">
                <div className="ai-shorts__row">
                  <button type="button" className="header-button" disabled={!!busy || active} onClick={saveText}>保存文案</button>
                  <button type="button" className="remix-lab-start" disabled={!!busy || active} onClick={storyboard}>
                    <Wand2 size={14} /> {busy === "拆分镜" ? "拆分镜中…" : current.shots.length ? "重新拆分镜" : "拆分镜"}
                  </button>
                </div>

            {current.shots.length > 0 && <>
              <div className="ai-shorts__toolbar">
                <span>
                  {current.shots.length} 镜 · 已出画面 {doneShots}/{current.shots.length}
                </span>
                <div className="ai-shorts__row">
                  <button type="button" className="remix-lab-start" disabled={!!busy || active || textDirty || current.storyboard_stale || editingShot !== null} onClick={generate}>
                    {active && current.status === "generating" ? "生成中…" : doneShots === 0 ? (isExplainer ? current.visual_settings?.opening_video_seconds ? "生成图片与开场视频" : "生成全部图片" : "生成全部") : "续跑未完成的"}
                  </button>
                  <button
                    type="button"
                    className="remix-lab-start"
                    disabled={!!busy || active || textDirty || current.storyboard_stale || editingShot !== null || doneShots !== current.shots.length}
                    onClick={assemble}
                    title={doneShots !== current.shots.length ? "所有镜头出完才能组装" : "配音 + 剪映草稿"}
                  >
                    {current.status === "assembling" ? "组装中…" : "配音并进剪映"}
                  </button>
                </div>
              </div>

            </>}
          </div>
                {textDirty ? <p className="ai-shorts__dirty" role="status">{contentDirty ? "文案有未保存修改，请保存并重新拆分镜后生成。" : imageModelDirty ? "生图模型有未保存修改，请先保存文案。保存后用于后续生图，无需重新拆分镜。" : "画面与字幕设置有未保存修改，请先保存文案。"}</p> : null}
                {current.storyboard_stale ? <p className="ai-shorts__dirty">文案已更新，请先重新拆分镜。旧图和旧草稿仍保留。</p> : null}
                {editingShot !== null && <p className="ai-shorts__dirty" role="status">第 {editingShot + 1} 镜正在编辑，请先保存或取消该分镜的修改。</p>}
                {active && <p className="ai-shorts__muted" role="status">{current.status === "assembling" ? "正在组装草稿，完成后可继续操作。" : "后台仍在生成，结束后可续跑失败或未完成的镜头。"}</p>}
                {busy && !active && <p className="ai-shorts__muted" role="status">正在{busy}，请等待操作结束。</p>}
                {current.error ? <p className={current.status === "failed" ? "ai-shorts__error" : "ai-shorts__muted"}>{current.error}</p> : null}

        </>}
      </div>
      <dialog ref={mediaDialog} className="ai-shorts__settings-dialog" aria-label="接口与并发设置">
        <div className="ai-shorts__dialog-head"><strong>接口与并发设置</strong><button type="button" className="header-button" onClick={() => mediaDialog.current?.close()}>关闭设置</button></div>
        <MediaSettingsPanel api={api} disabled={!!busy || active} />
      </dialog>
      <div className={`ai-shorts__body${current ? "" : " ai-shorts__body--create"}`}>
        {/* 左栏：列表 + 新建 / 当前短片的文案与角色 */}
        <aside className="ai-shorts__side">
          {!current ? (
            <section className="ai-shorts__card">
              <h2>新建短片</h2>
              <div className="ai-shorts__modes" role="radiogroup" aria-label="短片类型">
                {(Object.keys(MODE_LABEL) as AiShortMode[]).map((mode) => (
                  <button
                    key={mode}
                    type="button"
                    role="radio"
                    aria-checked={newMode === mode}
                    className={`ai-shorts__mode${newMode === mode ? " is-active" : ""}`}
                    onClick={() => {
                      setNewMode(mode);
                      if (mode === "fable" && !newStory) setNewStory(DEFAULT_STORY);
                    }}
                  >
                    <strong>{MODE_LABEL[mode]}</strong>
                    <small>{mode === "explainer" ? "按文意配图，统一纪实画风；轻柔运动、旁白字幕与重点标注" : "动物角色 + 每镜图生视频，角色在视频里开口说台词"}</small>
                  </button>
                ))}
              </div>
              <label>
                账号（决定配音音色和草稿命名）
                <select value={newAccount} onChange={(e) => setNewAccount(e.target.value)}>
                  <option value="">不指定</option>
                  {accounts.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
                </select>
              </label>
              <details className="ai-shorts__config-fold">
                <summary>模型与画面设置（可沿用默认）</summary>
              <label>
                拆分镜模型（可直接输入任意模型名，留空用默认）
                <ModelCombo id="ai-shorts-new-model" aria-label="拆分镜模型" value={newTextModel} onChange={setNewTextModel} placeholder={`默认：${defaultTextModel || "设置里的 Grok 模型"}`} />
              </label>
<ReasoningSelect value={newReasoning} onChange={setNewReasoning} disabled={!!busy} />
              <label>
                生图模型（直接填写模型名，留空用默认）
                <input aria-label="生图模型" value={newImageModel} onChange={(e) => setNewImageModel(e.target.value)} placeholder={`默认：${defaultImageModel || "后台配置的生图模型"}`} disabled={!!busy} />
              </label>
              <p className="ai-shorts__muted">使用 AI 短片当前中转接口，请填写该接口支持的生图模型名。</p>
              {newMode === "explainer" ? (
                <label>
                  分大段模型（先按话题把整篇切成几段，再并行拆镜；留空按段落机械切）
                  <ModelCombo id="ai-shorts-new-segment-model" aria-label="分大段模型" value={newSegmentModel} onChange={setNewSegmentModel} placeholder="如 gemini-3.8-flash-high；留空不用模型" />
                </label>
              ) : null}
              {newMode === "explainer" ? (
                <label>
                  画面风格策略
                  <select value={newStyle} onChange={(e) => setNewStyle(e.target.value)}>
                    {styles.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
                  </select>
                  {newStyle === "finance_editorial" ? <span className="ai-shorts__muted">大多数镜头使用纸张拼贴，抽象概念和多方关系使用微缩模型；拆分镜后可逐镜调整。</span> : null}
                </label>
              ) : null}
              {newMode === "fable" ? (
                <label>
                  顶部金句（可空，拆分镜时会自动补）
                  <input value={newHeadline} onChange={(e) => setNewHeadline(e.target.value)} />
                </label>
              ) : null}
              {newMode === "explainer" ? <VisualSettingsFields value={newVisual} onChange={setNewVisual} disabled={!!busy} defaultVideoModel={defaultVideoModel} /> : null}
              </details>
              <label>
                {newMode === "explainer" ? "口播文案（贴整篇，最多 6000 字）" : "旁白文案（100～300 字）"}
                <textarea rows={7} value={newStory} onChange={(e) => setNewStory(e.target.value)} placeholder={newMode === "explainer" ? "把二创定稿或口播稿整篇贴进来" : ""} />
              </label>
              <button type="button" className="remix-lab-start" disabled={!!busy || newStory.trim().length < 20} onClick={create}>
                <Plus size={14} /> 建短片
              </button>
            </section>
          ) : (
            <>
              <details className="ai-shorts__card ai-shorts__config-fold">
                <summary>文案与画面设置</summary>
                <div className="ai-shorts__card-head">
                  <h2>{isExplainer ? "文案" : "故事"}</h2>
                  <span className={`ai-shorts__status ai-shorts__status--${current.status}`}>{STATUS_LABEL[current.status] ?? current.status}</span>
                </div>
                <p className="ai-shorts__muted">
                  {MODE_LABEL[current.mode ?? "fable"]}
                  {current.account_id ? ` · 账号：${accountName(current.account_id) || current.account_id}` : ""}
                </p>
                {!isExplainer ? (
                  <label>
                    顶部金句
                    <input value={headlineDraft} onChange={(e) => setHeadlineDraft(e.target.value)} />
                  </label>
                ) : null}
                <label>
                  {isExplainer ? "口播文案" : "旁白文案"}
                  <textarea rows={6} value={storyDraft} onChange={(e) => setStoryDraft(e.target.value)} />
                </label>
                <label>
                  拆分镜模型（可直接输入任意模型名；改了要重新拆分镜）
                  <ModelCombo id="ai-shorts-edit-model" aria-label="拆分镜模型" value={textModelDraft} onChange={setTextModelDraft} placeholder={`默认：${defaultTextModel || "设置里的 Grok 模型"}`} />
                </label>
<ReasoningSelect value={reasoningDraft} onChange={setReasoningDraft} disabled={!!busy||active} />
                {isExplainer ? (
                  <label>
                    分大段模型（先按话题切大段再并行拆镜；留空按段落机械切）
                    <ModelCombo id="ai-shorts-edit-segment-model" aria-label="分大段模型" value={segmentModelDraft} onChange={setSegmentModelDraft} placeholder="如 gemini-3.8-flash-high" />
                  </label>
                ) : null}
                {isExplainer ? (
                  <label>
                    画面风格策略（更改后建议重新拆分镜，旧图保留）
                    <select value={styleDraft || "documentary"} onChange={(e) => setStyleDraft(e.target.value)}>
                      {styles.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
                    </select>
                    {styleDraft === "finance_editorial" ? <span className="ai-shorts__muted">大多数镜头使用纸张拼贴，抽象概念和多方关系使用微缩模型；可在每张分镜卡中手动切换。</span> : null}
                  </label>
                ) : (
                  <details>
                    <summary>画风前缀（全片统一）</summary>
                    <textarea rows={3} value={styleDraft} onChange={(e) => setStyleDraft(e.target.value)} />
                  </details>
                )}
                {isExplainer ? <VisualSettingsFields hideOpening value={visualDraft} onChange={setVisualDraft} disabled={!!busy || active} defaultVideoModel={defaultVideoModel} /> : null}
              </details>

              {current.characters.length ? (
                <section className="ai-shorts__card">
                  <h2>角色设定（全片共用外形，标签是开口时的嗓音）</h2>
                  <div className="ai-shorts__characters">
                    {current.characters.map((c, i) => (
                      <div key={c.name} className="ai-shorts__character">
                        {c.image_path ? (
                          <img src={assetURL(current.id, c.image_path)} alt={c.name} />
                        ) : (
                          <div className="ai-shorts__placeholder">{SHOT_STATUS[c.status] ?? c.status}</div>
                        )}
                        <strong>{c.name}{c.voice ? <em className="ai-shorts__voice-tag">{c.voice}</em> : null}</strong>
                        <small>{c.description}</small>
                        {c.error ? <small className="ai-shorts__error">{c.error}</small> : null}
                        <button type="button" className="header-button" disabled={!!busy || active || textDirty} onClick={() => regenCharacter(i)}>
                          <RefreshCw size={12} /> 重画
                        </button>
                      </div>
                    ))}
                  </div>
                </section>
              ) : null}

              <button type="button" className="header-button ai-shorts__back" disabled={!!busy || editingShot !== null || textDirty} onClick={() => onNavigate("/ai-shorts")}><ArrowLeft size={15} /> 返回列表</button>
            </>
          )}

          <section className="ai-shorts__card">
            <h2>短片列表</h2>
            <div aria-live="polite">{listState === "loading" ? <p className="ai-shorts__muted">正在读取短片列表…</p> : listState === "error" ? <button type="button" className="header-button" onClick={() => void reloadList()}>重试读取列表</button> : !shorts.length ? <p className="ai-shorts__muted">还没有短片。创建后会保存在这里。</p> : null}</div>
            <ul className="ai-shorts__list">
              {shorts.map((item) => (
                <li key={item.id} className={item.id === current?.id ? "is-selected" : undefined}>
                  <button type="button" disabled={!!busy || editingShot !== null || textDirty} onClick={() => onNavigate(`/ai-shorts/${item.id}`)}>
                    <strong>{item.headline || item.title}</strong>
                    <span>
                      {accountName(item.account_id) ? <em>{accountName(item.account_id)}</em> : null}
                      <em>{STATUS_LABEL[item.status] ?? item.status}</em>
                      <em>{item.shots.length} 镜</em>
                    </span>
                  </button>
                  <button type="button" className="ai-shorts__delete" aria-label="删除短片" disabled={!!busy || editingShot !== null || textDirty} onClick={() => remove(item.id)}>
                    <Trash2 size={13} />
                  </button>
                </li>
              ))}
            </ul>
          </section>
        </aside>

        {/* 中栏：分镜卡片 */}
        <main className="ai-shorts__main">
          {!current ? (
            <div className="ai-shorts__empty">
              <Film size={40} strokeWidth={1.5} />
              <h2>让画面跟着内容走</h2><p>原句 → 配图意图 → 主体与动作 → 生成图片 → 配音对齐。</p><p className="ai-shorts__muted">每镜可修改配图、运动、字幕关键词和重点标注，最后交付可编辑草稿。</p>
            </div>
          ) : current.shots.length === 0 ? (
            <div className="ai-shorts__empty">
              <Wand2 size={40} strokeWidth={1.5} />
              <p>{isExplainer ? "点左边「拆分镜」，AI 会按完整意思配图，说明为什么配这张图，并给出主体、场景、运动和关键词。" : "点左边「拆分镜」，AI 会把旁白切成 6～12 镜并给出角色表。"}</p>
            </div>
          ) : (
            <>
              <ol className="ai-shorts__shots">
                {current.shots.map((shot) => (
                  <ShotCard
                    key={shot.index}
                    shortID={current.id}
                    shot={shot}
                    projectStyle={current.style}
                    visual={visualDraft}
                    explainer={isExplainer}
                    ready={shotReady(current, shot)}
                    styles={styles}
                    speakers={[NARRATOR, ...current.characters.map((c) => c.name)]}
                    disabled={
                      !!busy || imageModelDirty || videoConfigDirty || (editingShot !== null && editingShot !== shot.index) ||
                      current.status === "assembling" ||
                      shot.image_status === "running" ||
                      shot.video_status === "running"
                    }
                    saving={busy === "保存分镜"}
                    needsVideo={shotNeedsVideo(current,shot)}
                    editing={editingShot === shot.index}
                    draft={shotDraft}
                    onDraft={setShotDraft}
                    onEdit={() => { setEditingShot(shot.index); setShotDraft(draftOf(shot)); }}
                    onCancel={() => setEditingShot(null)}
                    onSave={() => saveShot(shot.index)}
                    onRegen={(stage) => regen(shot.index, stage)}
                  />
                ))}
              </ol>
            </>
          )}
        </main>

        {/* 右栏：成片 */}
        <aside className="ai-shorts__result">
          <section className="ai-shorts__card">
            <h2>草稿交付</h2>
            {!current ? <p className="ai-shorts__muted">生成画面并完成配音后，在这里检查交付结果。</p> : current.draft_path && current.status !== "assembling" ? (
              <>
                <p><strong>{current.draft_name}</strong></p>
                {current.draft_stale ? <p className="ai-shorts__dirty">这是上次导出的草稿。分镜或设置已更改，重新组装后会另存一版。</p> : null}
                <p className="ai-shorts__muted">时长 {current.duration_s?.toFixed(1)} 秒 · 草稿已进剪映，打开剪映即可看到。</p>
                {current.narration_path ? (
                  <audio controls src={assetURL(current.id, current.narration_path)} className="ai-shorts__audio" />
                ) : null}
                <p className="ai-shorts__muted">路径：{current.draft_path}</p>
                {current.captions?.length ? <details className="ai-shorts__caption-timeline"><summary>查看字幕时间轴与关键词（{current.captions.length} 条）</summary>{current.captions.map((cap,i)=><p key={i}><time>{cap.start_s.toFixed(1)}–{cap.end_s.toFixed(1)}s</time> <HighlightedText text={cap.text} words={visualDraft.keywords_enabled?cap.keywords:[]} /></p>)}</details> : null}
                {current.srt_path ? <a className="header-button" href={assetURL(current.id,current.srt_path)} download>下载字幕 SRT</a> : null}
                <p className="ai-shorts__muted">请打开剪映检查画面、字幕与声音，再由你确认发布。草稿完成不代表已发布。</p>
              </>
            ) : current.status === "assembling" ? (
              <p className="ai-shorts__muted">{current.error || "配音 → 对齐分镜 → 生成草稿…"}</p>
            ) : (
              <p className="ai-shorts__muted">
                {isExplainer
                  ? "图片完成后点「配音并进剪映」。整篇配音按时间戳对齐镜头，图片运动、旁白、BGM、字幕和重点标注分别进入可编辑轨道。字幕数值与词组保持完整；每次组装另存草稿。"
                  : "所有镜头出完后点「配音并进剪映」。角色台词是视频模型生成时让角色自己说的，直接用视频声轨；旁白句用账号音色配音，画面裁到这句话的长度；顶部大字用金句。"}
              </p>
            )}
          </section>
        </aside>
      </div>
      </>}
    </div>
  );
}

function ShotCard({
  shortID, shot, projectStyle, visual, explainer, ready, styles, speakers, disabled, editing, draft, onDraft, onEdit, onCancel, onSave, onRegen, saving, needsVideo,
}: {
  shortID: string;
  shot: AiShot;
  projectStyle: string;
  visual: VisualSettings;
  explainer: boolean;
  ready: boolean;
  styles: AiStylePreset[];
  speakers: string[];
  disabled: boolean;
  saving: boolean;
  needsVideo: boolean;
  editing: boolean;
  draft: ShotDraft;
  onDraft: (d: ShotDraft) => void;
  onEdit: () => void;
  onCancel: () => void;
  onSave: () => void;
  onRegen: (stage: "image" | "video") => void;
}) {
  const imgURL = assetURL(shortID, shot.image_path);
  const vidURL = assetURL(shortID, shot.video_path);
  const speaker = shot.speaker || NARRATOR;
  const spoken = !explainer && speaker !== NARRATOR;
  const styleName = styles.find((s) => s.key === shot.style_key)?.name ?? shot.style_key ?? "";
  const shotStyles = styles.filter((style) => style.key === "paper_collage" || style.key === "miniature");
  // 图是按旧提示词出的：描述/规则改过之后没重生。
  const stale = !!shot.image_stale;
  const stateClass = ready ? "done" : needsVideo ? shot.video_status : shot.image_status;
  return (
    <li className={`ai-shorts__shot ai-shorts__shot--${stateClass}`}>
      <div className="ai-shorts__shot-media">
        {explainer ? <FramePreview shot={editing?{...shot,...draft,caption_lines:[draft.narration]}:shot} imageURL={imgURL} settings={visual} videoURL={needsVideo&&shot.video_status==="done"?vidURL:undefined} /> : shot.video_status === "done" && vidURL ? (
          <video src={vidURL} controls muted={!spoken} loop playsInline poster={imgURL || undefined} />
        ) : imgURL ? (
          <img src={imgURL} alt={`第 ${shot.index + 1} 镜画面`} />
        ) : (
          <div className="ai-shorts__placeholder">
            {shot.image_status === "running" ? "生图中…" : shot.video_status === "running" ? "生视频中…" : "待生成"}
          </div>
        )}
        <span className="ai-shorts__shot-no">{shot.index + 1}</span>
        {stale ? <span className="ai-shorts__shot-stale" title="描述或规则改过，这张图还是按旧提示词生成的">图是旧的，点重生图</span> : null}
        <span className="ai-shorts__shot-state">
          {explainer ? (
            <>
              {styleName ? `${styleName} · ` : ""}
              图片 {SHOT_STATUS[shot.image_status] ?? shot.image_status} · {needsVideo?`开场视频 ${SHOT_STATUS[shot.video_status]??shot.video_status}`:cameraMoves[shot.camera_move||"zoom_in"]}
            </>
          ) : (
            <>
              图 {SHOT_STATUS[shot.image_status] ?? shot.image_status} · 视频 {SHOT_STATUS[shot.video_status] ?? shot.video_status} · {shot.seconds}s
              {" · "}{spoken ? "角色开口说" : "旁白配音"}
            </>
          )}
          {shot.end_s ? ` · ${(shot.start_s ?? 0).toFixed(1)}–${shot.end_s.toFixed(1)}s` : ""}
        </span>
      </div>
      <div className="ai-shorts__shot-body">
        {editing ? (
          <fieldset className="ai-shorts__shot-edit" disabled={saving}>
            {explainer ? (
              <div>
                {projectStyle === "finance_editorial" ? (
                  <label>
                    镜头画风
                    <select value={draft.style_key} onChange={(e) => onDraft({ ...draft, style_key: e.target.value })}>
                      {shotStyles.map((style) => <option key={style.key} value={style.key}>{style.name}</option>)}
                    </select>
                  </label>
                ) : null}
                <label>画面类型<select value={draft.subject_type} onChange={(e)=>onDraft({...draft,subject_type:e.target.value})}>{Object.entries(subjectTypes).map(([key,label])=><option key={key} value={key}>{label}</option>)}</select></label>
                <label>
                  画面主体
                  <input value={draft.subject} placeholder="一眼能对上这句话的东西" onChange={(e) => onDraft({ ...draft, subject: e.target.value })} />
                </label>
              </div>
            ) : (
              <label>
                谁说
                <select value={draft.speaker} onChange={(e) => onDraft({ ...draft, speaker: e.target.value })}>
                  {speakers.map((name) => <option key={name} value={name}>{name}</option>)}
                </select>
              </label>
            )}
            <label>{explainer ? "旁白" : "台词"}<textarea rows={2} value={draft.narration} onChange={(e) => onDraft({ ...draft, narration: e.target.value })} /></label>
            <label>画面<textarea rows={3} value={draft.scene} onChange={(e) => onDraft({ ...draft, scene: e.target.value })} /></label>
            {explainer ? <>
              <label>配图意图<textarea rows={2} value={draft.visual_intent} placeholder="这句话在说什么，画面如何说明它" onChange={(e)=>onDraft({...draft,visual_intent:e.target.value})} /></label>
              <label>图片运动<select value={draft.camera_move} onChange={(e)=>onDraft({...draft,camera_move:e.target.value})}>{Object.entries(cameraMoves).map(([key,label])=><option key={key} value={key}>{label}</option>)}</select></label>
              <KeywordEditor narration={draft.narration} words={draft.keywords} onChange={(keywords)=>onDraft({...draft,keywords})} />
              <label>重点标注（可空）<input value={draft.annotation} placeholder="关键转折或总结，独立文字轨" onChange={(e)=>onDraft({...draft,annotation:e.target.value})} /></label>
            </> : null}
            <label>{explainer?"视频动作（启用开场视频时使用）":"镜头/动作"}<textarea rows={2} value={draft.motion} onChange={(e) => onDraft({ ...draft, motion: e.target.value })} /></label>
            <p className="ai-shorts__muted">
              {explainer ? "主体、配图意图、场景更改后需重生图；运动、关键词和标注只需重新组装。" : "选角色名 = 这句由角色在视频里开口说（重生视频才生效）；选「旁白」= 用账号音色配音。"}
            </p>
            <div className="ai-shorts__row">
              <button type="button" className="header-button" disabled={saving} onClick={onCancel}>取消</button>
              <button type="button" className="remix-lab-start" disabled={saving} onClick={onSave}>{saving ? "保存中…" : "保存"}</button>
            </div>
          </fieldset>
        ) : (
          <>
            <p className="ai-shorts__narration">
              {explainer ? null : <span className={`ai-shorts__speaker${spoken ? "" : " is-narrator"}`}>{speaker}</span>}
              「{shot.narration}」
            </p>
            {explainer ? <>
              <p className="ai-shorts__intent"><strong>{subjectTypes[shot.subject_type||"object"]} · 配图意图</strong><span>{shot.visual_intent || "旧分镜尚未填写配图意图，可点改描述补充。"}</span></p>
              {shot.source_text && shot.source_text !== shot.narration ? <details className="ai-shorts__prompt"><summary>拆镜时的原句</summary><p>{shot.source_text}</p></details> : null}
            </> : null}
            {explainer && shot.subject ? <p className="ai-shorts__subject">主体：{shot.subject}</p> : null}
            <p className="ai-shorts__scene">{shot.scene}</p>
            {!explainer ? (
              <p className="ai-shorts__motion">镜头：{shot.motion}{shot.characters?.length ? ` · 出场：${shot.characters.join("、")}` : ""}</p>
            ) : shot.motion ? (
              <p className="ai-shorts__motion">动作：{shot.motion}</p>
            ) : null}
            {explainer ? <div className="ai-shorts__caption-sample"><small>字幕拆分 · {shot.keywords?.length||0} 个关键词</small>{(shot.caption_lines||[shot.narration]).map((line,i)=><p key={i}><HighlightedText text={line} words={visual.keywords_enabled?shot.keywords:[]} /></p>)}{shot.annotation?<small>独立标注：{shot.annotation}</small>:null}</div> : null}
            <div className="ai-shorts__prompts">
              {shot.image_prompt ? (
                <details className="ai-shorts__prompt">
                  <summary>生图提示词</summary>
                  <p>{shot.image_prompt}</p>
                </details>
              ) : null}
              {needsVideo && shot.video_prompt ? (
                <details className="ai-shorts__prompt">
                  <summary>生视频提示词</summary>
                  <p>{shot.video_prompt}</p>
                </details>
              ) : null}
              {shot.image_prompt_used ? <details className="ai-shorts__prompt"><summary>当前图片实际使用的提示词</summary><p>{shot.image_prompt_used}</p></details> : null}
            </div>
            {shot.error ? <p className="ai-shorts__error">{shot.error}</p> : null}
            <div className="ai-shorts__row">
              <button type="button" className="header-button" disabled={disabled} onClick={onEdit}>改描述</button>
              {!needsVideo ? (
                <button type="button" className="header-button" disabled={disabled} onClick={() => onRegen("image")}>重生图</button>
              ) : (
                <>
                  <button type="button" className="header-button" disabled={disabled} onClick={() => onRegen("image")}>重生图+视频</button>
                  <button type="button" className="header-button" disabled={disabled || !shot.image_path} onClick={() => onRegen("video")}>{shot.video_submit_uncertain?"重新提交视频":shot.video_status==="failed"&&shot.video_request_id?"继续查询视频":"只重生视频"}</button>
                </>
              )}
            </div>
          </>
        )}
      </div>
    </li>
  );
}
