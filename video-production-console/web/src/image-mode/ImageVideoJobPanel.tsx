import { useCallback, useEffect, useState } from "react";
import type { Account, ImageOutputMode, ImageProjectDetail, ImageVideoJobDetail } from "../types";

type API = (path: string, init?: RequestInit) => Promise<Response>;

type Props = {
  api: API;
  detail: ImageProjectDetail;
  onMessage?: (message: string) => void;
};

function errorText(value: unknown) {
  return value && typeof value === "object" && "message" in value && typeof value.message === "string" ? value.message : "请求未能完成";
}

function seconds(value?: number) {
  if (!Number.isFinite(value)) return "—";
  return `${(Number(value) / 1_000_000).toFixed(2)}秒`;
}

export function ImageVideoJobPanel({ api, detail, onMessage }: Props) {
  const [mode, setMode] = useState<ImageOutputMode>(detail.project.output_mode || "image_slideshow");
  const [accountID, setAccountID] = useState(detail.project.account_id || "");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [job, setJob] = useState<ImageVideoJobDetail | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

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
    const persisted = detail.project.image_video_job;
    const jobID = detail.project.image_video_job_id;
    if (persisted) {
      setJob(persisted);
      setMode(persisted.job.output_mode);
      setAccountID(persisted.job.account_id);
      return;
    }
    if (!jobID) return;
    void api(`/api/image-video-jobs/${jobID}`).then(async (response) => {
      if (response.ok) {
        const loaded = await response.json() as ImageVideoJobDetail;
        setJob(loaded);
        setMode(loaded.job.output_mode);
        setAccountID(loaded.job.account_id);
      }
    }).catch(() => undefined);
  }, [api, detail.project.image_video_job, detail.project.image_video_job_id]);

  const loadJob = useCallback(async () => {
    if (!job?.job.id) return;
    const response = await api(`/api/image-video-jobs/${job.job.id}`);
    if (!response.ok) return;
    const loaded = await response.json() as ImageVideoJobDetail;
    setJob(loaded);
    setMode(loaded.job.output_mode);
  }, [api, job?.job.id]);

  useEffect(() => {
    if (!job || (job.job.status !== "pending" && job.job.status !== "running")) return;
    const timer = window.setInterval(() => void loadJob(), 1500);
    return () => window.clearInterval(timer);
  }, [job, loadJob]);

  const persistMode = async (next: ImageOutputMode) => {
    if (job || next === detail.project.output_mode) {
      setMode(next);
      return;
    }
    setMode(next);
    try {
      await api(`/api/image-projects/${detail.project.id}/output-mode`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ output_mode: next }),
      });
    } catch {
      // 锁定后由作业开始接口再校验；这里只做未锁定时的同步。
    }
  };

  const start = async () => {
    if (!accountID.trim() || busy || detail.items.some((item) => item.status !== "ready")) {
      setError("请先填写发布账号，并确认全部图片已生成。");
      return;
    }
    setBusy("start");
    setError("");
    try {
      if (!detail.project.output_mode_locked_at) {
        await persistMode(mode);
      }
      const response = await api(`/api/image-projects/${detail.project.id}/image-video-jobs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ output_mode: mode, account_id: accountID.trim(), idempotency_key: crypto.randomUUID() }),
      });
      const payload = await response.json() as { job_id?: string; message?: string };
      if (!response.ok || !payload.job_id) throw new Error(payload.message || "作业创建失败");
      const next = await api(`/api/image-video-jobs/${payload.job_id}`);
      if (!next.ok) throw new Error("作业状态读取失败");
      const loaded = await next.json() as ImageVideoJobDetail;
      setJob(loaded);
      setMode(loaded.job.output_mode);
      setAccountID(loaded.job.account_id);
      onMessage?.("视频作业已创建，模式和账号已锁定。");
    } catch (value) {
      setError(errorText(value));
    } finally {
      setBusy("");
    }
  };

  const action = async (name: "retry-failed" | "cancel" | "retry-registration") => {
    if (!job || busy) return;
    setBusy(name);
    setError("");
    try {
      const response = await api(`/api/image-video-jobs/${job.job.id}/${name}`, { method: "POST" });
      const payload = await response.json() as { message?: string };
      if (!response.ok) throw new Error(payload.message || "操作失败");
      await loadJob();
    } catch (value) {
      setError(errorText(value));
    } finally {
      setBusy("");
    }
  };

  const locked = Boolean(job);
  const failed = job?.items.filter((item) => item.status === "failed") || [];
  return (
    <section className="image-video-job-panel" aria-label="图文视频作业">
      <div className="image-video-job-panel__head">
        <div><span className="image-kicker">剪映草稿输出</span><h2>图片视频 / 图生视频</h2><p>无字幕轨、无可编辑标题；图片内中文标题保持原样。</p></div>
        {job ? <span className={`image-video-job-status image-video-job-status--${job.job.status}`}>{job.job.status}</span> : null}
      </div>
      <div className="image-video-mode-cards">
        <label className={mode === "image_slideshow" ? "is-selected" : ""}><input type="radio" name="image-video-output-mode" checked={mode === "image_slideshow"} disabled={locked || Boolean(busy)} onChange={() => void persistMode("image_slideshow")} /><span><strong>图片视频</strong><small>图片加轻推拉与淡入淡出</small></span></label>
        <label className={mode === "image_to_video" ? "is-selected" : ""}><input type="radio" name="image-video-output-mode" checked={mode === "image_to_video"} disabled={locked || Boolean(busy)} onChange={() => void persistMode("image_to_video")} /><span><strong>图生视频</strong><small>grok-imagine-video-1.5 · 480p · 24fps · 并发 6</small></span></label>
      </div>
      <label>发布账号<select value={accountID} disabled={locked || Boolean(busy)} onChange={(event) => setAccountID(event.target.value)}><option value="">选择已配置的账号</option>{accounts.map((account) => <option value={account.id} key={account.id}>{account.name}</option>)}</select></label>
      {!job ? <button type="button" className="primary" disabled={Boolean(busy)} onClick={() => void start()}>{busy === "start" ? "正在创建…" : "开始生成剪映草稿"}</button> : null}
      {job ? <div className="image-video-job-progress"><p>模板 {job.job.template_version} · 阶段 {job.job.phase || "preparing"} · 草稿 {job.job.draft_status || "未开始"} · 登记 {job.job.registration_status || "未开始"}</p><p>第 {job.job.retry_round + 1} 轮 · {job.items.filter((item) => item.status === "succeeded").length}/{job.items.length} 个镜头成功</p>{job.job.status === "succeeded" ? <p>剪映草稿已登记为「图文视频-{job.job.id}」。</p> : null}{job.items.map((item) => <div className="image-video-job-item" key={item.id}><span>镜头 {String(item.ordinal).padStart(2, "0")}</span><span>{item.status} · 第{item.attempt}/{item.max_attempts}次</span><small>时间线 {seconds(item.timeline_duration_us)}{item.requested_duration_seconds ? ` · 请求 ${item.requested_duration_seconds}秒` : ""}{item.actual_duration_us ? ` · 实际 ${seconds(item.actual_duration_us)}` : ""}</small>{item.error_message ? <small>{item.error_message}</small> : null}</div>)}</div> : null}
      {failed.length ? <button type="button" disabled={Boolean(busy)} onClick={() => void action("retry-failed")}>{busy === "retry-failed" ? "正在重试…" : "只重试失败镜头"}</button> : null}
      {job && job.job.draft_status === "succeeded" && job.job.registration_status === "failed" ? <button type="button" disabled={Boolean(busy)} onClick={() => void action("retry-registration")}>{busy === "retry-registration" ? "正在登记…" : "重试剪映登记"}</button> : null}
      {job && (job.job.status === "pending" || job.job.status === "running") ? <button type="button" className="danger" disabled={Boolean(busy)} onClick={() => void action("cancel")}>{busy === "cancel" ? "正在取消…" : "取消作业"}</button> : null}
      {error ? <p className="image-mode-notice" role="alert">{error}</p> : null}
    </section>
  );
}
