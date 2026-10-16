import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { ArrowLeft, Check, ChevronDown, Clapperboard, Download, Film, ImagePlus, Images, Loader2, Plus, RefreshCw, ScanSearch, ShieldCheck, Sparkles, Trash2, Upload, Wand2, X } from "lucide-react";
import type { ReplicateGenerateResponse, ReplicateProject, ReplicateProvider, ReplicateProvidersResponse, ReplicateShot, ReplicateTake } from "./replicateTypes";
import type { VideoQCCheck } from "../references/videoQCTypes";
import { VideoQCPanel, VideoQCResult } from "./VideoQC";
import { useModelOptions } from "../ModelSelect";
import { loadImageModels } from "../image-mode/ImageModelField";
import type { ImageModelOption } from "../image-mode/imageStudioTypes";
import "../shell/studio-page.css";
import "./replicate.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;
type Message = { tone: "ok" | "error" | "info"; text: string };

const ratios: Array<ReplicateProject["ratio"]> = ["9:16", "3:4", "1:1", "4:3", "16:9"];
const statusLabels: Record<ReplicateProject["status"], { label: string; tone: string }> = {
  draft: { label: "编辑分镜", tone: "draft" }, generating: { label: "生成中", tone: "working" }, ready: { label: "可以拼接", tone: "ready" },
  stitching: { label: "拼接中", tone: "working" }, stitched: { label: "已出成片", tone: "ready" }, failed: { label: "需要处理", tone: "attention" },
};
const takeLabels: Record<ReplicateTake["status"], string> = { queued: "排队中", submitting: "提交中", running: "生成中", succeeded: "完成", failed: "失败", canceled: "已取消" };

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json() as { message?: string };
    return body.message?.trim() || fallback;
  } catch {
    return fallback;
  }
}

const json = (method: string, body: unknown): RequestInit => ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

export function takeActive(take: ReplicateTake) {
  return take.status === "queued" || take.status === "submitting" || take.status === "running";
}

function aspect(ratio: ReplicateProject["ratio"]) {
  const [w, h] = ratio.split(":").map(Number);
  return `${w} / ${h}`;
}

function clock(seconds: number) {
  const total = Math.max(0, Math.round(seconds * 10) / 10);
  return `${Math.floor(total / 60)}:${(total % 60).toFixed(1).padStart(4, "0")}`;
}

/* ----------------------------- 列表与新建 ----------------------------- */

type AnalysisOption = { id: string; reference_id: string; title: string; created_at: string; prompts: number; product: string };

function NewProjectDialog({ api, presetAnalysisID, onClose, onCreated }: { api: API; presetAnalysisID?: string; onClose: () => void; onCreated: (project: ReplicateProject) => void }) {
  const [analyses, setAnalyses] = useState<AnalysisOption[] | null>(null);
  const [analysisID, setAnalysisID] = useState(presetAnalysisID || "");
  const [product, setProduct] = useState("");
  const [ratio, setRatio] = useState<ReplicateProject["ratio"]>("9:16");
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const references = await (await api("/api/references?limit=100")).json() as Array<{ id: string; title: string; caption: string }>;
        const lists = await Promise.all(references.slice(0, 60).map(async (reference) => {
          try {
            const response = await api(`/api/reference-analyses?reference_id=${encodeURIComponent(reference.id)}`);
            if (!response.ok) return [];
            const items = await response.json() as Array<{ id: string; status: string; created_at: string; product?: string }>;
            return items.filter((item) => item.status === "completed").map((item) => ({ id: item.id, reference_id: reference.id, title: reference.title || reference.caption.slice(0, 30) || "（无标题）", created_at: item.created_at, prompts: 0, product: item.product || "" }));
          } catch {
            return [];
          }
        }));
        const flat = lists.flat().sort((a, b) => b.created_at.localeCompare(a.created_at));
        if (alive) { setAnalyses(flat); if (!presetAnalysisID && flat[0]) setAnalysisID(flat[0].id); }
      } catch {
        if (alive) setAnalyses([]);
      }
    })();
    return () => { alive = false; };
  }, [api, presetAnalysisID]);
  const chosen = analyses?.find((item) => item.id === analysisID);
  useEffect(() => { if (chosen?.product && !product) setProduct(chosen.product); }, [chosen, product]);

  const create = async (blank: boolean) => {
    setBusy(true);
    setError("");
    try {
      const body = blank
        ? { blank: true, title: title.trim() || "空白复刻项目", ratio }
        : { reference_id: chosen?.reference_id || "", analysis_id: analysisID, product: product.trim(), ratio };
      const response = await api("/api/replicate", json("POST", body));
      if (!response.ok) throw new Error(await readError(response, "创建失败。"));
      onCreated(await response.json() as ReplicateProject);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败。");
      setBusy(false);
    }
  };

  return <div className="studio-dialog-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="studio-dialog rp-new" role="dialog" aria-modal="true" aria-label="新建复刻项目">
      <header><h2>新建复刻项目</h2><button type="button" className="studio-button studio-button--quiet" aria-label="关闭" onClick={onClose}><X size={16} aria-hidden="true" /></button></header>
      <div className="rp-new__body">
        <label className="studio-field">选一份拆解
          {analyses === null ? <span className="studio-metrics" role="status">正在读取拆解记录…</span>
            : analyses.length ? <select aria-label="选择拆解" value={analysisID} onChange={(event) => setAnalysisID(event.target.value)}>
              {analyses.map((item) => <option key={item.id} value={item.id}>{item.title} · {new Date(item.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</option>)}
            </select> : <span className="studio-metrics">还没有完成的拆解。先在「对标采集」里打开一条视频，点「拆解分析」。</span>}
          <small>系统会把拆解里每个镜头的复刻提示词变成分镜。</small>
        </label>
        <label className="studio-field">我的产品或场景
          <textarea rows={2} maxLength={300} aria-label="我的产品或场景" value={product} onChange={(event) => setProduct(event.target.value)} placeholder="例如：岚山海鲜大排档，现捞梭子蟹和皮皮虾" />
        </label>
        <fieldset className="rp-ratio">
          <legend>画面比例</legend>
          <div role="radiogroup" aria-label="画面比例">{ratios.map((value) => <button key={value} type="button" role="radio" aria-checked={ratio === value} onClick={() => setRatio(value)}>{value}</button>)}</div>
        </fieldset>
        {error ? <p className="studio-notice" data-tone="error" role="alert">{error}</p> : null}
      </div>
      <footer>
        <details className="rp-new__blank">
          <summary>不用拆解，空白开始</summary>
          <div className="studio-row"><input aria-label="项目名称" value={title} maxLength={60} onChange={(event) => setTitle(event.target.value)} placeholder="项目名称" /><button type="button" className="studio-button" disabled={busy} onClick={() => void create(true)}>空白新建</button></div>
        </details>
        <span className="grow" />
        <button type="button" className="studio-button" onClick={onClose}>取消</button>
        <button type="button" className="studio-button studio-button--primary" disabled={busy || !analysisID} onClick={() => void create(false)}>{busy ? "正在创建…" : "用这份拆解开始"}</button>
      </footer>
    </div>
  </div>;
}

function ProjectList({ api, onOpen, presetAnalysisID, onMessage }: { api: API; onOpen: (id: string) => void; presetAnalysisID?: string; onMessage: (message: Message) => void }) {
  const [projects, setProjects] = useState<ReplicateProject[] | null>(null);
  const [creating, setCreating] = useState(Boolean(presetAnalysisID));
  const load = useCallback(async () => {
    try {
      const response = await api("/api/replicate?limit=60");
      if (!response.ok) throw new Error();
      setProjects(await response.json() as ReplicateProject[]);
    } catch {
      setProjects([]);
      onMessage({ tone: "error", text: "复刻项目读取失败。" });
    }
  }, [api, onMessage]);
  useEffect(() => { void load(); }, [load]);
  return <section className="studio-panel" aria-labelledby="rp-list-title">
    <header>
      <div><h2 id="rp-list-title">复刻项目</h2><p>每个项目对应一条要复刻的爆款：分镜、首帧、视频版本和成片都存在里面。</p></div>
      <button type="button" className="studio-button studio-button--primary" onClick={() => setCreating(true)}><Plus size={15} aria-hidden="true" />新建复刻</button>
    </header>
    {projects === null ? <p className="studio-empty" role="status">正在读取…</p> : null}
    {projects?.length === 0 ? <div className="studio-empty">
      <Clapperboard size={28} strokeWidth={1.5} aria-hidden="true" />
      <strong>还没有复刻项目</strong>
      <span>先在「对标采集」里拆解一条爆款，再点「用这些提示词开始 AI 复刻」，或者直接在这里新建。</span>
    </div> : null}
    {projects?.length ? <ul className="rp-cards">{projects.map((project) => {
      const status = statusLabels[project.status];
      const done = project.progress.takes_done;
      return <li key={project.id}>
        <button type="button" onClick={() => onOpen(project.id)}>
          <span className="rp-cards__top"><span className="studio-status" data-status={status.tone}>{status.label}</span><span className="studio-metrics">{project.ratio}</span></span>
          <strong>{project.title}</strong>
          {project.product ? <span className="rp-cards__product">{project.product}</span> : null}
          <span className="studio-metrics"><span>{project.video_provider === "grok" ? "Grok" : "MiniMax H3"}</span>{project.progress.takes_total ? <span>视频 {done}/{project.progress.takes_total}</span> : null}{project.final_duration_s ? <span>成片 {project.final_duration_s.toFixed(1)}s</span> : null}</span>
        </button>
      </li>;
    })}</ul> : null}
    {creating ? <NewProjectDialog api={api} presetAnalysisID={presetAnalysisID} onClose={() => setCreating(false)} onCreated={(project) => { setCreating(false); onOpen(project.id); }} /> : null}
  </section>;
}

/* ----------------------------- 镜头行 ----------------------------- */

function TakeTile({ take, ratio, selected, busy, onSelect, onCancel, onDelete }: { take: ReplicateTake; ratio: ReplicateProject["ratio"]; selected: boolean; busy: boolean; onSelect: () => void; onCancel: () => void; onDelete: () => void }) {
  const active = takeActive(take);
  return <div className="rp-take" data-status={take.status} data-selected={selected ? "true" : undefined}>
    <div className="rp-take__media" style={{ aspectRatio: aspect(ratio) }}>
      {take.status === "succeeded" && take.url
        ? <video src={take.url} muted loop playsInline preload="metadata" onMouseEnter={(event) => void event.currentTarget.play().catch(() => undefined)} onMouseLeave={(event) => event.currentTarget.pause()} controls={false} aria-label="视频版本" />
        : <span className="rp-take__state">{active ? <Loader2 size={18} className="reference-spin" aria-hidden="true" /> : null}<span>{take.status === "failed" ? (take.error || "生成失败") : takeLabels[take.status]}</span></span>}
      <span className="rp-take__badge">{take.provider === "grok" ? "Grok" : "H3"}</span>
    </div>
    <div className="rp-take__bar">
      {take.status === "succeeded"
        ? <button type="button" className="rp-take__pick" aria-pressed={selected} disabled={busy} onClick={onSelect}>{selected ? <><Check size={13} aria-hidden="true" />已选用</> : "选这版"}</button>
        : active ? <button type="button" className="rp-take__ghost" disabled={busy} onClick={onCancel}>取消</button> : <span />}
      <button type="button" className="rp-take__ghost" aria-label="删除这个版本" disabled={busy || active} onClick={onDelete}><Trash2 size={13} aria-hidden="true" /></button>
    </div>
  </div>;
}

function PromptField({ label, value, rows = 3, hint, onSave }: { label: string; value: string; rows?: number; hint?: string; onSave: (value: string) => void }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return <label className="studio-field rp-prompt">
    <span className="rp-prompt__label">{label}{hint ? <small>{hint}</small> : null}</span>
    <textarea rows={rows} value={draft} aria-label={label} onChange={(event) => setDraft(event.target.value)} onBlur={() => { if (draft.trim() !== value.trim()) onSave(draft.trim()); }} />
  </label>;
}

function ShotRow({ project, shot, provider, busyKey, onPatch, onFirstFrame, onUpload, onUseReference, onGenerate, onTake, onDelete }: {
  project: ReplicateProject; shot: ReplicateShot; provider: ReplicateProvider | undefined; busyKey: string;
  onPatch: (fields: Partial<ReplicateShot>) => void; onFirstFrame: () => void; onUpload: (file: File) => void; onUseReference: () => void;
  onGenerate: (variants: number) => void; onTake: (action: "select" | "cancel" | "delete", take: ReplicateTake) => void; onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [variants, setVariants] = useState(project.variants || 1);
  useEffect(() => setVariants(project.variants || 1), [project.variants]);
  const fileRef = useRef<HTMLInputElement>(null);
  const frameReady = shot.first_frame_status === "ready" && Boolean(shot.first_frame_url);
  const durations = provider?.durations ?? [6];
  const running = shot.takes.some(takeActive);
  return <li className="rp-shot" aria-label={`镜头 ${shot.position}`}>
    <div className="rp-shot__index"><span>{String(shot.position).padStart(2, "0")}</span>{shot.source_end_s > shot.source_start_s ? <small>{clock(shot.source_start_s)}</small> : null}</div>
    <div className="rp-shot__frame">
      <div className="rp-frame" style={{ aspectRatio: aspect(project.ratio) }} data-status={shot.first_frame_status}>
        {frameReady ? <img src={shot.first_frame_url} alt={`镜头 ${shot.position} 首帧`} />
          : shot.first_frame_status === "generating" ? <span className="rp-frame__state"><Loader2 size={18} className="reference-spin" aria-hidden="true" />出图中</span>
          : shot.first_frame_status === "failed" ? <span className="rp-frame__state rp-frame__state--error">{shot.first_frame_error || "出图失败"}</span>
          : <span className="rp-frame__state">还没有首帧</span>}
        {shot.first_frame_source === "reference_frame" ? <span className="rp-frame__tag" title="这是同行原片的画面，正式发布前请换成自己的">同行画面</span> : shot.first_frame_source === "uploaded" ? <span className="rp-frame__tag">我的实拍</span> : null}
      </div>
      <div className="rp-frame__actions">
        <button type="button" className="studio-button studio-button--quiet" disabled={busyKey === `frame:${shot.id}` || shot.first_frame_status === "generating"} onClick={onFirstFrame}><Sparkles size={13} aria-hidden="true" />{frameReady ? "重出" : "出首帧"}</button>
        <button type="button" className="studio-button studio-button--quiet" onClick={() => fileRef.current?.click()}><Upload size={13} aria-hidden="true" />上传</button>
        {project.analysis_id ? <button type="button" className="studio-button studio-button--quiet" title="用拆解时抽到的同行画面，仅用于测试" onClick={onUseReference}><Images size={13} aria-hidden="true" />原片帧</button> : null}
        <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp" hidden aria-label={`上传镜头 ${shot.position} 首帧`} onChange={(event) => { const file = event.target.files?.[0]; if (file) onUpload(file); event.target.value = ""; }} />
      </div>
    </div>
    <div className="rp-shot__main">
      <div className="rp-shot__head">
        <input className="rp-shot__purpose" aria-label={`镜头 ${shot.position} 作用`} defaultValue={shot.purpose} placeholder="这个镜头的作用" onBlur={(event) => { if (event.target.value.trim() !== shot.purpose) onPatch({ purpose: event.target.value.trim() }); }} />
        <label className="rp-shot__duration">时长
          <select aria-label={`镜头 ${shot.position} 时长`} value={shot.duration_s} onChange={(event) => onPatch({ duration_s: Number(event.target.value) })}>
            {Array.from(new Set([...durations, shot.duration_s])).sort((a, b) => a - b).map((value) => <option key={value} value={value}>{value} 秒</option>)}
          </select>
        </label>
        <button type="button" className="studio-button studio-button--quiet" aria-expanded={open} onClick={() => setOpen((value) => !value)}><ChevronDown size={14} aria-hidden="true" className={open ? "rp-rotate" : undefined} />提示词</button>
        <button type="button" className="studio-button studio-button--quiet studio-button--danger" aria-label={`删除镜头 ${shot.position}`} onClick={onDelete}><Trash2 size={14} aria-hidden="true" /></button>
      </div>
      {open ? <div className="rp-shot__prompts">
        <PromptField label="首帧图提示词" hint="给图片模型，只写静态画面" value={shot.first_frame_prompt} onSave={(value) => onPatch({ first_frame_prompt: value })} />
        <PromptField label="运动提示词" hint="给视频模型，只写怎么动" value={shot.motion_prompt} onSave={(value) => onPatch({ motion_prompt: value })} />
        <div className="rp-shot__pair">
          <PromptField label="避免" rows={2} value={shot.negative} onSave={(value) => onPatch({ negative: value })} />
          <PromptField label="商品保持" rows={2} value={shot.keep} onSave={(value) => onPatch({ keep: value })} />
        </div>
      </div> : <p className="rp-shot__motion" title={shot.motion_prompt}>{shot.motion_prompt || "还没有运动提示词，点「提示词」填写"}</p>}
      <div className="rp-takes">
        {shot.takes.map((take) => <TakeTile key={take.id} take={take} ratio={project.ratio} selected={shot.selected_take_id === take.id} busy={busyKey === `take:${take.id}`}
          onSelect={() => onTake("select", take)} onCancel={() => onTake("cancel", take)} onDelete={() => onTake("delete", take)} />)}
        <div className="rp-takes__add">
          <label>出
            <select aria-label={`镜头 ${shot.position} 出几版`} value={variants} onChange={(event) => setVariants(Number(event.target.value))}>{[1, 2, 3, 4].map((value) => <option key={value} value={value}>{value}</option>)}</select>
            版
          </label>
          <button type="button" className="studio-button" disabled={!frameReady || busyKey === `gen:${shot.id}`} title={frameReady ? "" : "先出首帧图或上传图片"} onClick={() => onGenerate(variants)}>
            {running ? <RefreshCw size={13} aria-hidden="true" /> : <Film size={13} aria-hidden="true" />}{shot.takes.length ? "再生成" : "生成视频"}
          </button>
        </div>
      </div>
    </div>
  </li>;
}

/* ----------------------------- 项目工作台 ----------------------------- */

function StepPill({ index, label, done, active }: { index: number; label: string; done: boolean; active: boolean }) {
  return <li data-done={done ? "true" : undefined} data-active={active ? "true" : undefined}><span>{done ? <Check size={13} aria-hidden="true" /> : String(index).padStart(2, "0")}</span><strong>{label}</strong></li>;
}

function ProjectWorkspace({ api, projectID, onBack, onMessage }: { api: API; projectID: string; onBack: () => void; onMessage: (message: Message) => void }) {
  const [project, setProject] = useState<ReplicateProject | null>(null);
  const [providers, setProviders] = useState<ReplicateProvidersResponse | null>(null);
  const [imageModels, setImageModels] = useState<ImageModelOption[]>([]);
  const textModels = useModelOptions();
  const [qc, setQC] = useState<VideoQCCheck | null>(null);
  const [qcModel, setQCModel] = useState("");
  const [busyKey, setBusyKey] = useState("");
  const [loadError, setLoadError] = useState("");

  const load = useCallback(async () => {
    try {
      const response = await api(`/api/replicate/${projectID}`);
      if (!response.ok) throw new Error(await readError(response, "复刻项目读取失败。"));
      setProject(await response.json() as ReplicateProject);
      setLoadError("");
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : "复刻项目读取失败。");
    }
  }, [api, projectID]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    void (async () => {
      try { const response = await api("/api/replicate/providers"); if (response.ok) setProviders(await response.json() as ReplicateProvidersResponse); } catch { /* optional */ }
      const models = await loadImageModels(api);
      if (models) setImageModels(models.models.filter((model) => model.available));
    })();
  }, [api]);

  const loadQC = useCallback(async (id: string) => {
    if (!id) { setQC(null); return; }
    try { const response = await api(`/api/video-qc/${id}`); if (response.ok) setQC(await response.json() as VideoQCCheck); } catch { /* keep */ }
  }, [api]);
  useEffect(() => { if (project?.qc_id) void loadQC(project.qc_id); }, [project?.qc_id, loadQC]);

  const shots = useMemo(() => project?.shots ?? [], [project]);
  const anyActive = shots.some((shot) => shot.first_frame_status === "generating" || shot.takes.some(takeActive)) || project?.status === "stitching";
  const qcActive = qc?.status === "queued" || qc?.status === "running";
  useEffect(() => {
    if (!anyActive && !qcActive) return;
    const timer = window.setInterval(() => { if (anyActive) void load(); if (qcActive && project?.qc_id) void loadQC(project.qc_id); }, 4000);
    return () => window.clearInterval(timer);
  }, [anyActive, qcActive, load, loadQC, project?.qc_id]);

  const act = async (key: string, work: () => Promise<Response>, fallback: string, after?: (response: Response) => Promise<void>) => {
    setBusyKey(key);
    try {
      const response = await work();
      if (!response.ok) throw new Error(await readError(response, fallback));
      if (after) await after(response);
      else if (response.status !== 204) {
        const body = await response.json() as ReplicateProject | ReplicateGenerateResponse;
        setProject("project" in body ? body.project : body);
      }
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : fallback });
    } finally {
      setBusyKey("");
    }
  };

  const patchProject = (fields: Partial<ReplicateProject>) => act("project", () => api(`/api/replicate/${projectID}`, json("PATCH", fields)), "保存失败。");
  const patchShot = (shot: ReplicateShot, fields: Partial<ReplicateShot>) => act(`shot:${shot.id}`, () => api(`/api/replicate/shots/${shot.id}`, json("PATCH", fields)), "保存失败。");
  const firstFrames = (ids: string[]) => act(ids.length === 1 ? `frame:${ids[0]}` : "frames", () => api(`/api/replicate/${projectID}/first-frames`, json("POST", { shot_ids: ids })), "出首帧失败。");
  const upload = (shot: ReplicateShot, file: File) => {
    const form = new FormData();
    form.append("file", file);
    return act(`frame:${shot.id}`, () => api(`/api/replicate/shots/${shot.id}/first-frame`, { method: "POST", body: form }), "上传失败。");
  };
  const useReference = async (shot: ReplicateShot) => {
    const raw = window.prompt("用拆解时的第几张关键帧？（仅用于测试，正式发布请换成自己的画面）", "1");
    const index = Number(raw);
    if (!raw || !Number.isInteger(index) || index < 1) return;
    await act(`frame:${shot.id}`, () => api(`/api/replicate/shots/${shot.id}/first-frame/reference`, json("POST", { frame_index: index })), "设置失败。");
  };
  const generate = (ids: string[], variants: number) => act(ids.length === 1 ? `gen:${ids[0]}` : "generate", () => api(`/api/replicate/${projectID}/generate`, json("POST", { shot_ids: ids, variants })), "提交生成失败。", async (response) => {
    const body = await response.json() as ReplicateGenerateResponse;
    setProject(body.project);
    const submitted = body.project.progress.takes_running;
    onMessage(body.skipped.length
      ? { tone: "info", text: `已同时提交，${body.skipped.length} 个镜头没有首帧被跳过。` }
      : { tone: "info", text: `已同时提交 ${submitted || "全部"} 个视频任务，H3 每条约 3 分钟，完成一个显示一个。` });
  });
  const takeAction = (action: "select" | "cancel" | "delete", take: ReplicateTake) => {
    if (action === "delete" && !window.confirm("删除这个视频版本？")) return;
    const request = action === "delete" ? api(`/api/replicate/takes/${take.id}`, { method: "DELETE" }) : api(`/api/replicate/takes/${take.id}/${action}`, { method: "POST" });
    void act(`take:${take.id}`, () => request, "操作失败。", async (response) => {
      if (response.status === 204) { await load(); return; }
      const body = await response.json() as ReplicateProject | Record<string, unknown>;
      if ("shots" in body) setProject(body as ReplicateProject); else await load();
    });
  };
  const addShot = () => act("add", () => api(`/api/replicate/${projectID}/shots`, json("POST", { purpose: "", duration_s: providers?.providers.find((item) => item.id === project?.video_provider)?.default_duration ?? 6 })), "添加镜头失败。");
  const deleteShot = (shot: ReplicateShot) => {
    if (!window.confirm(`删除镜头 ${shot.position}？它的首帧和视频版本都会删掉。`)) return;
    void act(`shot:${shot.id}`, () => api(`/api/replicate/shots/${shot.id}`, { method: "DELETE" }), "删除失败。", load);
  };
  const stitch = () => act("stitch", () => api(`/api/replicate/${projectID}/stitch`, { method: "POST" }), "拼接失败。", async (response) => {
    if (response.status !== 204) { const body = await response.json() as ReplicateProject; if ("shots" in body) setProject(body); else await load(); }
    onMessage({ tone: "info", text: "正在拼接：统一分辨率后按顺序合成，一般十几秒。" });
  });
  const runQC = () => act("qc", () => api(`/api/replicate/${projectID}/qc`, json("POST", { model: qcModel })), "质检提交失败。", async (response) => {
    const check = await response.json() as VideoQCCheck;
    setQC(check);
    await load();
    onMessage({ tone: "info", text: "AI 质检中：抽帧检查手指、肢体、商品、文字，一般 1 分钟左右。" });
  });

  if (loadError && !project) return <div className="studio-notice" data-tone="error" role="alert">{loadError} <button type="button" className="studio-button studio-button--quiet" onClick={() => void load()}>重试</button></div>;
  if (!project) return <p className="studio-empty" role="status">正在读取复刻项目…</p>;

  const provider = providers?.providers.find((item) => item.id === project.video_provider);
  const framesReady = shots.filter((shot) => shot.first_frame_status === "ready").length;
  const selectedCount = shots.filter((shot) => shot.selected_take_id).length;
  const missingFrames = shots.filter((shot) => shot.first_frame_status !== "ready" && shot.first_frame_status !== "generating").map((shot) => shot.id);
  const readyToGenerate = shots.filter((shot) => shot.first_frame_status === "ready").map((shot) => shot.id);
  const totalTakes = readyToGenerate.length * (project.variants || 1);
  const canStitch = shots.length > 0 && selectedCount === shots.length && project.status !== "stitching";
  const stepIndex = project.status === "stitched" ? 4 : selectedCount === shots.length && shots.length ? 3 : framesReady ? 2 : 1;

  return <div className="rp-work">
    <div className="rp-work__top">
      <button type="button" className="studio-button studio-button--quiet" onClick={onBack}><ArrowLeft size={15} aria-hidden="true" />全部复刻项目</button>
      <ol className="rp-steps" aria-label="复刻进度">
        <StepPill index={1} label="分镜" done={shots.length > 0} active={stepIndex === 1} />
        <StepPill index={2} label="首帧图" done={shots.length > 0 && framesReady === shots.length} active={stepIndex === 2} />
        <StepPill index={3} label="生成视频" done={shots.length > 0 && selectedCount === shots.length} active={stepIndex === 3} />
        <StepPill index={4} label="拼接与质检" done={project.status === "stitched" && qc?.status === "completed"} active={stepIndex === 4} />
      </ol>
    </div>

    <section className="studio-panel rp-settings" aria-label="复刻设置">
      <div className="rp-settings__title">
        <input className="rp-title" aria-label="项目名称" defaultValue={project.title} onBlur={(event) => { if (event.target.value.trim() && event.target.value.trim() !== project.title) void patchProject({ title: event.target.value.trim() }); }} />
        <span className="studio-status" data-status={statusLabels[project.status].tone}>{statusLabels[project.status].label}</span>
      </div>
      <div className="rp-settings__grid">
        <label className="studio-field">视频模型
          <select aria-label="视频模型" value={project.video_provider} onChange={(event) => void patchProject({ video_provider: event.target.value as ReplicateProject["video_provider"] })}>
            {(providers?.providers ?? [{ id: "runninghub_h3", label: "MiniMax H3（RunningHub）", available: true } as ReplicateProvider, { id: "grok", label: "Grok Video", available: true } as ReplicateProvider]).map((item) => <option key={item.id} value={item.id} disabled={!item.available}>{item.label}{item.available ? "" : " · 未配置"}</option>)}
          </select>
          <small>{provider?.note}{provider?.id === "runninghub_h3" && provider.keys ? ` · ${provider.keys} 把 Key 并行` : ""}</small>
        </label>
        <label className="studio-field">首帧图模型
          <select aria-label="首帧图模型" value={project.image_model} onChange={(event) => void patchProject({ image_model: event.target.value })}>
            <option value="">默认图片模型</option>
            {imageModels.map((model) => <option key={model.id} value={model.id}>{model.label || model.id}</option>)}
          </select>
        </label>
        <label className="studio-field">画面比例
          <select aria-label="画面比例" value={project.ratio} onChange={(event) => void patchProject({ ratio: event.target.value as ReplicateProject["ratio"] })}>{ratios.map((value) => <option key={value} value={value}>{value}</option>)}</select>
        </label>
        <label className="studio-field">每个镜头出几版
          <select aria-label="每个镜头出几版" value={project.variants} onChange={(event) => void patchProject({ variants: Number(event.target.value) })}>{[1, 2, 3, 4].map((value) => <option key={value} value={value}>{value} 版</option>)}</select>
          <small>多出几版挑最好的，花费也按版数算。</small>
        </label>
      </div>
      <div className="rp-settings__grid rp-settings__grid--text">
        <PromptField label="我的产品或场景" rows={2} value={project.product} onSave={(value) => void patchProject({ product: value })} />
        <PromptField label="统一画风" rows={2} hint="每个镜头都会带上，保证前后一致" value={project.style_lock} onSave={(value) => void patchProject({ style_lock: value })} />
      </div>
    </section>

    <section className="studio-panel rp-shots-panel" aria-labelledby="rp-shots-title">
      <header>
        <div><h2 id="rp-shots-title">分镜 <span className="rp-count">{shots.length}</span></h2><p>先给每个镜头出首帧图，再一次性提交全部视频；不满意的镜头可以单独再生成。</p></div>
        <div className="studio-row">
          <button type="button" className="studio-button" disabled={!missingFrames.length || busyKey === "frames"} onClick={() => void firstFrames(missingFrames)}>
            {busyKey === "frames" ? <Loader2 size={15} className="reference-spin" aria-hidden="true" /> : <ImagePlus size={15} aria-hidden="true" />}补齐首帧（{missingFrames.length}）
          </button>
          <button type="button" className="studio-button studio-button--primary" disabled={!readyToGenerate.length || busyKey === "generate"} onClick={() => void generate(readyToGenerate, project.variants)}>
            {busyKey === "generate" ? <Loader2 size={15} className="reference-spin" aria-hidden="true" /> : <Wand2 size={15} aria-hidden="true" />}全部生成（{readyToGenerate.length} 镜头 × {project.variants} 版 = {totalTakes} 条）
          </button>
        </div>
      </header>
      {project.progress.takes_total ? <div className="rp-progress" aria-label="生成进度">
        <div className="rp-progress__bar"><span style={{ width: `${(project.progress.takes_done / project.progress.takes_total) * 100}%` }} /><span data-kind="failed" style={{ width: `${(project.progress.takes_failed / project.progress.takes_total) * 100}%` }} /></div>
        <span className="studio-metrics"><span>完成 {project.progress.takes_done}</span><span>进行中 {project.progress.takes_running}</span>{project.progress.takes_failed ? <span>失败 {project.progress.takes_failed}</span> : null}<span>共 {project.progress.takes_total}</span></span>
      </div> : null}
      {shots.length === 0 ? <div className="studio-empty"><ScanSearch size={26} strokeWidth={1.5} aria-hidden="true" /><strong>还没有镜头</strong><span>点下面「加一个镜头」手动写，或者从一份拆解新建项目。</span></div> : null}
      <ol className="rp-shots">{shots.map((shot) => <ShotRow key={shot.id} project={project} shot={shot} provider={provider} busyKey={busyKey}
        onPatch={(fields) => void patchShot(shot, fields)} onFirstFrame={() => void firstFrames([shot.id])} onUpload={(file) => void upload(shot, file)}
        onUseReference={() => void useReference(shot)} onGenerate={(variants) => void generate([shot.id], variants)} onTake={takeAction} onDelete={() => deleteShot(shot)} />)}</ol>
      <button type="button" className="studio-button studio-button--quiet rp-add" disabled={busyKey === "add"} onClick={() => void addShot()}><Plus size={15} aria-hidden="true" />加一个镜头</button>
    </section>

    <section className="studio-panel rp-final" aria-labelledby="rp-final-title">
      <header>
        <div><h2 id="rp-final-title">成片与质检</h2><p>每个镜头选一版，按顺序拼成一条无声成片，再交给 AI 抽帧质检。</p></div>
        <div className="studio-row">
          <button type="button" className="studio-button" disabled={!canStitch || busyKey === "stitch"} title={canStitch ? "" : "每个镜头都要先选一版"} onClick={() => void stitch()}>
            {project.status === "stitching" ? <Loader2 size={15} className="reference-spin" aria-hidden="true" /> : <Film size={15} aria-hidden="true" />}{project.status === "stitched" ? "重新拼接" : `拼接成片（已选 ${selectedCount}/${shots.length}）`}
          </button>
          <select aria-label="质检模型" className="rp-qc-model" value={qcModel} onChange={(event) => setQCModel(event.target.value)}>
            <option value="">默认看图模型</option>
            {textModels.map((option) => <option key={option} value={option}>{option}</option>)}
          </select>
          <button type="button" className="studio-button studio-button--primary" disabled={project.status !== "stitched" || busyKey === "qc" || qcActive} onClick={() => void runQC()}>
            {qcActive ? <Loader2 size={15} className="reference-spin" aria-hidden="true" /> : <ShieldCheck size={15} aria-hidden="true" />}{qcActive ? "质检中…" : qc ? "重新质检" : "AI 质检"}
          </button>
        </div>
      </header>
      {project.status === "stitched" && project.final_url ? <div className="rp-final__body">
        {qc?.status === "completed" ? <VideoQCResult check={qc} videoURL={project.final_url} /> : <div className="rp-final__video">
          <video src={project.final_url} controls playsInline preload="metadata" aria-label="复刻成片" style={{ aspectRatio: aspect(project.ratio) }} />
          <div className="rp-final__side">
            <span className="studio-metrics"><span>{project.final_duration_s.toFixed(1)} 秒</span><span>{project.ratio}</span><span>无声</span></span>
            <a className="studio-button" href={`${project.final_url}${project.final_url.includes("?") ? "&" : "?"}download=1`} download><Download size={15} aria-hidden="true" />下载成片</a>
            {qcActive ? <p className="studio-notice" role="status"><Loader2 size={14} className="reference-spin" aria-hidden="true" /> 正在质检…</p> : null}
            {qc?.status === "failed" ? <p className="studio-notice" data-tone="error" role="alert">{qc.error || "质检失败。"}</p> : null}
          </div>
        </div>}
      </div> : <p className="studio-metrics">{project.status === "stitching" ? "正在拼接…" : "还没有成片。"}</p>}
    </section>
  </div>;
}

/* ----------------------------- 页面 ----------------------------- */

export function ReplicatePage({ api, projectID, onNavigate }: { api: API; projectID?: string; onNavigate: (href: string) => void }) {
  const [message, setMessage] = useState<Message | null>(null);
  const [tab, setTab] = useState<"projects" | "qc">(() => (typeof window !== "undefined" && window.location.hash === "#qc" ? "qc" : "projects"));
  const presetAnalysisID = useMemo(() => {
    if (typeof window === "undefined") return undefined;
    return new URLSearchParams(window.location.search).get("analysis") || undefined;
  }, []);
  const notify = useCallback((next: Message) => setMessage(next), []);
  useEffect(() => {
    if (!message || message.tone === "error") return;
    const timer = window.setTimeout(() => setMessage(null), 6000);
    return () => window.clearTimeout(timer);
  }, [message]);
  const switchTab = (next: "projects" | "qc") => {
    setTab(next);
    try { window.history.replaceState(null, "", next === "qc" ? "/replicate#qc" : "/replicate"); } catch { /* history unavailable */ }
  };
  let body: ReactNode;
  if (projectID) body = <ProjectWorkspace api={api} projectID={projectID} onBack={() => onNavigate("/replicate")} onMessage={notify} />;
  else if (tab === "qc") body = <VideoQCPanel api={api} onMessage={notify} />;
  else body = <ProjectList api={api} presetAnalysisID={presetAnalysisID} onOpen={(id) => onNavigate(`/replicate/${id}`)} onMessage={notify} />;

  return <main className="studio-page replicate-page" id="studio-content" tabIndex={-1}>
    <header className="studio-page__head">
      <div>
        <p className="studio-kicker">创作工作室 / AI 复刻</p>
        <h1>AI 复刻</h1>
        <p>选一条同行爆款的拆解，按镜头出首帧、批量生成视频、挑版本拼成片，最后让 AI 抽帧质检。</p>
      </div>
      {projectID ? null : <div className="studio-tabs" role="tablist" aria-label="AI 复刻">
        <button type="button" role="tab" aria-selected={tab === "projects"} onClick={() => switchTab("projects")}><Clapperboard size={14} aria-hidden="true" />复刻项目</button>
        <button type="button" role="tab" aria-selected={tab === "qc"} onClick={() => switchTab("qc")}><ShieldCheck size={14} aria-hidden="true" />视频质检</button>
      </div>}
    </header>
    {message ? <div className="studio-notice" data-tone={message.tone === "info" ? undefined : message.tone} role={message.tone === "error" ? "alert" : "status"}>{message.text}<button type="button" className="studio-button studio-button--quiet" aria-label="关闭提示" onClick={() => setMessage(null)}><X size={14} aria-hidden="true" /></button></div> : null}
    {body}
  </main>;
}

