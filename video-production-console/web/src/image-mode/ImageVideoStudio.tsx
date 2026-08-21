import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ArrowLeft } from "lucide-react";
import type { Account, ImageOutputMode, ImageProject, ImageProjectDetail } from "../types";
import { ImageVideoJobPanel } from "./ImageVideoJobPanel";
import "./image-mode.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;

type Props = {
  api: API;
  initialProjectID?: string;
  onProjectOpen?: (id: string) => void;
  onProjectClose?: () => void;
};

function errorText(value: unknown) {
  return value && typeof value === "object" && "message" in value && typeof value.message === "string"
    ? value.message
    : "请求未能完成";
}

function imageURL(projectID: string, itemID: string, revision: string) {
  return `/api/image-projects/${projectID}/items/${itemID}/image?v=${encodeURIComponent(revision)}`;
}

function phaseLabel(project: ImageProject) {
  if (project.run_status === "failed") return "失败";
  if (project.run_status === "interrupted") return "已中断";
  switch (project.run_phase) {
    case "planning":
      return "正在配音拆段";
    case "imaging":
      return "正在生成镜头图";
    case "completed":
      return project.status === "ready" ? "图片已齐" : "部分完成";
    default:
      return project.run_status || project.status;
  }
}

export function ImageVideoStudio({ api, initialProjectID, onProjectOpen, onProjectClose }: Props) {
  const [projects, setProjects] = useState<ImageProject[]>([]);
  const [detail, setDetail] = useState<ImageProjectDetail | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [script, setScript] = useState("");
  const [accountID, setAccountID] = useState("");
  const [outputMode, setOutputMode] = useState<ImageOutputMode>("image_slideshow");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const loadList = useCallback(async (signal?: AbortSignal) => {
    const response = await api("/api/image-videos", signal ? { signal } : undefined);
    if (!response.ok) throw new Error(errorText(await response.json().catch(() => ({}))));
    const payload = await response.json() as ImageProject[];
    setProjects(Array.isArray(payload) ? payload : []);
  }, [api]);

  const loadProject = useCallback(async (id: string, signal?: AbortSignal) => {
    const response = await api(`/api/image-videos/${id}`, signal ? { signal } : undefined);
    if (!response.ok) throw new Error(errorText(await response.json().catch(() => ({}))));
    const payload = await response.json() as ImageProjectDetail;
    setDetail(payload);
  }, [api]);

  useEffect(() => {
    let active = true;
    void api("/api/accounts").then(async (response) => {
      if (!response.ok) return;
      const payload = await response.json() as Account[];
      if (active) setAccounts(Array.isArray(payload) ? payload : []);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [api]);

  useEffect(() => {
    const controller = new AbortController();
    void loadList(controller.signal).catch((value) => {
      if (!controller.signal.aborted) setError(errorText(value));
    });
    return () => controller.abort();
  }, [loadList]);

  useEffect(() => {
    if (!initialProjectID) {
      setDetail(null);
      return;
    }
    const controller = new AbortController();
    void loadProject(initialProjectID, controller.signal).catch((value) => {
      if (!controller.signal.aborted) setError(errorText(value));
    });
    return () => controller.abort();
  }, [initialProjectID, loadProject]);

  useEffect(() => {
    if (!detail) return;
    const running = detail.project.run_status === "running" || detail.project.image_video_job?.job.status === "pending" || detail.project.image_video_job?.job.status === "running";
    if (!running) return;
    const timer = window.setInterval(() => {
      void loadProject(detail.project.id).catch(() => undefined);
    }, 1500);
    return () => window.clearInterval(timer);
  }, [detail, loadProject]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!script.trim() || !accountID.trim() || busy) return;
    setBusy("create");
    setError("");
    try {
      const response = await api("/api/image-videos", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          script: script.trim(),
          account_id: accountID.trim(),
          output_mode: outputMode,
          idempotency_key: crypto.randomUUID(),
        }),
      });
      const payload = await response.json() as { project_id?: string; message?: string };
      if (!response.ok || !payload.project_id) throw new Error(payload.message || "图文视频创建失败");
      setScript("");
      if (onProjectOpen) onProjectOpen(payload.project_id);
      else await loadProject(payload.project_id);
      await loadList();
    } catch (value) {
      setError(errorText(value));
    } finally {
      setBusy("");
    }
  };

  const resume = async () => {
    if (!detail || busy) return;
    setBusy("resume");
    setError("");
    try {
      const response = await api(`/api/image-videos/${detail.project.id}/resume`, { method: "POST" });
      const payload = await response.json() as { message?: string };
      if (!response.ok) throw new Error(payload.message || "无法继续生成");
      await loadProject(detail.project.id);
    } catch (value) {
      setError(errorText(value));
    } finally {
      setBusy("");
    }
  };

  if (detail) {
    const allReady = detail.items.length > 0 && detail.items.every((item) => item.status === "ready");
    const canResume = detail.project.run_status === "failed" || detail.project.run_status === "interrupted";
    return (
      <main className="image-mode-workbench">
        <div className="image-mode-head">
          <div>
            <button type="button" className="header-button" onClick={() => { setDetail(null); onProjectClose?.(); }}>
              <ArrowLeft size={16} aria-hidden="true" />返回列表
            </button>
            <span className="image-kicker">图文视频</span>
            <h1>{detail.project.title}</h1>
            <p>{phaseLabel(detail.project)} · {detail.project.image_count} 个镜头 · {detail.project.output_mode === "image_to_video" ? "图生视频" : "图片视频"}</p>
          </div>
        </div>
        {detail.project.phase_error ? <div className="image-mode-notice" role="alert">{detail.project.phase_error}</div> : null}
        {message ? <div className="image-mode-notice" role="status">{message}</div> : null}
        {error ? <div className="image-mode-notice" role="alert">{error}</div> : null}
        {canResume ? <button type="button" className="primary" disabled={Boolean(busy)} onClick={() => void resume()}>{busy === "resume" ? "正在继续…" : "继续生成"}</button> : null}
        {allReady ? <ImageVideoJobPanel api={api} detail={detail} onMessage={setMessage} /> : <p>图片齐了之后会自动开始剪映草稿；也可在完成后手动重试失败镜头。</p>}
        <section className="image-card-grid" aria-label="镜头画廊">
          {detail.items.map((item) => {
            const sequence = String(item.sequence).padStart(3, "0");
            const revision = item.updated_at || `${item.status}-${item.width || 0}`;
            return (
              <article className={`image-card image-card--${item.status}`} key={item.id} aria-label={`镜头 ${sequence}`}>
                <div className="image-card-number">{sequence}</div>
                {item.status === "ready" ? <img src={imageURL(detail.project.id, item.id, revision)} alt="" /> : <div className="image-placeholder">{item.status === "generating" ? "生成中" : item.status === "failed" ? "生成失败" : "等待生成"}</div>}
                <strong>{item.title}</strong>
                <small>{item.source_text}</small>
                {item.error_message ? <p className="warning">{item.error_message}</p> : null}
              </article>
            );
          })}
        </section>
      </main>
    );
  }

  return (
    <main className="image-mode-workbench">
      <div className="image-mode-head">
        <div>
          <span className="image-kicker">图文视频</span>
          <h1>口播拆段生图</h1>
          <p>粘贴完整口播。系统先配音，再按前 30 秒 4.3–4.6 秒、之后至少 6 秒切镜头，赤墨风 9:16 生图，最后出无字幕剪映草稿。不使用图文制作那十几张图。</p>
        </div>
      </div>
      {error ? <div className="image-mode-notice" role="alert">{error}</div> : null}
      <div className="image-mode-start image-mode-start--with-list">
        <form className="quick-generate-form" onSubmit={(event) => void submit(event)}>
          <section className="quick-generate-script">
            <header className="quick-generate-script__head">
              <p className="image-kicker">独立产品线</p>
              <h2>把完整口播放进来</h2>
            </header>
            <label>
              口播文案
              <textarea rows={16} value={script} onChange={(event) => setScript(event.target.value)} placeholder="粘贴已经定稿的完整口播。" />
            </label>
          </section>
          <aside className="quick-generate-rail">
            <fieldset className="image-output-mode" aria-label="视频输出模式">
              <legend>输出模式</legend>
              <label className={outputMode === "image_slideshow" ? "is-selected" : ""}>
                <input type="radio" name="studio-output-mode" checked={outputMode === "image_slideshow"} onChange={() => setOutputMode("image_slideshow")} />
                <span><strong>图片视频</strong><small>图片加轻推拉与淡入淡出</small></span>
              </label>
              <label className={outputMode === "image_to_video" ? "is-selected" : ""}>
                <input type="radio" name="studio-output-mode" checked={outputMode === "image_to_video"} onChange={() => setOutputMode("image_to_video")} />
                <span><strong>图生视频</strong><small>grok-imagine-video-1.5 · 480p · 24fps</small></span>
              </label>
            </fieldset>
            <label>
              发布账号
              <select value={accountID} onChange={(event) => setAccountID(event.target.value)}>
                <option value="">选择已配置的账号</option>
                {accounts.map((account) => <option value={account.id} key={account.id}>{account.name}</option>)}
              </select>
            </label>
            <button type="submit" className="primary" disabled={!script.trim() || !accountID.trim() || Boolean(busy)}>
              {busy === "create" ? "正在创建…" : "开始生成图文视频"}
            </button>
          </aside>
        </form>
        <section className="image-project-list" aria-labelledby="image-video-list-title">
          <h2 id="image-video-list-title">已有图文视频</h2>
          {projects.length ? projects.map((project) => (
            <button type="button" key={project.id} onClick={() => onProjectOpen ? onProjectOpen(project.id) : void loadProject(project.id)}>
              <strong>{project.title}</strong>
              <span>{project.image_count} 镜 · {phaseLabel(project)}</span>
            </button>
          )) : <p>还没有图文视频。</p>}
        </section>
      </div>
    </main>
  );
}
