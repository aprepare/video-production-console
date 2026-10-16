import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, CheckCircle2, Loader2, ShieldCheck, Trash2, Upload, XCircle } from "lucide-react";
import { useModelOptions } from "../ModelSelect";
import type { VideoQCCheck, VideoQCIssue } from "../references/videoQCTypes";
import "./video-qc.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;
type Message = { tone: "ok" | "error" | "info"; text: string };

export const categoryLabels: Record<string, string> = {
  hands: "手部", body: "肢体", face: "面部", product: "商品", text: "文字", artifact: "穿帮跳变", watermark: "水印", quality: "清晰度",
};
const severityLabels: Record<VideoQCIssue["severity"], string> = { high: "严重", medium: "一般", low: "轻微" };
export const verdictLabels: Record<string, { label: string; tone: string }> = {
  pass: { label: "合格", tone: "ready" }, warn: { label: "有瑕疵", tone: "working" }, fail: { label: "不合格", tone: "attention" },
};

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json() as { message?: string };
    return body.message?.trim() || fallback;
  } catch {
    return fallback;
  }
}

function clock(seconds: number) {
  const total = Math.max(0, seconds);
  const minutes = Math.floor(total / 60);
  return `${String(minutes).padStart(2, "0")}:${(total % 60).toFixed(1).padStart(4, "0")}`;
}

/** 质检结果：总评、问题清单（点问题跳到那一秒）、抽查的帧。 */
export function VideoQCResult({ check, videoURL, onSeekShot }: { check: VideoQCCheck; videoURL?: string; onSeekShot?: (seconds: number) => void }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const issues = check.report?.issues ?? [];
  const verdict = verdictLabels[check.verdict] ?? { label: "未评定", tone: "draft" };
  const seek = (seconds: number) => {
    if (videoRef.current) { videoRef.current.currentTime = seconds; void videoRef.current.play().catch(() => undefined); }
    onSeekShot?.(seconds);
  };
  const issueFrames = new Set(issues.map((issue) => issue.frame_index));
  return <div className="qc-result">
    <div className="qc-result__head" data-verdict={check.verdict}>
      <span className="qc-result__icon" aria-hidden="true">
        {check.verdict === "pass" ? <CheckCircle2 size={22} /> : check.verdict === "fail" ? <XCircle size={22} /> : <AlertTriangle size={22} />}
      </span>
      <div>
        <strong>{verdict.label}<span className="qc-result__score">{check.score} 分</span></strong>
        <p>{check.report?.summary || (issues.length ? `发现 ${issues.length} 个问题` : "没有发现问题")}</p>
      </div>
      <span className="studio-metrics"><span>{check.frame_count} 帧</span><span>{check.duration_s.toFixed(1)} 秒</span>{check.model ? <span>{check.model}</span> : null}</span>
    </div>
    <div className="qc-result__body">
      {videoURL ? <video ref={videoRef} className="qc-result__video" src={videoURL} controls playsInline preload="metadata" aria-label="被质检的视频" /> : null}
      <div className="qc-result__issues">
        {issues.length ? <ol aria-label="问题清单">{issues.map((issue, index) => {
          const frame = check.frames?.find((item) => item.index === issue.frame_index);
          return <li key={index} data-severity={issue.severity}>
            <button type="button" onClick={() => seek(issue.time_s)} aria-label={`跳到 ${clock(issue.time_s)}：${issue.description}`}>
              {frame ? <img src={frame.url} alt="" loading="lazy" /> : <span className="qc-result__noframe" />}
              <span className="qc-result__issue">
                <span className="qc-result__tags"><span className="qc-sev" data-severity={issue.severity}>{severityLabels[issue.severity]}</span><span>{categoryLabels[issue.category] ?? issue.category}</span><time>{clock(issue.time_s)}</time></span>
                <strong>{issue.description}</strong>
                {issue.suggestion ? <small>{issue.suggestion}</small> : null}
              </span>
            </button>
          </li>;
        })}</ol> : <div className="qc-result__clean"><ShieldCheck size={22} aria-hidden="true" /><span>抽查的 {check.frame_count} 帧里没有发现手部、肢体、商品、文字等问题。</span></div>}
        {check.report?.checked?.length ? <p className="studio-metrics qc-result__checked">检查项：{check.report.checked.map((item) => categoryLabels[item] ?? item).join("、")}</p> : null}
      </div>
    </div>
    {check.frames?.length ? <details className="qc-result__frames">
      <summary>查看抽查的 {check.frames.length} 帧</summary>
      <ol>{check.frames.map((frame) => <li key={frame.index} data-flagged={issueFrames.has(frame.index) ? "true" : undefined}>
        <button type="button" onClick={() => seek(frame.time_s)} aria-label={`跳到 ${clock(frame.time_s)}`}><img src={frame.url} alt="" loading="lazy" /><span>{clock(frame.time_s)}</span></button>
      </li>)}</ol>
    </details> : null}
  </div>;
}

/** 视频质检：上传一条视频，AI 抽帧检查手指、肢体、商品变形、文字乱码等问题。 */
export function VideoQCPanel({ api, onMessage }: { api: API; onMessage: (message: Message) => void }) {
  const models = useModelOptions();
  const [checks, setChecks] = useState<VideoQCCheck[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<VideoQCCheck | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [product, setProduct] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState<"" | "upload" | string>("");
  const [dragging, setDragging] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const loadList = useCallback(async () => {
    try {
      const response = await api("/api/video-qc?limit=30");
      if (!response.ok) return;
      const items = await response.json() as VideoQCCheck[];
      setChecks(items);
      setSelectedID((current) => current && items.some((item) => item.id === current) ? current : items[0]?.id ?? "");
    } catch { /* advisory */ }
  }, [api]);
  useEffect(() => { void loadList(); }, [loadList]);

  const loadDetail = useCallback(async (id: string) => {
    if (!id) { setDetail(null); return; }
    try {
      const response = await api(`/api/video-qc/${id}`);
      if (response.ok) setDetail(await response.json() as VideoQCCheck);
    } catch { /* keep previous */ }
  }, [api]);
  useEffect(() => { void loadDetail(selectedID); }, [selectedID, loadDetail]);

  const pending = checks.some((item) => item.status === "queued" || item.status === "running");
  useEffect(() => {
    if (!pending) return;
    const timer = window.setInterval(() => { void loadList(); if (selectedID) void loadDetail(selectedID); }, 3000);
    return () => window.clearInterval(timer);
  }, [pending, loadList, loadDetail, selectedID]);

  const submit = async () => {
    if (!file) return;
    setBusy("upload");
    try {
      const form = new FormData();
      form.append("file", file);
      if (product.trim()) form.append("product", product.trim());
      if (model) form.append("model", model);
      const response = await api("/api/video-qc", { method: "POST", body: form });
      if (!response.ok) throw new Error(await readError(response, "上传失败。"));
      const created = await response.json() as VideoQCCheck;
      setChecks((current) => [created, ...current.filter((item) => item.id !== created.id)]);
      setSelectedID(created.id);
      setFile(null);
      onMessage({ tone: "info", text: "已开始质检：抽帧后交给看图模型逐项检查，一般 30 秒到 2 分钟。" });
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "上传失败。" });
    } finally {
      setBusy("");
    }
  };
  const remove = async (id: string) => {
    if (!window.confirm("删除这条质检记录？")) return;
    setBusy(id);
    try {
      const response = await api(`/api/video-qc/${id}`, { method: "DELETE" });
      if (!response.ok && response.status !== 404) throw new Error(await readError(response, "删除失败。"));
      setChecks((current) => current.filter((item) => item.id !== id));
      if (selectedID === id) setSelectedID("");
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "删除失败。" });
    } finally {
      setBusy("");
    }
  };
  const pick = (picked: File | undefined | null) => {
    if (!picked) return;
    if (!/\.(mp4|mov|webm|mkv)$/i.test(picked.name)) { onMessage({ tone: "error", text: "只支持 MP4、MOV、WebM、MKV 视频。" }); return; }
    if (picked.size > 300 * 1024 * 1024) { onMessage({ tone: "error", text: "视频超过 300MB。" }); return; }
    setFile(picked);
  };

  return <div className="qc-panel">
    <section className="qc-upload">
      <div
        className="qc-drop" data-dragging={dragging ? "true" : undefined} data-has-file={file ? "true" : undefined}
        onDragOver={(event) => { event.preventDefault(); setDragging(true); }}
        onDragLeave={() => setDragging(false)}
        onDrop={(event) => { event.preventDefault(); setDragging(false); pick(event.dataTransfer.files?.[0]); }}
      >
        <Upload size={22} aria-hidden="true" />
        <strong>{file ? file.name : "把要检查的视频拖到这里"}</strong>
        <span>{file ? `${(file.size / 1024 / 1024).toFixed(1)} MB` : "AI 生成的成片或片段都可以，最大 300MB"}</span>
        <button type="button" className="studio-button" onClick={() => inputRef.current?.click()}>{file ? "换一个" : "选择视频"}</button>
        <input ref={inputRef} type="file" accept="video/mp4,video/quicktime,video/webm,video/x-matroska,.mkv" hidden aria-label="选择要质检的视频" onChange={(event) => { pick(event.target.files?.[0]); event.target.value = ""; }} />
      </div>
      <div className="qc-upload__options">
        <label className="studio-field">质检模型
          <select aria-label="质检模型" value={model} onChange={(event) => setModel(event.target.value)}>
            <option value="">默认看图模型</option>
            {models.map((option) => <option key={option} value={option}>{option}</option>)}
          </select>
        </label>
        <label className="studio-field">商品描述（可选）
          <input aria-label="商品描述" maxLength={300} value={product} onChange={(event) => setProduct(event.target.value)} placeholder="例如：一只红色梭子蟹，八条腿两只大钳" />
          <small>填了会按它核对商品有没有变形、前后是否一致。</small>
        </label>
        <button type="button" className="studio-button studio-button--primary" disabled={!file || busy === "upload"} onClick={() => void submit()}>
          {busy === "upload" ? <Loader2 size={15} className="reference-spin" aria-hidden="true" /> : <ShieldCheck size={15} aria-hidden="true" />}
          {busy === "upload" ? "正在上传…" : "开始质检"}
        </button>
      </div>
    </section>

    <div className="qc-layout">
      <aside className="qc-history" aria-label="质检记录">
        <h3>质检记录</h3>
        {checks.length ? <ul>{checks.map((item) => {
          const verdict = verdictLabels[item.verdict];
          const working = item.status === "queued" || item.status === "running";
          return <li key={item.id}>
            <button type="button" aria-current={item.id === selectedID ? "true" : undefined} onClick={() => setSelectedID(item.id)}>
              <strong>{item.title || "未命名视频"}</strong>
              <span className="studio-metrics">
                {working ? <span className="studio-status" data-status="working">{item.status === "queued" ? "排队中" : "检查中"}</span>
                  : item.status === "failed" ? <span className="studio-status" data-status="attention">失败</span>
                  : verdict ? <span className="studio-status" data-status={verdict.tone}>{verdict.label} · {item.score}</span> : null}
                <span>{new Date(item.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</span>
              </span>
            </button>
            <button type="button" className="studio-button studio-button--quiet studio-button--danger" aria-label={`删除质检：${item.title}`} disabled={busy === item.id} onClick={() => void remove(item.id)}><Trash2 size={14} aria-hidden="true" /></button>
          </li>;
        })}</ul> : <p className="studio-metrics">还没有质检记录。</p>}
      </aside>
      <section className="qc-detail" aria-label="质检结果">
        {!detail ? <div className="studio-empty"><ShieldCheck size={26} strokeWidth={1.5} aria-hidden="true" /><strong>上传一条视频开始质检</strong><span>检查手指、肢体、面部、商品变形、文字乱码、画面跳变、水印和清晰度，问题标到秒。</span></div> : null}
        {detail && (detail.status === "queued" || detail.status === "running") ? <div className="studio-notice" role="status"><Loader2 size={15} className="reference-spin" aria-hidden="true" /> {detail.status === "queued" ? "排队中…" : `正在检查${detail.frame_count ? ` ${detail.frame_count} 帧` : ""}…`}</div> : null}
        {detail?.status === "failed" ? <div className="studio-notice" data-tone="error" role="alert">{detail.error || "质检失败。"}</div> : null}
        {detail?.status === "completed" ? <VideoQCResult check={detail} videoURL={detail.source === "upload" || detail.source === "replicate" || detail.source === "replicate_take" ? `/api/video-qc/${detail.id}/video` : undefined} /> : null}
      </section>
    </div>
  </div>;
}
