import { useCallback, useEffect, useMemo, useState } from "react";
import { Clapperboard, ClipboardCopy, Copy, Download, Film, Loader2, ScanSearch, Sparkles, Trash2 } from "lucide-react";
import type { ReferenceApi, ReferencePost } from "./ReferencesPage";
import { formatClock } from "./ReferenceMedia";

type StructureBeat = { start_s: number; end_s: number; beat: string; what_happens: string; purpose: string };
type ShotPlan = { shot: string; duration_s: number; how_to_shoot: string; line: string };
export type AIPrompt = { shot_index: number; start_s: number; end_s: number; purpose: string; first_frame_prompt: string; motion_prompt: string; t2v_prompt: string; negative: string; keep: string };
export type AnalysisReport = {
  summary?: string;
  audience?: string;
  hook?: { type?: string; first_3s?: string; why_it_works?: string };
  structure?: StructureBeat[];
  visual?: { shot_types?: string; camera_moves?: string; b_roll?: string; on_screen_text?: string; pace?: string };
  audio?: { voice?: string; music?: string; sfx?: string };
  persuasion?: string[];
  conversion?: string;
  replicate?: { shot_list?: ShotPlan[]; script_skeleton?: string[]; editing_tips?: string[]; variables?: string[]; avoid?: string[]; style_lock?: string; ai_prompts?: AIPrompt[] };
  difficulty?: string;
};
export type AnalysisFrame = { index: number; time_s: number; shot: number; url: string };
export type ReferenceAnalysisRecord = {
  id: string;
  reference_id: string;
  status: "queued" | "running" | "completed" | "failed";
  model: string;
  focus: string;
  product: string;
  frame_count: number;
  error: string;
  created_at: string;
  updated_at: string;
  report?: AnalysisReport;
  frames?: AnalysisFrame[];
};

const statusLabels: Record<ReferenceAnalysisRecord["status"], { label: string; tone: string }> = {
  queued: { label: "排队中", tone: "working" },
  running: { label: "分析中", tone: "working" },
  completed: { label: "已完成", tone: "ready" },
  failed: { label: "失败", tone: "attention" },
};

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

const list = (items?: string[]) => (items ?? []).filter((item) => item && item.trim());
const clock = (seconds: number) => formatClock(Math.round((seconds || 0) * 1000));

/** Markdown export so the breakdown can travel to a doc, a teammate or a merchant. */
export function analysisMarkdown(post: ReferencePost, record: ReferenceAnalysisRecord) {
  const r = record.report ?? {};
  const lines: string[] = [`# 拆解：${post.title || post.external_id}`, "", `平台：${post.platform} · 作者：${post.author || "未知"} · 模型：${record.model || "默认"}`, ""];
  const section = (title: string, body: string | undefined) => { if (body?.trim()) lines.push(`## ${title}`, "", body.trim(), ""); };
  const bullets = (title: string, items?: string[]) => { const values = list(items); if (values.length) lines.push(`## ${title}`, "", ...values.map((item) => `- ${item}`), ""); };
  section("一句话", r.summary);
  section("目标观众", r.audience);
  if (r.hook) section("开头钩子", [r.hook.type && `类型：${r.hook.type}`, r.hook.first_3s && `前3秒：${r.hook.first_3s}`, r.hook.why_it_works && `为什么有效：${r.hook.why_it_works}`].filter(Boolean).join("\n"));
  if (r.structure?.length) lines.push("## 结构", "", ...r.structure.map((beat) => `- ${clock(beat.start_s)}–${clock(beat.end_s)} **${beat.beat}**：${beat.what_happens}（${beat.purpose}）`), "");
  if (r.visual) section("画面", [r.visual.shot_types && `景别：${r.visual.shot_types}`, r.visual.camera_moves && `运镜：${r.visual.camera_moves}`, r.visual.b_roll && `空镜/产品镜头：${r.visual.b_roll}`, r.visual.on_screen_text && `花字字幕：${r.visual.on_screen_text}`, r.visual.pace && `节奏：${r.visual.pace}`].filter(Boolean).join("\n"));
  if (r.audio) section("声音", [r.audio.voice && `人声：${r.audio.voice}`, r.audio.music && `配乐：${r.audio.music}`, r.audio.sfx && `音效：${r.audio.sfx}`].filter(Boolean).join("\n"));
  bullets("说服手法", r.persuasion);
  section("转化引导", r.conversion);
  const rep = r.replicate;
  if (rep?.shot_list?.length) lines.push("## 拍摄清单", "", "| 镜头 | 时长 | 怎么拍 | 台词/字幕 |", "| --- | --- | --- | --- |", ...rep.shot_list.map((shot) => `| ${shot.shot} | ${shot.duration_s || ""}s | ${shot.how_to_shoot} | ${shot.line} |`), "");
  bullets("口播骨架", rep?.script_skeleton);
  bullets("剪辑要点", rep?.editing_tips);
  bullets("需要替换的变量", rep?.variables);
  bullets("不要照搬", rep?.avoid);
  if (rep?.ai_prompts?.length) {
    lines.push("## AI 复刻提示词", "");
    if (rep.style_lock?.trim()) lines.push(`统一画风：${rep.style_lock.trim()}`, "");
    rep.ai_prompts.forEach((item) => {
      lines.push(`### 镜头 ${item.shot_index} · ${clock(item.start_s)}–${clock(item.end_s)}${item.purpose ? ` · ${item.purpose}` : ""}`, "");
      if (item.first_frame_prompt) lines.push(`- 首帧图：${item.first_frame_prompt}`);
      if (item.motion_prompt) lines.push(`- 运动：${item.motion_prompt}`);
      if (item.t2v_prompt) lines.push(`- 文生视频：${item.t2v_prompt}`);
      if (item.negative) lines.push(`- 避免：${item.negative}`);
      if (item.keep) lines.push(`- 商品保持：${item.keep}`);
      lines.push("");
    });
  }
  section("复刻难度", r.difficulty);
  return lines.join("\n");
}

type ModelOption = { id: string };

function PromptLine({ label, text, onCopy }: { label: string; text: string; onCopy: (text: string) => void }) {
  if (!text?.trim()) return null;
  return <div className="analysis-prompts__line">
    <span>{label}</span>
    <p>{text}</p>
    <button type="button" className="studio-button studio-button--quiet" aria-label={`复制${label}提示词`} onClick={() => onCopy(text)}><Copy size={14} aria-hidden="true" /></button>
  </div>;
}

type Props = {
  api: ReferenceApi;
  post: ReferencePost;
  onSeek: (seconds: number) => void;
  onMessage: (message: { tone: "ok" | "error" | "info"; text: string }) => void;
  onReplicate?: (analysisID: string) => void;
};

/** 拆解：切镜抽帧 + 口播时间轴交给多模态模型，产出「怎么做的、怎么模仿」。 */
export function ReferenceAnalysis({ api, post, onSeek, onMessage, onReplicate }: Props) {
  const [records, setRecords] = useState<ReferenceAnalysisRecord[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<ReferenceAnalysisRecord | null>(null);
  const [models, setModels] = useState<ModelOption[]>([]);
  const [model, setModel] = useState("");
  const [focus, setFocus] = useState("");
  const [product, setProduct] = useState("");
  const [busy, setBusy] = useState<"" | "start" | "delete">("");

  const loadList = useCallback(async () => {
    try {
      const response = await api(`/api/reference-analyses?reference_id=${encodeURIComponent(post.id)}`);
      if (!response.ok) return;
      const items = (await response.json()) as ReferenceAnalysisRecord[];
      setRecords(items);
      setSelectedID((current) => current && items.some((item) => item.id === current) ? current : items[0]?.id ?? "");
    } catch { /* the list is advisory */ }
  }, [api, post.id]);

  useEffect(() => { void loadList(); }, [loadList]);
  useEffect(() => {
    void (async () => {
      try {
        const response = await api("/api/setup/models");
        if (!response.ok) return;
        const body = await response.json();
        setModels(Array.isArray(body?.models) ? body.models : []);
      } catch { /* fall back to the configured default */ }
    })();
  }, [api]);

  const loadDetail = useCallback(async (id: string) => {
    if (!id) { setDetail(null); return; }
    try {
      const response = await api(`/api/reference-analyses/${id}`);
      if (response.ok) setDetail(await response.json());
    } catch { /* keep the previous view */ }
  }, [api]);
  useEffect(() => { void loadDetail(selectedID); }, [selectedID, loadDetail]);

  const pending = records.some((record) => record.status === "queued" || record.status === "running");
  useEffect(() => {
    if (!pending) return;
    const timer = window.setInterval(() => { void loadList(); if (selectedID) void loadDetail(selectedID); }, 4000);
    return () => window.clearInterval(timer);
  }, [pending, loadList, loadDetail, selectedID]);

  const start = async () => {
    setBusy("start");
    try {
      const response = await api("/api/reference-analyses", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reference_id: post.id, ...(model ? { model } : {}), ...(focus.trim() ? { focus: focus.trim() } : {}), ...(product.trim() ? { product: product.trim() } : {}) }),
      });
      if (!response.ok) throw new Error(await readError(response, "拆解提交失败。"));
      const record = (await response.json()) as ReferenceAnalysisRecord;
      setRecords((current) => [record, ...current.filter((item) => item.id !== record.id)]);
      setSelectedID(record.id);
      onMessage({ tone: "info", text: "已开始拆解：先找切镜点抽关键帧，再交给模型分析，一般 1–3 分钟。" });
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "拆解提交失败。" });
    } finally {
      setBusy("");
    }
  };

  const remove = async (id: string) => {
    if (!window.confirm("删除这份拆解报告？")) return;
    setBusy("delete");
    try {
      const response = await api(`/api/reference-analyses/${id}`, { method: "DELETE" });
      if (!response.ok) throw new Error(await readError(response, "删除失败。"));
      setRecords((current) => current.filter((item) => item.id !== id));
      setSelectedID("");
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "删除失败。" });
    } finally {
      setBusy("");
    }
  };

  const markdown = useMemo(() => detail?.status === "completed" ? analysisMarkdown(post, detail) : "", [detail, post]);
  const copyText = async (text: string) => {
    try { await navigator.clipboard.writeText(text); onMessage({ tone: "ok", text: "提示词已复制。" }); }
    catch { onMessage({ tone: "error", text: "复制失败。" }); }
  };
  const copy = async () => {
    try { await navigator.clipboard.writeText(markdown); onMessage({ tone: "ok", text: "拆解报告已复制。" }); }
    catch { onMessage({ tone: "error", text: "复制失败，请改用下载。" }); }
  };
  const download = () => {
    const blob = new Blob([markdown], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `拆解-${(post.title || post.external_id).replace(/[\\/:*?"<>|]+/g, "-").slice(0, 30)}.md`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  };

  const mediaReady = post.media_status === "ready";
  const report = detail?.status === "completed" ? detail.report ?? {} : null;

  return <div className="reference-analysis">
    <div className="reference-analysis__form">
      <p className="studio-metrics">找出切镜点、抽关键帧，连同带时间的口播一起交给多模态模型：它是怎么拍、怎么剪、怎么留人的，你该怎么模仿。</p>
      <div className="studio-row">
        <select aria-label="拆解模型" value={model} onChange={(event) => setModel(event.target.value)} className="reference-analysis__model">
          <option value="">默认拆解模型</option>
          {models.map((option) => <option key={option.id} value={option.id}>{option.id}</option>)}
        </select>
        <input className="grow" aria-label="分析重点（可选）" maxLength={300} value={focus} onChange={(event) => setFocus(event.target.value)} placeholder="分析重点（可选）：例如重点看开头三秒和字幕包装" />
        <button type="button" className="studio-button studio-button--primary" disabled={!mediaReady || pending || Boolean(busy)} onClick={() => void start()}>
          <ScanSearch size={15} aria-hidden="true" />{busy === "start" ? "正在提交…" : pending ? "分析中…" : "开始拆解"}
        </button>
      </div>
      <input aria-label="我的产品或场景（可选）" maxLength={300} value={product} onChange={(event) => setProduct(event.target.value)} placeholder="我的产品或场景（可选）：例如「岚山海鲜大排档，主打现捞梭子蟹」，复刻提示词会换成你的" />
      {!mediaReady ? <p className="studio-notice" role="status">{post.has_video ? "原视频下载到本机后才能拆解，请先在「原片与口播」里下载。" : "这条内容没有视频，无法拆解画面。"}</p> : null}
    </div>

    {records.length > 1 ? <div className="studio-row reference-analysis__history" role="group" aria-label="历史拆解">
      {records.map((record) => <button key={record.id} type="button" className="studio-button studio-button--quiet" aria-pressed={record.id === selectedID} onClick={() => setSelectedID(record.id)}>
        {new Date(record.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })} · {record.model || "默认"} · {statusLabels[record.status].label}
      </button>)}
    </div> : null}

    {detail && (detail.status === "queued" || detail.status === "running") ? <div className="studio-notice" role="status"><Loader2 size={15} className="reference-spin" aria-hidden="true" /> 正在拆解（{detail.model || "默认模型"}）…完成后自动显示。</div> : null}
    {detail?.status === "failed" ? <div className="studio-notice" data-tone="error" role="alert">{detail.error || "拆解失败。"}</div> : null}

    {report && detail ? <article className="analysis-report" aria-label="拆解报告">
      <header className="studio-row">
        <span className="studio-status" data-status="ready">已完成</span>
        <span className="studio-metrics"><span>{detail.model || "默认模型"}</span><span>{detail.frame_count} 张关键帧</span></span>
        <span className="grow" />
        <button type="button" className="studio-button studio-button--quiet" onClick={() => void copy()}><ClipboardCopy size={15} aria-hidden="true" />复制</button>
        <button type="button" className="studio-button studio-button--quiet" onClick={download}><Download size={15} aria-hidden="true" />下载</button>
        <button type="button" className="studio-button studio-button--quiet studio-button--danger" aria-label="删除这份拆解" disabled={Boolean(busy)} onClick={() => void remove(detail.id)}><Trash2 size={15} aria-hidden="true" /></button>
      </header>

      {report.summary ? <p className="analysis-report__summary">{report.summary}</p> : null}
      {report.audience ? <p className="analysis-report__audience"><strong>目标观众</strong>{report.audience}</p> : null}

      {detail.frames?.length ? <ol className="analysis-frames" aria-label="关键帧">
        {detail.frames.map((frame) => <li key={frame.index}>
          <button type="button" onClick={() => onSeek(frame.time_s)} aria-label={`跳到 ${clock(frame.time_s)}`}>
            <img src={frame.url} alt="" loading="lazy" />
            <span>{clock(frame.time_s)}</span>
          </button>
        </li>)}
      </ol> : null}

      {report.hook ? <section className="analysis-card analysis-card--hook">
        <h3>开头钩子{report.hook.type ? <span className="analysis-tag">{report.hook.type}</span> : null}</h3>
        {report.hook.first_3s ? <p><strong>前 3 秒</strong>{report.hook.first_3s}</p> : null}
        {report.hook.why_it_works ? <p><strong>为什么有效</strong>{report.hook.why_it_works}</p> : null}
      </section> : null}

      {report.structure?.length ? <section className="analysis-card">
        <h3>结构与节奏</h3>
        <ol className="analysis-beats">
          {report.structure.map((beat, index) => <li key={index}>
            <button type="button" className="analysis-time" onClick={() => onSeek(beat.start_s)}>{clock(beat.start_s)}–{clock(beat.end_s)}</button>
            <div><strong>{beat.beat}</strong><p>{beat.what_happens}</p>{beat.purpose ? <small>{beat.purpose}</small> : null}</div>
          </li>)}
        </ol>
      </section> : null}

      <div className="analysis-grid">
        {report.visual ? <section className="analysis-card"><h3>画面</h3><dl>
          {report.visual.shot_types ? <><dt>景别机位</dt><dd>{report.visual.shot_types}</dd></> : null}
          {report.visual.camera_moves ? <><dt>运镜</dt><dd>{report.visual.camera_moves}</dd></> : null}
          {report.visual.b_roll ? <><dt>空镜与产品</dt><dd>{report.visual.b_roll}</dd></> : null}
          {report.visual.on_screen_text ? <><dt>花字字幕</dt><dd>{report.visual.on_screen_text}</dd></> : null}
          {report.visual.pace ? <><dt>节奏</dt><dd>{report.visual.pace}</dd></> : null}
        </dl></section> : null}
        {report.audio ? <section className="analysis-card"><h3>声音</h3><dl>
          {report.audio.voice ? <><dt>人声</dt><dd>{report.audio.voice}</dd></> : null}
          {report.audio.music ? <><dt>配乐</dt><dd>{report.audio.music}</dd></> : null}
          {report.audio.sfx ? <><dt>音效</dt><dd>{report.audio.sfx}</dd></> : null}
        </dl></section> : null}
      </div>

      {list(report.persuasion).length || report.conversion ? <section className="analysis-card">
        <h3>说服与转化</h3>
        {list(report.persuasion).length ? <ul>{list(report.persuasion).map((item, index) => <li key={index}>{item}</li>)}</ul> : null}
        {report.conversion ? <p><strong>结尾引导</strong>{report.conversion}</p> : null}
      </section> : null}

      {report.replicate ? <section className="analysis-card analysis-card--replicate">
        <h3><Film size={15} aria-hidden="true" /> 怎么模仿{report.difficulty ? <span className="analysis-tag">{report.difficulty}</span> : null}</h3>
        {report.replicate.shot_list?.length ? <div className="analysis-table-wrap"><table className="studio-table">
          <thead><tr><th>镜头</th><th className="num">时长</th><th>怎么拍</th><th>台词 / 字幕</th></tr></thead>
          <tbody>{report.replicate.shot_list.map((shot, index) => <tr key={index}><td>{shot.shot}</td><td className="num">{shot.duration_s ? `${shot.duration_s}s` : ""}</td><td>{shot.how_to_shoot}</td><td>{shot.line}</td></tr>)}</tbody>
        </table></div> : null}
        <div className="analysis-grid">
          {list(report.replicate.script_skeleton).length ? <div><h4>口播骨架</h4><ol>{list(report.replicate.script_skeleton).map((item, index) => <li key={index}>{item}</li>)}</ol></div> : null}
          {list(report.replicate.editing_tips).length ? <div><h4>剪辑要点</h4><ul>{list(report.replicate.editing_tips).map((item, index) => <li key={index}>{item}</li>)}</ul></div> : null}
          {list(report.replicate.variables).length ? <div><h4>换成你的内容</h4><ul>{list(report.replicate.variables).map((item, index) => <li key={index}>{item}</li>)}</ul></div> : null}
          {list(report.replicate.avoid).length ? <div><h4>不要照搬</h4><ul>{list(report.replicate.avoid).map((item, index) => <li key={index}>{item}</li>)}</ul></div> : null}
        </div>
      </section> : null}
      {report.replicate?.ai_prompts?.length ? <section className="analysis-card analysis-card--prompts" aria-label="AI 复刻提示词">
        <h3><Sparkles size={15} aria-hidden="true" /> AI 复刻提示词<span className="analysis-tag">{report.replicate.ai_prompts.length} 个镜头</span></h3>
        <p className="studio-metrics">每个镜头两段：先用「首帧图」出一张图，再用「运动」交给图生视频模型；「文生视频」可以直接贴到可灵、即梦、海螺。</p>
        {report.replicate.style_lock ? <p className="analysis-style-lock"><strong>统一画风</strong>{report.replicate.style_lock}</p> : null}
        <ol className="analysis-prompts">
          {report.replicate.ai_prompts.map((item) => <li key={item.shot_index}>
            <header>
              <span className="analysis-prompts__no">{String(item.shot_index).padStart(2, "0")}</span>
              <button type="button" className="analysis-time" onClick={() => onSeek(item.start_s)}>{clock(item.start_s)}–{clock(item.end_s)}</button>
              {item.purpose ? <strong>{item.purpose}</strong> : null}
            </header>
            <PromptLine label="首帧图" text={item.first_frame_prompt} onCopy={copyText} />
            <PromptLine label="运动" text={item.motion_prompt} onCopy={copyText} />
            <PromptLine label="文生视频" text={item.t2v_prompt} onCopy={copyText} />
            {item.negative ? <p className="analysis-prompts__meta"><span>避免</span>{item.negative}</p> : null}
            {item.keep ? <p className="analysis-prompts__meta"><span>商品保持</span>{item.keep}</p> : null}
          </li>)}
        </ol>
        {onReplicate ? <div className="studio-row analysis-prompts__actions">
          <span className="studio-metrics">{detail.product ? `已按「${detail.product}」替换` : "没有填写你的产品，提示词里用的是通用描述"}</span>
          <span className="grow" />
          <button type="button" className="studio-button studio-button--primary" onClick={() => onReplicate(detail.id)}><Clapperboard size={15} aria-hidden="true" />用这些提示词开始 AI 复刻</button>
        </div> : null}
      </section> : null}
    </article> : null}

    {!records.length && mediaReady ? <div className="studio-empty"><ScanSearch size={26} strokeWidth={1.5} aria-hidden="true" /><strong>还没有拆解</strong><span>选一个看图能力强的模型，点「开始拆解」。</span></div> : null}
  </div>;
}
