import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { AlertTriangle, ChevronLeft, ChevronRight, Copy, Download, Heart, ImagePlus, Loader2, RefreshCw, RotateCcw, Search, Sparkles, Trash2, Wand2, X } from "lucide-react";
import { ModelSelect } from "../ModelSelect";
import type { ImageGeneration, ImageGenerationOutput, ImageGenerationPage, ImageModelOption, ImageModelsResponse } from "./imageStudioTypes";
import "./image-studio.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;
type Ratio = ImageGeneration["ratio"];

export const STUDIO_DRAFT_KEY = "console:image-studio-draft";

const ratios: Array<{ value: Ratio; label: string; hint: string }> = [
  { value: "3:4", label: "3:4", hint: "小红书竖图" },
  { value: "9:16", label: "9:16", hint: "竖屏封面" },
  { value: "1:1", label: "1:1", hint: "方图" },
  { value: "4:3", label: "4:3", hint: "横图" },
];

export const studioStyles: Array<[string, string]> = [
  ["", "不套风格"],
  ["finance_documentary", "财经纪实插画"],
  ["red_ink", "赤墨风"],
  ["old_newspaper", "旧报档案风"],
  ["ledger_investigation", "账本调查风"],
  ["dark_crisis", "暗黑危机风"],
  ["city_era", "城市时代感"],
  ["blackboard", "黑板讲解风"],
  ["custom", "自定义风格"],
];

const fallbackModels: ImageModelOption[] = [
  { id: "grok-imagine-image-2.0", label: "Grok Imagine 2.0", note: "细节最好 · 约 15 秒", available: true },
  { id: "grok-imagine-image", label: "Grok Imagine", note: "快 · 约 7 秒", available: true },
  { id: "grok-imagine-image-quality", label: "Grok Imagine Quality", note: "快 · 约 7 秒 · 画质增强", available: true },
];

const PAGE_SIZE = 24;

type Draft = { prompt: string; enhance: boolean; finalPrompt: string; style: string; customStyle: string; model: string; ratio: Ratio; count: number; textModel: string };

const emptyDraft: Draft = { prompt: "", enhance: false, finalPrompt: "", style: "", customStyle: "", model: "", ratio: "3:4", count: 2, textModel: "" };

function loadDraft(): Draft {
  try {
    const saved = JSON.parse(sessionStorage.getItem(STUDIO_DRAFT_KEY) || "{}") as Partial<Draft>;
    return { ...emptyDraft, ...saved, count: Math.min(4, Math.max(1, Number(saved.count) || emptyDraft.count)) };
  } catch {
    return emptyDraft;
  }
}

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json() as { message?: string };
    return body.message?.trim() || fallback;
  } catch {
    return fallback;
  }
}

/** 换模型或换 Key 之前，原样重试注定失败。 */
export function retryWontHelp(generation: ImageGeneration) {
  return /网关不支持|Key 无效|没有权限|内容安全/.test(generation.error || generation.outputs.find((output) => output.status === "failed")?.error || "");
}

export function isActive(generation: ImageGeneration) {
  return generation.status === "queued" || generation.status === "running";
}

export function generationLabel(generation: ImageGeneration) {
  const ready = generation.outputs.filter((output) => output.status === "ready").length;
  switch (generation.status) {
    case "queued": return "排队中";
    case "running": return ready ? `已出 ${ready}/${generation.count}` : "正在出图";
    case "completed": return `${ready} 张`;
    case "partial": return `出了 ${ready}/${generation.count} 张`;
    default: return "失败";
  }
}

export function relativeTime(iso: string, now = Date.now()) {
  const minutes = Math.round((now - Date.parse(iso)) / 60000);
  if (Number.isNaN(minutes)) return "";
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时前`;
  const date = new Date(iso);
  return `${date.getMonth() + 1}月${date.getDate()}日`;
}

function aspect(ratio: Ratio) {
  const [w, h] = ratio.split(":").map(Number);
  return `${w} / ${h}`;
}

function RatioGlyph({ ratio }: { ratio: Ratio }) {
  const [w, h] = ratio.split(":").map(Number);
  const scale = 16 / Math.max(w, h);
  return <span className="studio-ratio-glyph" aria-hidden="true" style={{ width: `${w * scale}px`, height: `${h * scale}px` }} />;
}

function ModelPicker({ models, value, defaultModel, onChange }: { models: ImageModelOption[]; value: string; defaultModel: string; onChange: (value: string) => void }) {
  const effective = value || defaultModel;
  const known = models.some((model) => model.id === effective);
  const [custom, setCustom] = useState(!known && Boolean(value));
  const current = models.find((model) => model.id === effective);
  return <div className="studio-model">
    <label>
      图片模型
      {custom
        ? <input aria-label="图片模型" value={value} maxLength={128} placeholder="输入网关上的模型名" onChange={(event) => onChange(event.target.value)} />
        : <select aria-label="图片模型" value={effective} onChange={(event) => { if (event.target.value === "__custom") { setCustom(true); onChange(""); } else onChange(event.target.value === defaultModel ? "" : event.target.value); }}>
          {models.map((model) => <option key={model.id} value={model.id} disabled={!model.available}>
            {model.label || model.id}{model.id === defaultModel ? "（默认）" : ""}{model.available ? "" : " · 不可用"}
          </option>)}
          <option value="__custom">自定义模型名…</option>
        </select>}
    </label>
    <small className="studio-model__note">
      {custom ? <button type="button" className="studio-link" onClick={() => { setCustom(false); onChange(""); }}>从列表选择</button> : current?.note || (current && !current.available ? "网关暂不支持" : "")}
    </small>
  </div>;
}

function Placeholder({ ratio, state, error }: { ratio: Ratio; state: "pending" | "failed"; error?: string }) {
  return <div className="studio-tile studio-tile--placeholder" data-state={state} style={{ aspectRatio: aspect(ratio) }}>
    {state === "pending"
      ? <><Loader2 className="studio-spin" size={20} aria-hidden="true" /><span>生成中</span></>
      : <><AlertTriangle size={18} aria-hidden="true" /><span>{error || "这张没出来"}</span></>}
  </div>;
}

function OutputTile({ generation, output, onOpen }: { generation: ImageGeneration; output: ImageGenerationOutput; onOpen: () => void }) {
  if (output.status !== "ready") return <Placeholder ratio={generation.ratio} state={output.status} error={output.error} />;
  return <button type="button" className="studio-tile" style={{ aspectRatio: aspect(generation.ratio) }} onClick={onOpen} aria-label={`查看第 ${output.position + 1} 张大图`}>
    <img src={output.url} alt={`${generation.prompt.slice(0, 40)} 第 ${output.position + 1} 张`} loading="lazy" decoding="async" width={output.width || undefined} height={output.height || undefined} />
  </button>;
}

function GenerationCard({ generation, busy, onOpen, onReuse, onRetry, onFavorite, onDelete }: {
  generation: ImageGeneration; busy: boolean;
  onOpen: (output: ImageGenerationOutput) => void; onReuse: () => void; onRetry: () => void; onFavorite: () => void; onDelete: () => void;
}) {
  const failed = generation.outputs.filter((output) => output.status === "failed").length;
  const ready = generation.outputs.filter((output) => output.status === "ready");
  const allFailed = generation.status === "failed" && failed === generation.outputs.length;
  const tone = generation.status === "failed" ? "attention" : generation.status === "partial" ? "attention" : isActive(generation) ? "working" : "ready";
  return <article className="studio-generation" data-status={generation.status} aria-label={`出图：${generation.prompt.slice(0, 30)}`}>
    <div className="studio-generation__tiles" data-count={generation.count} data-all-failed={allFailed ? "true" : undefined}>
      {generation.outputs.map((output) => <OutputTile key={output.id} generation={generation} output={output} onOpen={() => onOpen(output)} />)}
    </div>
    <div className="studio-generation__body">
      <p className="studio-generation__prompt" title={generation.final_prompt || generation.prompt}>{generation.prompt}</p>
      {generation.error && generation.status !== "completed" && !generation.outputs.some((output) => output.status === "failed" && output.error === generation.error) ? <p className="studio-generation__error" role="alert">{generation.error}</p> : null}
      <div className="studio-generation__meta">
        <span className="studio-status" data-status={tone}>{generationLabel(generation)}</span>
        <span>{generation.model}</span>
        <span>{generation.ratio}</span>
        {generation.enhance ? <span className="studio-chip"><Sparkles size={11} aria-hidden="true" />扩写</span> : null}
        <span>{relativeTime(generation.created_at)}</span>
      </div>
      <div className="studio-generation__actions">
        <button type="button" onClick={onReuse} title="把这次的提示词和参数填回上面"><RotateCcw size={14} aria-hidden="true" />再来一组</button>
        {failed && !isActive(generation) && !retryWontHelp(generation) ? <button type="button" disabled={busy} onClick={onRetry}><RefreshCw size={14} aria-hidden="true" />补出 {failed} 张</button> : null}
        {ready.length > 1 ? <a href={`/api/image-studio/generations/${generation.id}/download`} download><Download size={14} aria-hidden="true" />全部</a> : null}
        <span className="studio-grow" />
        <button type="button" className="studio-icon" aria-pressed={generation.favorite} aria-label={generation.favorite ? "取消收藏这次出图" : "收藏这次出图"} disabled={busy} onClick={onFavorite}><Heart size={15} aria-hidden="true" fill={generation.favorite ? "currentColor" : "none"} /></button>
        <button type="button" className="studio-icon studio-icon--danger" aria-label="删除这次出图" disabled={busy} onClick={onDelete}><Trash2 size={15} aria-hidden="true" /></button>
      </div>
    </div>
  </article>;
}

type LightboxState = { generation: ImageGeneration; index: number };

function Lightbox({ state, onClose, onMove, onReuse, onCopy }: { state: LightboxState; onClose: () => void; onMove: (delta: number) => void; onReuse: () => void; onCopy: (text: string) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const ready = state.generation.outputs.filter((output) => output.status === "ready");
  const output = ready[state.index];
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
      if (event.key === "ArrowRight") onMove(1);
      if (event.key === "ArrowLeft") onMove(-1);
    };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); previous?.focus?.(); };
  }, [onClose, onMove]);
  if (!output) return null;
  return <div className="studio-lightbox" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="studio-lightbox__panel" role="dialog" aria-modal="true" aria-label="大图预览" tabIndex={-1} ref={ref}>
      <div className="studio-lightbox__stage">
        <img src={output.url} alt={`第 ${output.position + 1} 张大图`} />
        {ready.length > 1 ? <>
          <button type="button" className="studio-lightbox__nav" data-dir="prev" aria-label="上一张" onClick={() => onMove(-1)}><ChevronLeft size={22} aria-hidden="true" /></button>
          <button type="button" className="studio-lightbox__nav" data-dir="next" aria-label="下一张" onClick={() => onMove(1)}><ChevronRight size={22} aria-hidden="true" /></button>
          <span className="studio-lightbox__count">{state.index + 1} / {ready.length}</span>
        </> : null}
      </div>
      <aside className="studio-lightbox__side">
        <header>
          <strong>第 {output.position + 1} 张</strong>
          <button type="button" className="studio-icon" aria-label="关闭大图" onClick={onClose}><X size={18} aria-hidden="true" /></button>
        </header>
        <dl>
          <dt>尺寸</dt><dd>{output.width}×{output.height}</dd>
          <dt>模型</dt><dd>{state.generation.model}</dd>
          <dt>比例</dt><dd>{state.generation.ratio}</dd>
        </dl>
        <div className="studio-lightbox__prompt">
          <span>{state.generation.final_prompt && state.generation.final_prompt !== state.generation.prompt ? "实际提示词" : "提示词"}</span>
          <p>{state.generation.final_prompt || state.generation.prompt}</p>
        </div>
        <div className="studio-lightbox__actions">
          <a className="primary" href={`${output.url}?download=1`} download><Download size={15} aria-hidden="true" />下载这张</a>
          <button type="button" onClick={() => onCopy(state.generation.final_prompt || state.generation.prompt)}><Copy size={15} aria-hidden="true" />复制提示词</button>
          <button type="button" onClick={onReuse}><RotateCcw size={15} aria-hidden="true" />用这组参数再出</button>
        </div>
      </aside>
    </div>
  </div>;
}

/** 单张出图：一段提示词直接出 1–4 张，所有结果存进出图历史。 */
export function ImageStudio({ api, onOpenSettings, notice }: { api: API; onOpenSettings?: () => void; notice?: ReactNode }) {
  const [draft, setDraft] = useState<Draft>(loadDraft);
  const [models, setModels] = useState<ImageModelsResponse | null>(null);
  const [history, setHistory] = useState<ImageGeneration[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [listState, setListState] = useState<"loading" | "ready" | "error">("loading");
  const [query, setQuery] = useState("");
  const [favoritesOnly, setFavoritesOnly] = useState(false);
  const [busy, setBusy] = useState<"" | "enhance" | "generate" | "more" | string>("");
  const [message, setMessage] = useState<{ tone: "error" | "ok"; text: string } | null>(null);
  const [lightbox, setLightbox] = useState<LightboxState | null>(null);
  const promptRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    try { sessionStorage.setItem(STUDIO_DRAFT_KEY, JSON.stringify(draft)); } catch { /* storage may be unavailable */ }
  }, [draft]);
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));

  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const response = await api("/api/image-studio/models");
        if (!response.ok) throw new Error();
        const body = await response.json() as ImageModelsResponse;
        if (alive) setModels(body);
      } catch {
        if (alive) setModels({ default_model: fallbackModels[0].id, models: fallbackModels, key_configured: true, key_source: "image_text", list_error: "模型列表读取失败，先用内置列表" });
      }
    })();
    return () => { alive = false; };
  }, [api]);

  const listQuery = useCallback((before = "") => {
    const params = new URLSearchParams({ limit: String(PAGE_SIZE) });
    if (before) params.set("before", before);
    if (favoritesOnly) params.set("favorite", "1");
    if (query.trim()) params.set("q", query.trim());
    return `/api/image-studio/generations?${params}`;
  }, [favoritesOnly, query]);

  const loadHistory = useCallback(async () => {
    try {
      const response = await api(listQuery());
      if (!response.ok) throw new Error();
      const page = await response.json() as ImageGenerationPage;
      setHistory(page.items);
      setHasMore(page.has_more);
      setListState("ready");
    } catch {
      setListState("error");
    }
  }, [api, listQuery]);

  useEffect(() => {
    const timer = window.setTimeout(() => void loadHistory(), query ? 250 : 0);
    return () => window.clearTimeout(timer);
  }, [loadHistory, query]);

  const activeIDs = useMemo(() => history.filter(isActive).map((generation) => generation.id), [history]);
  useEffect(() => {
    if (!activeIDs.length) return;
    const timer = window.setInterval(() => {
      void Promise.all(activeIDs.map(async (id) => {
        try {
          const response = await api(`/api/image-studio/generations/${id}`);
          return response.ok ? await response.json() as ImageGeneration : null;
        } catch {
          return null;
        }
      })).then((updated) => {
        const byID = new Map(updated.filter(Boolean).map((generation) => [generation!.id, generation!]));
        if (byID.size) setHistory((current) => current.map((generation) => byID.get(generation.id) ?? generation));
      });
    }, 2000);
    return () => window.clearInterval(timer);
  }, [activeIDs, api]);

  const loadMore = async () => {
    const last = history[history.length - 1];
    if (!last) return;
    setBusy("more");
    try {
      const response = await api(listQuery(last.created_at));
      if (!response.ok) throw new Error();
      const page = await response.json() as ImageGenerationPage;
      setHistory((current) => [...current, ...page.items.filter((item) => !current.some((existing) => existing.id === item.id))]);
      setHasMore(page.has_more);
    } catch {
      setMessage({ tone: "error", text: "更多历史读取失败，稍后再试。" });
    } finally {
      setBusy("");
    }
  };

  const json = (method: string, body: unknown): RequestInit => ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

  const enhance = async () => {
    if (!draft.prompt.trim()) return;
    setBusy("enhance");
    setMessage(null);
    try {
      const response = await api("/api/image-studio/enhance", json("POST", { prompt: draft.prompt.trim(), style: draft.style, custom_style: draft.customStyle.trim(), ratio: draft.ratio, text_model: draft.textModel }));
      if (!response.ok) throw new Error(await readError(response, "扩写失败，可以直接用原话出图。"));
      const body = await response.json() as { final_prompt: string };
      setDraft((current) => ({ ...current, enhance: true, finalPrompt: body.final_prompt }));
    } catch (error) {
      setMessage({ tone: "error", text: error instanceof Error ? error.message : "扩写失败。" });
    } finally {
      setBusy("");
    }
  };

  const customMissing = draft.style === "custom" && !draft.customStyle.trim();
  const notConfigured = models !== null && !models.key_configured;
  const canSubmit = Boolean(draft.prompt.trim()) && !customMissing && !notConfigured && busy !== "generate";

  const generate = async (event?: FormEvent) => {
    event?.preventDefault();
    if (!canSubmit) return;
    setBusy("generate");
    setMessage(null);
    try {
      const response = await api("/api/image-studio/generations", json("POST", {
        prompt: draft.prompt.trim(),
        final_prompt: draft.enhance ? draft.finalPrompt.trim() : "",
        enhance: draft.enhance,
        style: draft.style,
        custom_style: draft.style === "custom" ? draft.customStyle.trim() : "",
        model: draft.model.trim(),
        ratio: draft.ratio,
        count: draft.count,
        text_model: draft.textModel,
      }));
      if (!response.ok) throw new Error(await readError(response, "出图请求没有成功，请稍后再试。"));
      const created = await response.json() as ImageGeneration;
      setHistory((current) => [created, ...current.filter((item) => item.id !== created.id)]);
      if (favoritesOnly || query) { setFavoritesOnly(false); setQuery(""); }
    } catch (error) {
      setMessage({ tone: "error", text: error instanceof Error ? error.message : "出图请求没有成功。" });
    } finally {
      setBusy("");
    }
  };

  const replace = (generation: ImageGeneration) => setHistory((current) => current.map((item) => item.id === generation.id ? generation : item));
  const guarded = async (id: string, work: () => Promise<void>) => {
    setBusy(id);
    try { await work(); } catch (error) { setMessage({ tone: "error", text: error instanceof Error ? error.message : "操作失败。" }); } finally { setBusy(""); }
  };
  const toggleFavorite = (generation: ImageGeneration) => guarded(generation.id, async () => {
    const response = await api(`/api/image-studio/generations/${generation.id}`, json("PATCH", { favorite: !generation.favorite }));
    if (!response.ok) throw new Error(await readError(response, "收藏失败。"));
    const updated = await response.json() as ImageGeneration;
    if (favoritesOnly && !updated.favorite) setHistory((current) => current.filter((item) => item.id !== updated.id));
    else replace(updated);
  });
  const retry = (generation: ImageGeneration) => guarded(generation.id, async () => {
    const response = await api(`/api/image-studio/generations/${generation.id}/retry`, { method: "POST" });
    if (!response.ok) throw new Error(await readError(response, "重试失败。"));
    replace(await response.json() as ImageGeneration);
  });
  const remove = (generation: ImageGeneration) => {
    const ready = generation.outputs.filter((output) => output.status === "ready").length;
    if (!window.confirm(ready ? `删除这次出图和 ${ready} 张图片？删除后找不回来。` : "删除这次出图记录？")) return;
    void guarded(generation.id, async () => {
      const response = await api(`/api/image-studio/generations/${generation.id}`, { method: "DELETE" });
      if (!response.ok && response.status !== 404) throw new Error(await readError(response, "删除失败。"));
      setHistory((current) => current.filter((item) => item.id !== generation.id));
    });
  };
  const reuse = (generation: ImageGeneration) => {
    setDraft({
      prompt: generation.prompt,
      enhance: generation.enhance,
      finalPrompt: generation.enhance ? generation.final_prompt : "",
      style: generation.style,
      customStyle: generation.custom_style,
      model: generation.model === models?.default_model ? "" : generation.model,
      ratio: generation.ratio,
      count: generation.count,
      textModel: draft.textModel,
    });
    setLightbox(null);
    window.scrollTo({ top: 0, behavior: "smooth" });
    window.setTimeout(() => promptRef.current?.focus(), 250);
  };
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); setMessage({ tone: "ok", text: "提示词已复制。" }); }
    catch { setMessage({ tone: "error", text: "复制失败。" }); }
  };
  const openLightbox = (generation: ImageGeneration, output: ImageGenerationOutput) => {
    const ready = generation.outputs.filter((item) => item.status === "ready");
    setLightbox({ generation, index: Math.max(0, ready.findIndex((item) => item.id === output.id)) });
  };
  const moveLightbox = useCallback((delta: number) => setLightbox((current) => {
    if (!current) return current;
    const total = current.generation.outputs.filter((item) => item.status === "ready").length;
    return total ? { ...current, index: (current.index + delta + total) % total } : current;
  }), []);
  const closeLightbox = useCallback(() => setLightbox(null), []);

  const modelOptions = models?.models.length ? models.models : fallbackModels;
  const defaultModel = models?.default_model || fallbackModels[0].id;
  const promptLength = Array.from(draft.prompt).length;

  return <div className="image-studio">
    {notice}
    {notConfigured ? <div className="studio-banner" role="alert">
      <AlertTriangle size={16} aria-hidden="true" />
      <span>还没配置生图服务的 Key，出不了图。</span>
      {onOpenSettings ? <button type="button" className="studio-link" onClick={onOpenSettings}>打开设置 → 图片</button> : null}
    </div> : null}
    {models?.list_error ? <p className="studio-hint" role="status">{models.list_error}</p> : null}

    <form className="studio-composer" onSubmit={(event) => void generate(event)}>
      <section className="studio-composer__main">
        <label className="studio-prompt">
          <span className="studio-prompt__label">想要什么画面</span>
          <textarea
            ref={promptRef}
            aria-label="提示词"
            value={draft.prompt}
            maxLength={4000}
            rows={5}
            onChange={(event) => setDraft((current) => ({ ...current, prompt: event.target.value, finalPrompt: current.enhance ? current.finalPrompt : "" }))}
            onKeyDown={(event) => { if ((event.metaKey || event.ctrlKey) && event.key === "Enter") void generate(); }}
            placeholder="例：小红书封面，暖色手帐风，一张旧书桌上摊开考研单词本，旁边一杯热咖啡和便利贴，标题「30天背完5500词」"
          />
        </label>
        <div className="studio-prompt__bar">
          <span className="studio-hint">{promptLength ? `${promptLength} 字` : "直接写需求就行，Ctrl+Enter 出图"}</span>
          <span className="studio-grow" />
          {draft.enhance
            ? <button type="button" className="studio-link" onClick={() => setDraft((current) => ({ ...current, enhance: false, finalPrompt: "" }))}>改回用原话</button>
            : null}
          <button type="button" className="studio-enhance" disabled={!draft.prompt.trim() || busy === "enhance"} onClick={() => void enhance()}>
            {busy === "enhance" ? <Loader2 className="studio-spin" size={15} aria-hidden="true" /> : <Wand2 size={15} aria-hidden="true" />}
            {busy === "enhance" ? "正在扩写…" : draft.enhance ? "重新扩写" : "AI 帮我扩写"}
          </button>
        </div>
        {draft.enhance ? <label className="studio-expanded">
          <span><Sparkles size={13} aria-hidden="true" />扩写后的提示词（可以直接改，出图用这段）</span>
          <textarea aria-label="扩写后的提示词" value={draft.finalPrompt} maxLength={4000} rows={6} onChange={(event) => set("finalPrompt", event.target.value)} />
        </label> : null}
      </section>

      <aside className="studio-composer__rail">
        <ModelPicker models={modelOptions} value={draft.model} defaultModel={defaultModel} onChange={(value) => set("model", value)} />
        <fieldset className="studio-ratios">
          <legend>比例</legend>
          <div role="radiogroup" aria-label="图片比例">
            {ratios.map((option) => <button key={option.value} type="button" role="radio" aria-checked={draft.ratio === option.value} title={option.hint} onClick={() => set("ratio", option.value)}>
              <RatioGlyph ratio={option.value} /><span>{option.label}</span>
            </button>)}
          </div>
        </fieldset>
        <fieldset className="studio-count">
          <legend>一次出几张</legend>
          <div role="radiogroup" aria-label="出图张数">
            {[1, 2, 3, 4].map((value) => <button key={value} type="button" role="radio" aria-checked={draft.count === value} onClick={() => set("count", value)}>{value}</button>)}
          </div>
        </fieldset>
        <label>
          风格
          <select aria-label="风格" value={draft.style} onChange={(event) => set("style", event.target.value)}>
            {studioStyles.map(([value, label]) => <option key={value || "none"} value={value}>{label}</option>)}
          </select>
        </label>
        {draft.style === "custom" ? <label>
          自定义风格
          <textarea aria-label="自定义风格" rows={3} maxLength={500} value={draft.customStyle} onChange={(event) => set("customStyle", event.target.value)} placeholder="材质、光线、色彩、构图，例如：温暖手帐风，牛皮纸底，水彩淡彩" />
        </label> : null}
        {draft.enhance || busy === "enhance" ? <label>
          扩写用的文本模型
          <ModelSelect aria-label="扩写文本模型" value={draft.textModel} emptyLabel="默认图文文本模型" onChange={(value) => set("textModel", value)} />
        </label> : null}
        {message ? <p className="studio-message" data-tone={message.tone} role={message.tone === "error" ? "alert" : "status"}>{message.text}</p> : null}
        <button type="submit" className="primary studio-submit" disabled={!canSubmit}>
          {busy === "generate" ? <Loader2 className="studio-spin" size={16} aria-hidden="true" /> : <ImagePlus size={16} aria-hidden="true" />}
          {busy === "generate" ? "正在提交…" : `出 ${draft.count} 张图`}
        </button>
      </aside>
    </form>

    <section className="studio-history" aria-labelledby="studio-history-title">
      <header className="studio-history__head">
        <div>
          <span className="image-kicker">出图历史</span>
          <h2 id="studio-history-title">我出过的图</h2>
        </div>
        <div className="studio-history__tools">
          <label className="studio-search"><Search size={15} aria-hidden="true" /><input type="search" aria-label="搜索出图历史" placeholder="搜提示词" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
          <button type="button" className="studio-filter" aria-pressed={favoritesOnly} onClick={() => setFavoritesOnly((value) => !value)}><Heart size={14} aria-hidden="true" fill={favoritesOnly ? "currentColor" : "none"} />只看收藏</button>
          <button type="button" className="studio-icon" aria-label="刷新出图历史" onClick={() => void loadHistory()}><RefreshCw size={15} aria-hidden="true" /></button>
        </div>
      </header>
      {listState === "loading" ? <p className="studio-hint" role="status">正在读取…</p> : null}
      {listState === "error" ? <div className="studio-banner" role="alert"><span>出图历史读取失败。</span><button type="button" className="studio-link" onClick={() => void loadHistory()}>重试</button></div> : null}
      {listState === "ready" && history.length === 0 ? <div className="studio-empty">
        <ImagePlus size={28} strokeWidth={1.5} aria-hidden="true" />
        <strong>{query || favoritesOnly ? "没有匹配的出图" : "还没有出过图"}</strong>
        <span>{query || favoritesOnly ? "换个关键词，或取消「只看收藏」。" : "在上面写一句画面需求，出的图都会保存在这里。"}</span>
      </div> : null}
      <div className="studio-history__grid">
        {history.map((generation) => <GenerationCard key={generation.id} generation={generation} busy={busy === generation.id}
          onOpen={(output) => openLightbox(generation, output)} onReuse={() => reuse(generation)} onRetry={() => void retry(generation)}
          onFavorite={() => void toggleFavorite(generation)} onDelete={() => remove(generation)} />)}
      </div>
      {hasMore ? <button type="button" className="studio-more" disabled={busy === "more"} onClick={() => void loadMore()}>{busy === "more" ? "加载中…" : "加载更早的"}</button> : null}
    </section>

    {lightbox ? <Lightbox state={lightbox} onClose={closeLightbox} onMove={moveLightbox} onReuse={() => reuse(lightbox.generation)} onCopy={(text) => void copy(text)} /> : null}
  </div>;
}
