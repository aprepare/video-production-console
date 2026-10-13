import { useEffect, useMemo, useRef, useState } from "react";
import { Download, Loader2, RotateCw, Video } from "lucide-react";
import type { ReferenceApi, ReferencePost } from "./ReferencesPage";

export type Sentence = { start_ms: number; end_ms: number; text: string };

export function formatClock(ms: number) {
  const total = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function formatBytes(bytes: number) {
  if (!bytes) return "";
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
}

type Props = {
  api: ReferenceApi;
  post: ReferencePost;
  onChange: (post: ReferencePost) => void;
  onMessage: (message: { tone: "ok" | "error" | "info"; text: string }) => void;
  /** Lets the analysis tab seek the same player to a timestamp. */
  seekRef?: React.MutableRefObject<((seconds: number) => void) | null>;
};

/** 原片：本地缓存的视频 + 按句跳转的口播时间轴。 */
export function ReferenceMedia({ api, post, onChange, onMessage, seekRef }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [current, setCurrent] = useState(0);
  const [busy, setBusy] = useState(false);
  const sentences = useMemo(() => post.sentences ?? [], [post.sentences]);
  const ready = post.media_status === "ready";

  useEffect(() => {
    if (!seekRef) return;
    seekRef.current = (seconds: number) => {
      const video = videoRef.current;
      if (!video) return;
      video.currentTime = Math.max(0, seconds);
      void video.play().catch(() => { /* autoplay may be blocked; the seek still lands */ });
    };
    return () => { seekRef.current = null; };
  }, [seekRef]);

  const seek = (ms: number) => {
    const video = videoRef.current;
    if (!video) return;
    video.currentTime = ms / 1000;
    void video.play().catch(() => { /* see above */ });
  };

  const download = async () => {
    setBusy(true);
    try {
      const response = await api(`/api/references/${post.id}/media`, { method: "POST" });
      if (!response.ok) {
        let text = "下载提交失败。";
        try { const body = await response.json(); if (body?.message) text = body.message; } catch { /* keep fallback */ }
        throw new Error(text);
      }
      onChange(await response.json());
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "下载提交失败。" });
    } finally {
      setBusy(false);
    }
  };

  const activeIndex = sentences.findIndex((sentence) => current * 1000 >= sentence.start_ms && current * 1000 < sentence.end_ms);

  if (!post.has_video) {
    return <div className="reference-media reference-media--empty">
      <Video size={22} strokeWidth={1.5} aria-hidden="true" />
      <p>{post.platform === "xhs" ? "小红书接口目前只返回图文，没有视频地址；视频笔记请点「原帖」在网页里看。" : "这条内容没有视频。"}</p>
    </div>;
  }

  return <div className="reference-media">
    {ready ? <video
      ref={videoRef}
      className="reference-media__player"
      src={`/api/references/${post.id}/media`}
      poster={post.has_cover ? `/api/references/${post.id}/cover` : undefined}
      controls
      preload="metadata"
      playsInline
      onTimeUpdate={(event) => setCurrent(event.currentTarget.currentTime)}
      aria-label="对标原视频"
    /> : <div className="reference-media__placeholder" role="status">
      {post.media_status === "downloading" ? <><Loader2 size={20} className="reference-spin" aria-hidden="true" /><span>正在把原视频下载到本机…</span></> : null}
      {post.media_status === "failed" ? <><span>{post.media_error || "原视频下载失败。"}</span><button type="button" className="studio-button" disabled={busy} onClick={() => void download()}><RotateCw size={15} aria-hidden="true" />重新下载</button></> : null}
      {post.media_status === "none" || !post.media_status ? <><span>原视频还没下载到本机。</span><button type="button" className="studio-button" disabled={busy} onClick={() => void download()}><Download size={15} aria-hidden="true" />{busy ? "正在提交…" : "下载原视频"}</button></> : null}
    </div>}

    {ready ? <div className="studio-row reference-media__bar">
      <span className="studio-metrics">{post.duration_s ? <span>{Math.round(post.duration_s)} 秒</span> : null}{post.media_size ? <span>{formatBytes(post.media_size)}</span> : null}<span>已存到本机，平台链接失效也能看</span></span>
      <span className="grow" />
      <a className="studio-button studio-button--quiet" href={`/api/references/${post.id}/media?download=1`} download><Download size={15} aria-hidden="true" />下载 MP4</a>
    </div> : null}

    {sentences.length ? <ol className="reference-timeline" aria-label="口播时间轴">
      {sentences.map((sentence, index) => <li key={`${sentence.start_ms}-${index}`}>
        <button type="button" aria-current={index === activeIndex ? "true" : undefined} disabled={!ready} onClick={() => seek(sentence.start_ms)}>
          <time>{formatClock(sentence.start_ms)}</time>
          <span>{sentence.text}</span>
        </button>
      </li>)}
    </ol> : null}
  </div>;
}
