import { useCallback, useEffect, useMemo, useState } from "react";
import { Clapperboard, Film, GitBranch, ImagePlus, Scissors, Sparkles } from "lucide-react";
import type { BusinessApi, Material, Order } from "./MerchantsPage";
import "./aigc.css";

export type AigcJobKind = "extract_frames" | "restyle" | "image_to_video";
export type AigcJobStatus = "queued" | "running" | "awaiting_confirm" | "completed" | "failed" | "cancelled";
export type AigcJob = {
  id: string;
  merchant_id: string;
  order_id: string;
  kind: AigcJobKind;
  status: AigcJobStatus;
  input_material_ids: string[];
  output_material_ids: string[];
  params_json?: string;
  error: string;
  created_at: string;
  updated_at: string;
};
export type LineageStep = {
  id: string;
  filename: string;
  kind: string;
  step: string;
  created_at: string;
};

const kindLabels: Record<AigcJobKind, string> = { extract_frames: "抽关键帧", restyle: "AI 重绘", image_to_video: "图生视频" };
const statusLabels: Record<AigcJobStatus, { label: string; tone: string }> = {
  queued: { label: "排队中", tone: "working" },
  running: { label: "进行中", tone: "working" },
  awaiting_confirm: { label: "待确认提交", tone: "draft" },
  completed: { label: "已完成", tone: "ready" },
  failed: { label: "失败", tone: "attention" },
  cancelled: { label: "已取消", tone: "draft" },
};
const stepLabels: Record<string, string> = { "": "原片", extract_frames: "抽帧", restyle: "重绘", image_to_video: "图生视频" };

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}
async function post<T>(api: BusinessApi, path: string, body: unknown, fallback: string): Promise<T> {
  const response = await api(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  if (!response.ok) throw new Error(await readError(response, fallback));
  return response.json() as Promise<T>;
}
function parseParams(job: AigcJob): Record<string, unknown> {
  try { return job.params_json ? JSON.parse(job.params_json) : {}; } catch { return {}; }
}

/** Derived materials carry provenance; anything else is an original upload. */
export function isDerived(material: Material & { parent_material_id?: string }) {
  return Boolean(material.parent_material_id);
}

type Props = {
  api: BusinessApi;
  merchantID: string;
  materials: Array<Material & { parent_material_id?: string }>;
  orders: Order[];
  onMaterialsChanged: () => void;
  onNotice: (notice: { tone: "ok" | "error"; text: string }) => void;
};

/** 轨二 AIGC 写实重塑：实拍 → 关键帧 → 保真重绘 → 图生视频，每步可追溯到原片。 */
export function AigcStudio({ api, merchantID, materials, orders, onMaterialsChanged, onNotice }: Props) {
  const [jobs, setJobs] = useState<AigcJob[]>([]);
  const [busy, setBusy] = useState("");
  const [video, setVideo] = useState("");
  const [frameCount, setFrameCount] = useState(6);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState("");
  const [preserve, setPreserve] = useState("");
  const [orderID, setOrderID] = useState("");
  const [unitCost, setUnitCost] = useState("");
  const [videoPrompt, setVideoPrompt] = useState("");
  const [duration, setDuration] = useState(5);
  const [aspect, setAspect] = useState<"9:16" | "16:9" | "1:1">("9:16");
  const [lineage, setLineage] = useState<{ id: string; steps: LineageStep[] } | null>(null);

  const loadJobs = useCallback(async () => {
    try {
      const response = await api(`/api/aigc/jobs?merchant_id=${merchantID}`);
      if (response.ok) setJobs(await response.json());
    } catch { /* jobs are advisory */ }
  }, [api, merchantID]);
  useEffect(() => { void loadJobs(); }, [loadJobs]);
  const active = jobs.some((job) => job.status === "running" || job.status === "queued");
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => { void loadJobs(); onMaterialsChanged(); }, 5000);
    return () => window.clearInterval(timer);
  }, [active, loadJobs, onMaterialsChanged]);

  const videos = materials.filter((material) => material.kind === "video" && !isDerived(material));
  const images = materials.filter((material) => material.kind === "image");
  const pickedImages = useMemo(() => images.filter((image) => picked.has(image.id)), [images, picked]);
  const toggle = (id: string) => setPicked((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });

  const run = async (key: string, action: () => Promise<unknown>, success: string) => {
    setBusy(key);
    try { await action(); onNotice({ tone: "ok", text: success }); await loadJobs(); onMaterialsChanged(); }
    catch (error) { onNotice({ tone: "error", text: error instanceof Error ? error.message : "操作失败。" }); }
    finally { setBusy(""); }
  };
  const extract = () => run("extract", () => post(api, "/api/aigc/extract-frames", { merchant_id: merchantID, material_id: video, count: frameCount }, "抽帧失败。"), "关键帧已生成，在下方勾选后重绘。");
  const restyle = () => run("restyle", async () => {
    await post(api, "/api/aigc/restyle", {
      merchant_id: merchantID, material_ids: [...picked], prompt: prompt.trim(), preserve: preserve.trim(),
      ...(orderID ? { order_id: orderID } : {}),
      ...(unitCost ? { unit_cost_cents: Math.round(Number.parseFloat(unitCost) * 100) || 0 } : {}),
    }, "重绘提交失败。");
    setPicked(new Set());
  }, "已提交重绘，完成后新图会出现在下方。");
  const prepare = () => run("prepare", async () => {
    await post(api, "/api/aigc/image-to-video", {
      merchant_id: merchantID, material_ids: [...picked], prompt: videoPrompt.trim(), duration_s: duration, aspect,
      ...(orderID ? { order_id: orderID } : {}),
    }, "准备失败。");
    setPicked(new Set());
  }, "已生成镜头清单，确认后才会提交到 RunningHub。");
  const confirm = (job: AigcJob) => {
    const shots = Number(parseParams(job).estimated_shots) || job.input_material_ids.length;
    if (!window.confirm(`提交 ${shots} 个镜头到 RunningHub 生成视频？这一步会产生费用，提交后不能撤回。`)) return;
    void run(`confirm-${job.id}`, () => post(api, `/api/aigc/jobs/${job.id}/confirm`, {}, "提交失败。"), "已提交生成，完成后视频会进入素材台账。");
  };
  const showLineage = async (id: string) => {
    if (lineage?.id === id) { setLineage(null); return; }
    try {
      const response = await api(`/api/aigc/lineage/${id}`);
      if (!response.ok) throw new Error(await readError(response, "来源链读取失败。"));
      setLineage({ id, steps: await response.json() });
    } catch (error) {
      onNotice({ tone: "error", text: error instanceof Error ? error.message : "来源链读取失败。" });
    }
  };

  return <div className="aigc-studio">
    <p className="aigc-intro">实拍混剪不够用时再用这条：抽关键帧后只重绘环境和光影，商品本身要保持原样。生成结果都标记为「AI 生成」并保留来源链，交付前请人工核对商品是否走样。</p>

    <section className="aigc-step" aria-labelledby="aigc-step-1">
      <h3 id="aigc-step-1"><span>1</span><Scissors size={15} aria-hidden="true" /> 从实拍视频抽关键帧</h3>
      {videos.length ? <div className="studio-row">
        <select className="grow" aria-label="选择实拍视频" value={video} onChange={(event) => setVideo(event.target.value)}>
          <option value="">选择一条商户实拍视频…</option>
          {videos.map((material) => <option key={material.id} value={material.id}>{material.filename}{material.description ? ` · ${material.description}` : ""}</option>)}
        </select>
        <label className="aigc-inline">张数<input type="number" min={1} max={12} value={frameCount} onChange={(event) => setFrameCount(Math.min(12, Math.max(1, Number.parseInt(event.target.value, 10) || 1)))} /></label>
        <button type="button" className="studio-button" disabled={!video || Boolean(busy)} onClick={() => void extract()}>{busy === "extract" ? "正在抽帧…" : "抽帧"}</button>
      </div> : <p className="studio-metrics">先在「授权素材」里上传商户实拍视频。</p>}
    </section>

    <section className="aigc-step" aria-labelledby="aigc-step-2">
      <h3 id="aigc-step-2"><span>2</span><ImagePlus size={15} aria-hidden="true" /> 选图</h3>
      {images.length ? <ul className="aigc-gallery">
        {images.map((image) => <li key={image.id}>
          <label className="aigc-tile" data-picked={picked.has(image.id) ? "true" : undefined}>
            <input type="checkbox" checked={picked.has(image.id)} onChange={() => toggle(image.id)} aria-label={`选择 ${image.filename}`} />
            <img src={`/api/materials/${image.id}/content`} alt={image.filename} loading="lazy" />
            <span className="aigc-tile__badge" data-ai={image.license_source === "ai_generated" ? "true" : undefined}>{image.license_source === "ai_generated" ? "AI 生成" : "实拍"}</span>
          </label>
          <button type="button" className="aigc-lineage-button" onClick={() => void showLineage(image.id)} aria-expanded={lineage?.id === image.id}><GitBranch size={12} aria-hidden="true" />来源</button>
          {lineage?.id === image.id ? <ol className="aigc-lineage" aria-label={`${image.filename} 的来源链`}>{lineage.steps.map((step) => <li key={step.id}><strong>{stepLabels[step.step] ?? step.step}</strong> {step.filename}</li>)}</ol> : null}
        </li>)}
      </ul> : <p className="studio-metrics">还没有图片。抽帧后会出现在这里，也可以直接上传商品图。</p>}
    </section>

    <div className="aigc-actions">
      <section className="aigc-step" aria-labelledby="aigc-step-3">
        <h3 id="aigc-step-3"><span>3</span><Sparkles size={15} aria-hidden="true" /> 保真重绘（已选 {pickedImages.length} 张）</h3>
        <label className="studio-field">想要的环境与氛围<textarea rows={3} value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="例：清晨渔港码头，薄雾，暖色逆光，木质案板上摆放，真实摄影质感" /></label>
        <label className="studio-field">必须保持不变<small>系统已固定要求商品形状、颜色、包装、文字和数量不变；这里补充具体细节。</small><input value={preserve} onChange={(event) => setPreserve(event.target.value)} placeholder="例：蟹壳上的红色绑绳、包装盒上的「岚山」字样" /></label>
        <div className="studio-row">
          <select aria-label="计入订单" value={orderID} onChange={(event) => setOrderID(event.target.value)}><option value="">不计入订单成本</option>{orders.map((order) => <option key={order.id} value={order.id}>{order.title}</option>)}</select>
          {orderID ? <label className="aigc-inline">单张成本 ¥<input inputMode="decimal" value={unitCost} onChange={(event) => setUnitCost(event.target.value)} placeholder="0.3" /></label> : null}
          <span className="grow" />
          <button type="button" className="studio-button studio-button--primary" disabled={!pickedImages.length || pickedImages.length > 8 || !prompt.trim() || Boolean(busy)} onClick={() => void restyle()}>{busy === "restyle" ? "正在提交…" : `重绘 ${pickedImages.length} 张`}</button>
        </div>
        {pickedImages.length > 8 ? <p className="studio-notice" data-tone="error" role="alert">一次最多重绘 8 张。</p> : null}
      </section>

      <section className="aigc-step" aria-labelledby="aigc-step-4">
        <h3 id="aigc-step-4"><span>4</span><Clapperboard size={15} aria-hidden="true" /> 图生视频（RunningHub H3）</h3>
        <label className="studio-field">镜头动作<textarea rows={3} value={videoPrompt} onChange={(event) => setVideoPrompt(event.target.value)} placeholder="例：镜头缓慢推近，蟹钳轻微动，热气上升；不要出现人物" /></label>
        <div className="studio-row">
          <label className="aigc-inline">时长<select value={duration} onChange={(event) => setDuration(Number(event.target.value))}>{[3, 5, 6, 8, 10].map((value) => <option key={value} value={value}>{value} 秒</option>)}</select></label>
          <label className="aigc-inline">画幅<select value={aspect} onChange={(event) => setAspect(event.target.value as typeof aspect)}><option value="9:16">竖屏 9:16</option><option value="16:9">横屏 16:9</option><option value="1:1">方形 1:1</option></select></label>
          <span className="grow" />
          <button type="button" className="studio-button" disabled={!pickedImages.length || !videoPrompt.trim() || Boolean(busy)} onClick={() => void prepare()}>{busy === "prepare" ? "正在准备…" : `准备 ${pickedImages.length} 个镜头`}</button>
        </div>
        <p className="studio-metrics">只生成镜头清单，不花钱；在下方作业里确认后才提交。</p>
      </section>
    </div>

    <section className="aigc-step" aria-labelledby="aigc-jobs">
      <h3 id="aigc-jobs"><Film size={15} aria-hidden="true" /> 作业</h3>
      {jobs.length ? <ul className="studio-list aigc-jobs">{jobs.map((job) => {
        const status = statusLabels[job.status];
        return <li key={job.id}>
          <strong>{kindLabels[job.kind]}</strong>
          <span className="studio-status" data-status={status.tone}>{status.label}</span>
          <span className="studio-metrics">输入 {job.input_material_ids.length} · 产出 {job.output_material_ids.length} · {new Date(job.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</span>
          <span className="grow" />
          {job.status === "awaiting_confirm" ? <button type="button" className="studio-button studio-button--primary" disabled={Boolean(busy)} onClick={() => confirm(job)}>{busy === `confirm-${job.id}` ? "正在提交…" : "确认提交（会产生费用）"}</button> : null}
          {job.error ? <p className="aigc-job-error">{job.error}</p> : null}
        </li>;
      })}</ul> : <p className="studio-metrics">还没有作业。</p>}
    </section>
  </div>;
}
