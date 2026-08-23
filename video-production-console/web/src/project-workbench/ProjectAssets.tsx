import {
  AudioLines,
  Captions,
  ChevronDown,
  Clapperboard,
  ExternalLink,
  FileText,
  Image,
  List,
  RotateCcw,
  Upload,
  WandSparkles,
} from "lucide-react";
import type { ComponentType } from "react";
import { useEffect, useRef } from "react";
import type { ProjectAsset, ProjectDetail, RegisteredMontageAsset } from "./types";

type AssetType = "continuous_script" | "narration" | "spoken_script" | "caption_keywords" | "subtitle_srt" | "mix_draft";
export type ProjectAssetUploadType = "narration" | "subtitle_srt" | "account_background";
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
    description: "与连续文案对应的最终旁白音频（仅 mp3 / wav / m4a）。",
    accept: ".mp3,.wav,.m4a,audio/mpeg,audio/wav,audio/mp4,audio/x-m4a",
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
    type: "spoken_script",
    label: "口播稿",
    description: "按一句一行切好的口播正文，配音字幕按这些行切。",
    accept: ".txt,text/plain",
    icon: List,
  },
  {
    type: "caption_keywords",
    label: "字幕关键词",
    description: "按口播行标注的警示词（红）和数字（金），混剪字幕会放大这些词。",
    accept: ".json,application/json",
    icon: WandSparkles,
  },
  {
    type: "mix_draft",
    label: "剪映草稿",
    description: "已登记的可编辑剪映工程，用于最后检查。",
    accept: ".zip,application/zip",
    icon: Clapperboard,
  },
];

function assetState(asset?: ProjectAsset, generating = false) {
  if (generating) return { label: "生成中", className: "generating" };
  if (!asset) return { label: "缺失", className: "missing" };
  if (asset.state === "ready") return { label: "存在", className: "ready" };
  if (asset.state === "generating") return { label: "生成中", className: "generating" };
  return { label: "失效", className: "invalid" };
}

type ProjectAssetsProps = {
  detail: ProjectDetail;
  registeredDraft?: RegisteredMontageAsset & { task_id: string };
  onUpload: (type: "narration" | "subtitle_srt", file: File) => void;
  onReplaceBackground: (file: File) => void;
  onViewAsset: (asset: ProjectAsset) => void;
  onReviseContinuousScript?: () => void;
  onImportContinuousScript?: () => void;
  onRemakeMontage?: () => void;
  onStartSpokenLines?: () => void;
  spokenLinesLive?: boolean;
  onStartCaptionKeywords?: () => void;
  captionKeywordsLive?: boolean;
  onExportVideo?: (assetID: string) => void;
  videoExporting?: boolean;
  onGenerateNarration?: () => void;
  pendingActions: string[];
  uploadRequest: AssetUploadRequest;
};

export function ProjectAssets({
  detail,
  registeredDraft,
  onUpload,
  onReplaceBackground,
  onViewAsset,
  onReviseContinuousScript,
  onImportContinuousScript,
  onRemakeMontage,
  onStartSpokenLines,
  spokenLinesLive = false,
  onStartCaptionKeywords,
  captionKeywordsLive = false,
  onExportVideo,
  videoExporting = false,
  onGenerateNarration,
  pendingActions,
  uploadRequest,
}: ProjectAssetsProps) {
  const uploadRefs = useRef<Partial<Record<ProjectAssetUploadType, HTMLInputElement | null>>>({});

  useEffect(() => {
    if (!uploadRequest) return;
    uploadRefs.current[uploadRequest.type]?.click();
  }, [uploadRequest]);

  const isPending = (_type: ProjectAssetUploadType) => pendingActions.length > 0;
  const narrationGenerating = pendingActions.includes("generate-narration");
  const spokenGenerating = spokenLinesLive || pendingActions.includes("spoken-lines");
  const continuousScriptReady = detail.assets.continuous_script?.state === "ready";
  const spokenScriptReady = detail.assets.spoken_script?.state === "ready";
  const narrationDisabledReason = !continuousScriptReady
    ? "连续文案尚未就绪，请先备好连续文案"
    : !spokenScriptReady
      ? "口播稿尚未就绪，请先生成口播稿"
      : pendingActions.length && !narrationGenerating
        ? "当前项目还有其他操作在进行中"
        : "";
  const backgroundState = assetState(detail.background_reference || undefined);
  const readyAssetCount = assetDefinitions.reduce((count, definition) => (
    detail.assets[definition.type]?.state === "ready" ? count + 1 : count
  ), detail.background_reference?.state === "ready" ? 1 : 0);

  return (
    <section className="project-assets" aria-label="当前项目资产">
      <details className="mobile-accordion" open>
        <summary aria-label="收起或展开项目资产">
          <span>项目资产</span>
          <ChevronDown size={18} aria-hidden="true" />
        </summary>
        <div className="mobile-accordion__content">
      <div className="workbench-section-heading">
        <div>
          <span>PROJECT ASSETS</span>
          <h2>当前项目资产</h2>
        </div>
        <div className="asset-inventory" aria-label={`${readyAssetCount} 个资产已就绪，共 ${assetDefinitions.length + 1} 个`}>
          <strong>{String(readyAssetCount).padStart(2, "0")}</strong>
          <span>/ {String(assetDefinitions.length + 1).padStart(2, "0")} 已就绪</span>
        </div>
      </div>

      <div className="project-assets__list">
        {assetDefinitions.map((definition) => {
          const asset = detail.assets[definition.type];
          const generating = (narrationGenerating
            && (definition.type === "narration" || definition.type === "subtitle_srt"))
            || (spokenGenerating && definition.type === "spoken_script")
            || (captionKeywordsLive && definition.type === "caption_keywords");
          const state = assetState(asset, generating);
          const Icon = definition.icon;
          const isRegisteredDraft = definition.type === "mix_draft" && asset?.state === "ready";
          const draftDisplayName = isRegisteredDraft
            ? readableDraftName(registeredDraft?.display_name, registeredDraft?.storage_name)
              || readableDraftName(registeredDraft?.filename, registeredDraft?.storage_name)
              || readableDraftName(asset?.filename, registeredDraft?.storage_name)
              || "未命名草稿"
            : "";
          return (
            <article className={`project-asset project-asset--${state.className}`} key={definition.type}>
              <div className="project-asset__icon" aria-hidden="true"><Icon size={18} /></div>
              <div className="project-asset__copy">
                <div className="project-asset__title">
                  <strong>{definition.label}</strong>
                  <span className={`asset-state asset-state--${state.className}`}>
                    <span className="asset-state__dot" aria-hidden="true" />
                    {isRegisteredDraft ? "已登记 · 可继续编辑" : state.label}
                  </span>
                </div>
                {isRegisteredDraft ? (
                  <>
                    <strong className="project-asset__display-name">{draftDisplayName}</strong>
                    <details className="technical-history project-asset__technical">
                      <summary>技术信息</summary>
                      <dl>
                        <dt>存储名</dt><dd>{registeredDraft?.storage_name || asset.filename}</dd>
                        {registeredDraft?.draft_id ? <><dt>草稿 ID</dt><dd>{registeredDraft.draft_id}</dd></> : null}
                        {registeredDraft?.task_id ? <><dt>任务 UUID</dt><dd>{registeredDraft.task_id}</dd></> : null}
                        {registeredDraft?.path ? <><dt>登记路径</dt><dd>{registeredDraft.path}</dd></> : null}
                        <dt>资产 ID</dt><dd>{asset.id}</dd>
                      </dl>
                    </details>
                  </>
                ) : (
                  <>
                    {asset ? <small>{asset.filename} · v{asset.version}</small> : <small>等待上传</small>}
                  </>
                )}
              </div>
              <div className="project-asset__actions">
                {asset ? (
                  <button type="button" onClick={() => onViewAsset(asset)} aria-label={`查看${definition.label}`}>
                    <ExternalLink size={15} aria-hidden="true" />
                    查看
                  </button>
                ) : null}
                {!asset && definition.type === "continuous_script" && onImportContinuousScript ? (
                  <button type="button" onClick={onImportContinuousScript} aria-label="导入成品文案">
                    <Upload size={15} aria-hidden="true" />
                    导入文案
                  </button>
                ) : null}
                {asset && definition.type === "continuous_script" && onReviseContinuousScript ? (
                  <button type="button" onClick={onReviseContinuousScript} aria-label="打回重做连续文案">
                    <RotateCcw size={15} aria-hidden="true" />
                    重做
                  </button>
                ) : null}
                {definition.type === "mix_draft" && onRemakeMontage ? (
                  <button type="button" onClick={onRemakeMontage} aria-label="重做混剪">
                    <RotateCcw size={15} aria-hidden="true" />
                    重做
                  </button>
                ) : null}
                {isRegisteredDraft && onExportVideo ? (
                  <button
                    type="button"
                    onClick={() => onExportVideo(registeredDraft?.id || asset.id)}
                    disabled={videoExporting}
                    aria-busy={videoExporting}
                    title="控制本机剪映自动导出成品视频，期间请不要操作鼠标键盘"
                    aria-label={videoExporting ? "正在导出视频" : "导出视频"}
                  >
                    <Clapperboard size={15} aria-hidden="true" />
                    {videoExporting ? "正在导出…" : "导出视频"}
                  </button>
                ) : null}
                {definition.type === "spoken_script" && onStartSpokenLines && continuousScriptReady && asset ? (
                  <button
                    type="button"
                    onClick={onStartSpokenLines}
                    disabled={spokenGenerating || Boolean(pendingActions.length)}
                    aria-busy={spokenGenerating}
                    aria-label={spokenGenerating ? "正在生成口播稿" : "重做口播稿"}
                  >
                    <WandSparkles size={15} aria-hidden="true" />
                    {spokenGenerating ? "正在生成…" : "重做口播稿"}
                  </button>
                ) : null}
                {definition.type === "caption_keywords" && onStartCaptionKeywords && spokenScriptReady ? (
                  <button
                    type="button"
                    onClick={onStartCaptionKeywords}
                    disabled={captionKeywordsLive || spokenGenerating}
                    aria-busy={captionKeywordsLive}
                    title="按当前口播稿重新标注字幕关键词，完成后重做混剪即可生效"
                    aria-label={captionKeywordsLive ? "正在标注字幕关键词" : asset ? "重标关键词" : "标注关键词"}
                  >
                    <WandSparkles size={15} aria-hidden="true" />
                    {captionKeywordsLive ? "正在标注…" : asset ? "重标关键词" : "标注关键词"}
                  </button>
                ) : null}
                {definition.type === "narration" && onGenerateNarration ? (
                  <button
                    type="button"
                    onClick={onGenerateNarration}
                    disabled={Boolean(narrationDisabledReason) || narrationGenerating}
                    aria-busy={narrationGenerating}
                    title={narrationDisabledReason || "调用配音接口一次生成配音与 SRT 字幕"}
                    aria-label={narrationGenerating
                      ? "正在生成配音与字幕"
                      : narrationDisabledReason
                        ? `生成配音与字幕（${narrationDisabledReason}）`
                        : "生成配音与字幕"}
                  >
                    <WandSparkles size={15} aria-hidden="true" />
                    {narrationGenerating ? "正在生成…" : "生成配音与字幕"}
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
                        if (file) onUpload(definition.type as "narration" | "subtitle_srt", file);
                        event.target.value = "";
                      }}
                    />
                  </label>
                ) : null}
              </div>
            </article>
          );
        })}

        <article className={`project-asset project-asset--inherited project-asset--${backgroundState.className}`}>
          <div className="project-asset__icon" aria-hidden="true"><Image size={18} /></div>
          <div className="project-asset__copy">
            <div className="project-asset__title">
              <strong>账号背景图</strong>
              <span className={`asset-state asset-state--${backgroundState.className}`}>
                <span className="asset-state__dot" aria-hidden="true" />
                {backgroundState.label}
              </span>
            </div>
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
        </div>
      </details>
    </section>
  );
}

const storageIdentityPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function readableDraftName(value: string | undefined, storageName?: string) {
  const candidate = value?.trim();
  if (!candidate || storageIdentityPattern.test(candidate) || candidate === storageName?.trim()) return "";
  return candidate;
}
