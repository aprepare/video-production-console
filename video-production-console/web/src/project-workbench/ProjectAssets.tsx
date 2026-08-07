import {
  AudioLines,
  Captions,
  Clapperboard,
  ExternalLink,
  FileText,
  Image,
  Upload,
  Video,
} from "lucide-react";
import type { ComponentType } from "react";
import { useEffect, useRef } from "react";
import type { ProjectAsset, ProjectDetail } from "./types";

type AssetType = "continuous_script" | "narration" | "subtitle_srt" | "mix_draft" | "final_video";
export type ProjectAssetUploadType = "narration" | "subtitle_srt" | "final_video" | "account_background";
export type AssetUploadRequest = { type: ProjectAssetUploadType; token: number } | null;

const assetDefinitions: Array<{
  type: AssetType;
  label: string;
  description: string;
  accept: string;
  icon: ComponentType<{ size?: number; "aria-hidden"?: boolean }>;
  manualUpload?: boolean;
}> = [
  {
    type: "continuous_script",
    label: "连续文案",
    description: "二创生成的完整连续文本，用于配音和混剪。",
    accept: ".txt,.md,text/plain,text/markdown",
    icon: FileText,
  },
  {
    type: "narration",
    label: "配音",
    description: "与连续文案对应的最终旁白音频。",
    accept: "audio/*",
    icon: AudioLines,
    manualUpload: true,
  },
  {
    type: "subtitle_srt",
    label: "SRT 字幕",
    description: "带真实时间轴的字幕文件，用于画面同步。",
    accept: ".srt,application/x-subrip,text/plain",
    icon: Captions,
    manualUpload: true,
  },
  {
    type: "mix_draft",
    label: "混剪草稿",
    description: "已登记的可编辑剪映工程，用于最后检查。",
    accept: ".zip,application/zip",
    icon: Clapperboard,
  },
  {
    type: "final_video",
    label: "成片",
    description: "审核完成的最终视频，确认发布状态前必须存在。",
    accept: "video/*",
    icon: Video,
    manualUpload: true,
  },
];

function assetState(asset?: ProjectAsset) {
  if (!asset) return { label: "缺失", className: "missing" };
  if (asset.state === "ready") return { label: "存在", className: "ready" };
  if (asset.state === "generating") return { label: "生成中", className: "generating" };
  return { label: "失效", className: "invalid" };
}

type ProjectAssetsProps = {
  detail: ProjectDetail;
  onUpload: (type: "narration" | "subtitle_srt" | "final_video", file: File) => void;
  onReplaceBackground: (file: File) => void;
  onViewAsset: (asset: ProjectAsset) => void;
  pendingActions: string[];
  uploadRequest: AssetUploadRequest;
};

export function ProjectAssets({
  detail,
  onUpload,
  onReplaceBackground,
  onViewAsset,
  pendingActions,
  uploadRequest,
}: ProjectAssetsProps) {
  const uploadRefs = useRef<Partial<Record<ProjectAssetUploadType, HTMLInputElement | null>>>({});

  useEffect(() => {
    if (!uploadRequest) return;
    uploadRefs.current[uploadRequest.type]?.click();
  }, [uploadRequest]);

  const isPending = (_type: ProjectAssetUploadType) => pendingActions.length > 0;
  return (
    <section className="project-assets" aria-label="当前项目资产">
      <div className="workbench-section-heading">
        <div>
          <span>PROJECT ASSETS</span>
          <h2>当前项目资产</h2>
        </div>
        <p>以下文件仅属于“{detail.project.title}”</p>
      </div>

      <div className="project-assets__list">
        {assetDefinitions.map((definition) => {
          const asset = detail.assets[definition.type];
          const state = assetState(asset);
          const Icon = definition.icon;
          return (
            <article className="project-asset" key={definition.type}>
              <div className="project-asset__icon" aria-hidden="true"><Icon size={18} /></div>
              <div className="project-asset__copy">
                <div className="project-asset__title">
                  <strong>{definition.label}</strong>
                  <span className={`asset-state asset-state--${state.className}`}>{state.label}</span>
                </div>
                <p>{definition.description}</p>
                {asset ? <small>{asset.filename} · v{asset.version}</small> : <small>尚未上传到当前项目</small>}
              </div>
              <div className="project-asset__actions">
                {asset ? (
                  <button type="button" onClick={() => onViewAsset(asset)} aria-label={`查看${definition.label}`}>
                    <ExternalLink size={15} aria-hidden="true" />
                    查看
                  </button>
                ) : null}
                {definition.manualUpload ? (
                  <label className="asset-upload-control">
                    <Upload size={15} aria-hidden="true" />
                    <span>{asset ? "替换" : "上传"}</span>
                    <input
                      ref={(node) => { uploadRefs.current[definition.type as ProjectAssetUploadType] = node; }}
                      type="file"
                      accept={definition.accept}
                      aria-label={`上传${definition.label}`}
                      disabled={isPending(definition.type as ProjectAssetUploadType)}
                      onChange={(event) => {
                        if (isPending(definition.type as ProjectAssetUploadType)) {
                          event.target.value = "";
                          return;
                        }
                        const file = event.target.files?.[0];
                        if (file) onUpload(definition.type as "narration" | "subtitle_srt" | "final_video", file);
                        event.target.value = "";
                      }}
                    />
                  </label>
                ) : null}
              </div>
            </article>
          );
        })}

        <article className="project-asset project-asset--inherited">
          <div className="project-asset__icon" aria-hidden="true"><Image size={18} /></div>
          <div className="project-asset__copy">
            <div className="project-asset__title">
              <strong>账号背景图</strong>
              <span className={`asset-state asset-state--${assetState(detail.background_reference || undefined).className}`}>
                {assetState(detail.background_reference || undefined).label}
              </span>
            </div>
            <p>由当前账号提供，混剪时作为该项目的固定画面来源。</p>
            <small>{detail.background_reference?.filename || "当前账号尚未配置"}</small>
          </div>
          <div className="project-asset__actions">
            {detail.background_reference ? (
              <button type="button" onClick={() => onViewAsset(detail.background_reference!)} aria-label="查看账号背景图">
                <ExternalLink size={15} aria-hidden="true" />
                查看
              </button>
            ) : null}
            <label className="asset-upload-control">
              <Upload size={15} aria-hidden="true" />
              <span>{detail.background_reference ? "替换" : "上传"}</span>
              <input
                ref={(node) => { uploadRefs.current.account_background = node; }}
                type="file"
                accept="image/png,image/jpeg,image/webp"
                aria-label={detail.background_reference ? "替换账号背景图" : "上传账号背景图"}
                disabled={isPending("account_background")}
                onChange={(event) => {
                  if (isPending("account_background")) {
                    event.target.value = "";
                    return;
                  }
                  const file = event.target.files?.[0];
                  if (file) onReplaceBackground(file);
                  event.target.value = "";
                }}
              />
            </label>
          </div>
        </article>
      </div>
    </section>
  );
}
