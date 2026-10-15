import { useEffect, useRef, useState } from "react";
import { ArrowUpRight, ImageOff, Play, X } from "lucide-react";

export type PreviewPlatform = "douyin" | "xhs" | "channels";

export function previewURL(platform: PreviewPlatform, kind: "cover" | "video", url: string) {
  return `/api/references/preview?${new URLSearchParams({ platform, kind, url })}`;
}

function clock(seconds: number) {
  const total = Math.max(0, Math.round(seconds));
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
}

/** 竖版封面缩略图：有视频时点一下就在线播放，图文显示「图文」角标。 */
export function PreviewThumb({ platform, coverURL, videoURL, durationS, title, onPlay }: {
  platform: PreviewPlatform;
  coverURL: string;
  videoURL: string;
  durationS: number;
  title: string;
  onPlay: () => void;
}) {
  const [broken, setBroken] = useState(false);
  const playable = Boolean(videoURL);
  const image = coverURL && !broken
    ? <img src={previewURL(platform, "cover", coverURL)} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} />
    : <span className="preview-thumb__blank"><ImageOff size={16} aria-hidden="true" /></span>;
  const badge = playable ? (durationS ? clock(durationS) : "视频") : "图文";
  if (!playable) {
    return <span className="preview-thumb" data-kind="note">{image}<span className="preview-thumb__badge">{badge}</span></span>;
  }
  return <button type="button" className="preview-thumb" data-kind="video" aria-label={`播放：${title || "视频"}`} onClick={(event) => { event.preventDefault(); event.stopPropagation(); onPlay(); }}>
    {image}
    <span className="preview-thumb__play" aria-hidden="true"><Play size={16} fill="currentColor" /></span>
    <span className="preview-thumb__badge">{badge}</span>
  </button>;
}

export type PreviewTarget = {
  platform: PreviewPlatform;
  title: string;
  author: string;
  url: string;
  coverURL: string;
  videoURL: string;
};

/** 在线预览：视频直接从平台 CDN 经本机转发播放，不下载、不转写。 */
export function PreviewPlayer({ target, onClose, actions }: { target: PreviewTarget; onClose: () => void; actions?: React.ReactNode }) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialogRef.current?.focus();
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); previous?.focus?.(); };
  }, [onClose]);

  return <div className="studio-dialog-backdrop preview-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="preview-dialog" role="dialog" aria-modal="true" aria-label={`预览：${target.title || "视频"}`} tabIndex={-1} ref={dialogRef}>
      <div className="preview-dialog__stage">
        {failed
          ? <div className="preview-dialog__failed" role="alert"><p>视频地址已过期或平台拒绝了播放。</p><span>重新搜索一次通常就好；也可以去原帖看。</span></div>
          : <video
            src={previewURL(target.platform, "video", target.videoURL)}
            poster={target.coverURL ? previewURL(target.platform, "cover", target.coverURL) : undefined}
            controls
            autoPlay
            playsInline
            preload="metadata"
            aria-label="在线预览视频"
            onError={() => setFailed(true)}
          />}
      </div>
      <div className="preview-dialog__meta">
        <div>
          <strong>{target.title || "（无标题）"}</strong>
          <span>{target.author || "未知作者"} · 在线预览，不占本机空间</span>
        </div>
        <div className="studio-row">
          {actions}
          {target.url ? <a className="studio-button studio-button--quiet" href={target.url} target="_blank" rel="noreferrer"><ArrowUpRight size={15} aria-hidden="true" />原帖</a> : null}
          <button type="button" className="studio-button studio-button--quiet" aria-label="关闭预览" onClick={onClose}><X size={16} aria-hidden="true" /></button>
        </div>
      </div>
    </div>
  </div>;
}
