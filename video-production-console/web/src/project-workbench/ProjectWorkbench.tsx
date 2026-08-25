import {
  ArrowLeft,
  ArrowRight,
  CircleCheck,
  Clapperboard,
  Info,
  FileText,
  X,
  Send,
  Trash2,
  UploadCloud,
  WandSparkles,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { TaskModelFields } from "../TaskModelFields";
import type { TaskModelDefaults, TaskModelOverride } from "../taskModel";
import type { MontagePlanQC, ProjectAsset, ProjectDetail, ProjectTask, ProductionStage } from "./types";
import { canOneClickProduce, canRemakeMontage, deriveProductionStage, isLiveTaskStatus, missingProductionInputs, montageInputsReady, nextPrimaryAction } from "./workflow";
import { ProductionRail } from "./ProductionRail";
import { ProjectAssets } from "./ProjectAssets";
import type { AssetUploadRequest, ProjectAssetUploadType } from "./ProjectAssets";
import { ProjectTaskSummary } from "./ProjectTaskSummary";
import type { MontageKind } from "../production-modes/catalog";
import { montageKindLabel } from "../production-modes/catalog";
import "./project-workbench.css";

type UploadAssetType = "narration" | "subtitle_srt";

export type ProjectWorkbenchProps = {
  detail: ProjectDetail;
  tasks: ProjectTask[];
  accountName: string;
  message?: string;
  theme: "light" | "dark";
  onThemeChange: (theme: "light" | "dark") => void;
  onBack: () => void;
  onDelete: () => void;
  mixKind?: MontageKind;
  onMix: () => void;
  onMovieMix?: () => void;
  onImageVideoMix?: () => void;
  onRemakeMontage?: () => void;
  onRemakeMovieMontage?: () => void;
  onRemakeImageVideo?: () => void;
  onPublish: () => void;
  onUpload: (type: UploadAssetType, file: File) => void;
  onSaveSourceScript: (content: string) => void;
  onImportContinuousScript: (content: string) => void;
  loadSourceScriptContent?: (assetID: string) => Promise<string>;
  onReviseContinuousScript?: () => void;
  onStartSpokenLines?: () => void;
  onStartCaptionKeywords?: () => void;
  onGenerateNarration?: () => void;
  taskModel: TaskModelOverride;
  onTaskModelChange: (value: TaskModelOverride) => void;
  taskModelDefaults?: TaskModelDefaults;
  onReplaceBackground: (file: File) => void;
  onViewAsset: (asset: ProjectAsset) => void;
  onOpenTask: (task: ProjectTask) => void;
  pendingActions: string[];
  onExportVideo?: (assetID: string) => void;
  videoExporting?: boolean;
};

const missingLabels: Record<string, string> = {
  continuous_script: "连续文案",
  spoken_script: "口播稿",
  narration: "配音",
  subtitle_srt: "SRT 字幕",
  account_background: "账号背景图",
  mix_draft: "混剪草稿",
};

const stageLabels = {
  script: "文案生成",
  assets: "制作素材",
  mixing: "智能混剪",
  review: "成片审核",
  published: "发布完成",
} as const;

const stagePositions = {
  script: 1,
  assets: 2,
  mixing: 3,
  review: 4,
  published: 5,
} as const;

function primaryActionIcon(actionId: string) {
  if (actionId === "publish") return <Send size={19} aria-hidden="true" />;
  if (actionId === "start-mixing") return <Clapperboard size={19} aria-hidden="true" />;
  if (actionId === "prepare-assets") return <UploadCloud size={19} aria-hidden="true" />;
  return <WandSparkles size={19} aria-hidden="true" />;
}

function shouldShowPublishingCopy(stage: ProductionStage, hasPackage: boolean) {
  if (stage === "review" || stage === "published") return true;
  return stage === "mixing" && hasPackage;
}

function CopyFieldButton(props: {
  label: string;
  value: string;
  copied: boolean;
  onCopy: () => void;
}) {
  const disabled = !props.value.trim();
  return (
    <button
      type="button"
      className={`publishing-copy-button${props.copied ? " is-copied" : ""}`}
      aria-label={props.label}
      disabled={disabled}
      onClick={props.onCopy}
    >
      {props.copied ? "已复制" : "复制"}
    </button>
  );
}

export function ProjectWorkbench(props: ProjectWorkbenchProps) {
  const { detail, loadSourceScriptContent } = props;
  const mixKind = props.mixKind ?? "scenic";
  const stage = deriveProductionStage(detail);
  const action = nextPrimaryAction(detail);
  const mixLabel = mixKind === "movie"
    ? "开始电影混剪"
    : mixKind === "image-video"
      ? "开始图片视频"
      : "开始风景混剪";
  const remakeLabel = mixKind === "movie"
    ? "重做电影混剪"
    : mixKind === "image-video"
      ? "重做图片视频"
      : "重做混剪";
  const spokenLinesLive = props.tasks.some((task) =>
    task.action === "remix.spoken_lines"
    && isLiveTaskStatus(task.status));
  const displayAction = action?.id === "start-mixing"
    ? { ...action, label: mixLabel }
    : action?.id === "start-spoken-lines" && spokenLinesLive
      ? { ...action, label: "正在生成口播稿", disabled: true }
      : action;
  const missing = missingProductionInputs(detail);
  const currentTask = [...props.tasks]
    .filter((task) => ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(task.status))
    .sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0]
    || [...props.tasks].sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0];
  const currentTaskIsLive = Boolean(currentTask && ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(currentTask.status));
  const readyMixDraft = detail.assets.mix_draft?.state === "ready" ? detail.assets.mix_draft : undefined;
  const registeredDraftTask = [...props.tasks]
    .filter((task) =>
      task.project_id === detail.project.id
      && task.action === "montage.execute"
      && task.status === "completed"
      && task.montage?.registered_asset?.id === readyMixDraft?.id)
    .sort((left, right) => {
      const timeOrder = Date.parse(right.created_at) - Date.parse(left.created_at);
      return timeOrder || right.id.localeCompare(left.id);
    })[0];
  const publishingTask = [...props.tasks]
    .filter((task) => task.project_id === detail.project.id && task.publishing_package)
    .sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0];
  const publishingPackage = publishingTask?.publishing_package || detail.publishing_package;
  const description = publishingPackage?.description || publishingPackage?.descriptions?.[0] || "";
  const descriptionForCopy = description.trim();
  const shortTitles = publishingPackage?.short_titles || [];
  const primaryShortTitle = shortTitles[0] || "";
  const showPublishingCopy = shouldShowPublishingCopy(stage, Boolean(publishingPackage));
  const [uploadRequest, setUploadRequest] = useState<AssetUploadRequest>(null);
  const [sourceScript, setSourceScript] = useState("");
  const [sourceDialogOpen, setSourceDialogOpen] = useState(false);
  const [importScript, setImportScript] = useState("");
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const sourceOpenButtonRef = useRef<HTMLButtonElement>(null);
  const importOpenButtonRef = useRef<HTMLButtonElement>(null);
  const sourceDialogRef = useRef<HTMLElement>(null);
  const importDialogRef = useRef<HTMLElement>(null);
  const keywordAutoKey = useRef("");
  const oneClickSpokenKey = useRef("");
  const oneClickKeywordKey = useRef("");
  const oneClickNarrationKey = useRef("");
  const oneClickMontageKey = useRef("");
  const oneClickArmedAt = useRef(0);
  const [oneClickArmed, setOneClickArmed] = useState(false);
  const [copiedKey, setCopiedKey] = useState("");
  const projectPending = props.pendingActions.length > 0;
  const sourceReady = detail.assets.source_script?.state === "ready";
  const sourceAssetID = sourceReady ? detail.assets.source_script?.id : "";
  const sourceRemixLive = props.tasks.some((task) =>
    task.action === "remix.standard"
    && isLiveTaskStatus(task.status));
  const sourceRemixPending = props.pendingActions.includes("source-remix") || sourceRemixLive;
  const importScriptPending = props.pendingActions.includes("save-continuous-script");
  const showTaskModel = Boolean(
    action && ["start-source-remix", "start-spoken-lines", "start-mixing"].includes(action.id),
  );
  const montageLive = props.tasks.some((task) =>
    task.action === "montage.execute"
    && isLiveTaskStatus(task.status));
  const remakeReady = canRemakeMontage(detail) && !montageLive && !props.pendingActions.includes("montage");
  const remakeMontage = Boolean(mixKind === "scenic" && props.onRemakeMontage && remakeReady);
  const remakeMovieMontage = Boolean(mixKind === "movie" && props.onRemakeMovieMontage && remakeReady);
  const remakeImageVideo = Boolean(mixKind === "image-video" && props.onRemakeImageVideo && remakeReady);
  const remakeCurrent = remakeMontage || remakeMovieMontage || remakeImageVideo;
  const onRemakeCurrent = mixKind === "movie"
    ? props.onRemakeMovieMontage
    : mixKind === "image-video"
      ? props.onRemakeImageVideo
      : props.onRemakeMontage;

  useEffect(() => {
    setSourceScript("");
    setSourceDialogOpen(false);
    setImportScript("");
    setImportDialogOpen(false);
    setCopiedKey("");
    keywordAutoKey.current = "";
    oneClickSpokenKey.current = "";
    oneClickKeywordKey.current = "";
    oneClickNarrationKey.current = "";
    oneClickMontageKey.current = "";
    oneClickArmedAt.current = 0;
    setOneClickArmed(false);
  }, [detail.project.id]);

  // 二创出稿后停在「生成口播稿」，等操作员确认文案再点主按钮或「一键生成」。
  // 口播稿就绪后自动标注字幕关键词。这是可选增强：失败或缺席时混剪
  // 回落到本地词表，所以不占主按钮，也不阻塞配音。
  const captionKeywordsLive = props.tasks.some((task) =>
    task.action === "remix.caption_keywords"
    && isLiveTaskStatus(task.status));
  useEffect(() => {
    if (oneClickArmed) return;
    if (!props.onStartCaptionKeywords) return;
    if (detail.assets.spoken_script?.state !== "ready") return;
    if (detail.assets.caption_keywords?.state === "ready") return;
    if (captionKeywordsLive) return;
    const versionKey = detail.assets.spoken_script?.id;
    if (!versionKey || keywordAutoKey.current === versionKey) return;
    keywordAutoKey.current = versionKey;
    props.onStartCaptionKeywords();
  }, [oneClickArmed, captionKeywordsLive, props.onStartCaptionKeywords, detail.assets.spoken_script?.id, detail.assets.spoken_script?.state, detail.assets.caption_keywords?.state]);

  const narrationGenerating = props.pendingActions.includes("generate-narration");
  const oneClickBusy = projectPending || spokenLinesLive || montageLive || narrationGenerating || sourceRemixLive;

  const startCurrentMix = () => {
    if (mixKind === "movie") props.onMovieMix?.();
    else if (mixKind === "image-video") props.onImageVideoMix?.();
    else props.onMix();
  };

  const failedSinceArm = (action: string) => props.tasks.some((task) =>
    ["failed", "canceled", "interrupted_on_restart"].includes(task.status)
    && task.action === action
    && Date.parse(task.created_at) >= oneClickArmedAt.current - 5000);

  // 文案确定后一点：口播稿 → 先打字幕关键词（失败不挡）→ 配音字幕 → 混剪到剪映草稿。
  // 已有 ready 草稿不会重做。某步失败就停，按钮恢复，再点一次会重试。
  useEffect(() => {
    if (!oneClickArmed) return;
    if (detail.assets.mix_draft?.state === "ready" || !canOneClickProduce(detail)) {
      setOneClickArmed(false);
      return;
    }
    if (oneClickBusy) return;

    const scriptID = detail.assets.continuous_script?.id || "";
    const spokenReady = detail.assets.spoken_script?.state === "ready";
    const spokenID = detail.assets.spoken_script?.id || "";
    const keywordsReady = detail.assets.caption_keywords?.state === "ready";
    const narrationReady = detail.assets.narration?.state === "ready";
    const srtReady = detail.assets.subtitle_srt?.state === "ready";

    if (!spokenReady) {
      if (!props.onStartSpokenLines || !scriptID) return;
      if (oneClickSpokenKey.current === scriptID) {
        if (failedSinceArm("remix.spoken_lines")) setOneClickArmed(false);
        return;
      }
      oneClickSpokenKey.current = scriptID;
      props.onStartSpokenLines();
      return;
    }

    if (!keywordsReady && !captionKeywordsLive && props.onStartCaptionKeywords && spokenID && oneClickKeywordKey.current !== spokenID) {
      oneClickKeywordKey.current = spokenID;
      props.onStartCaptionKeywords();
      return;
    }

    if (!narrationReady || !srtReady) {
      if (!props.onGenerateNarration || !spokenID) return;
      if (oneClickNarrationKey.current === spokenID) {
        if (!narrationGenerating) setOneClickArmed(false);
        return;
      }
      oneClickNarrationKey.current = spokenID;
      props.onGenerateNarration();
      return;
    }

    if (!montageInputsReady(detail)) {
      setOneClickArmed(false);
      return;
    }
    const mixKey = `${detail.assets.narration?.id || ""}:${detail.assets.subtitle_srt?.id || ""}`;
    if (oneClickMontageKey.current === mixKey) {
      if (failedSinceArm("montage.execute")) setOneClickArmed(false);
      return;
    }
    oneClickMontageKey.current = mixKey;
    startCurrentMix();
  }, [
    oneClickArmed,
    oneClickBusy,
    captionKeywordsLive,
    narrationGenerating,
    detail,
    mixKind,
    props.tasks,
    props.onStartSpokenLines,
    props.onStartCaptionKeywords,
    props.onGenerateNarration,
    props.onMix,
    props.onMovieMix,
    props.onImageVideoMix,
  ]);

  const oneClickAvailable = canOneClickProduce(detail);
  const armOneClickProduce = () => {
    if (!oneClickAvailable || oneClickBusy) return;
    oneClickSpokenKey.current = "";
    oneClickKeywordKey.current = "";
    oneClickNarrationKey.current = "";
    oneClickMontageKey.current = "";
    oneClickArmedAt.current = Date.now();
    setOneClickArmed(true);
  };

  useEffect(() => {
    if (!copiedKey) return;
    const timer = window.setTimeout(() => setCopiedKey(""), 1600);
    return () => window.clearTimeout(timer);
  }, [copiedKey]);

  useEffect(() => {
    if (!sourceDialogOpen) return;
    const openButton = sourceOpenButtonRef.current;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setSourceDialogOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(sourceDialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ) || []);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      openButton?.focus();
    };
  }, [sourceDialogOpen]);

  useEffect(() => {
    if (!importDialogOpen) return;
    const openButton = importOpenButtonRef.current;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setImportDialogOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(importDialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ) || []);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      openButton?.focus();
    };
  }, [importDialogOpen]);

  async function copyPublishingText(key: string, value: string) {
    const text = value.trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopiedKey(key);
    } catch {
      // Clipboard can fail without a secure context; leave the button label unchanged.
    }
  }

  useEffect(() => {
    if (!sourceAssetID || !loadSourceScriptContent) return;
    let active = true;
    void loadSourceScriptContent(sourceAssetID)
      .then((content) => {
        if (active) setSourceScript(content);
      })
      .catch(() => {
        // The save action remains available if historical source content cannot be read.
      });
    return () => {
      active = false;
    };
  }, [loadSourceScriptContent, sourceAssetID]);

  const knownMissing = new Set(["continuous_script", "spoken_script", "narration", "subtitle_srt", "account_background", "mix_draft"]);
  const unknownMissing = missing.find((type) => !knownMissing.has(type));
  const pendingForAction = action?.id === "start-mixing"
    ? "montage"
    : action?.id === "start-spoken-lines"
      ? "spoken-lines"
      : action?.id === "publish"
        ? "publish"
        : "";
  const actionPending = projectPending || (action?.id === "start-source-remix" && sourceRemixLive) || (action?.id === "start-spoken-lines" && spokenLinesLive) || (action?.id === "prepare-assets"
    ? missing.some((type) => props.pendingActions.includes(`upload:${type}`))
    : pendingForAction ? props.pendingActions.includes(pendingForAction) : false);

  const requestUpload = (type: ProjectAssetUploadType) => {
    setUploadRequest((current) => ({ type, token: (current?.token || 0) + 1 }));
  };

  const runPrimaryAction = () => {
    if (!action || action.disabled) return;
    if (action.id === "start-source-remix") {
      if (sourceScript.trim()) props.onSaveSourceScript(sourceScript.trim());
    }
    else if (action.id === "start-spoken-lines") {
      props.onStartSpokenLines?.();
    }
    else if (action.id === "start-mixing") {
      startCurrentMix();
    }
    else if (action.id === "publish") props.onPublish();
    else {
      const target = missing[0];
      if (target === "narration" || target === "subtitle_srt" || target === "account_background")
        requestUpload(target);
    }
  };

  const saveSourceScriptAndRemix = () => {
    if (sourceScript.trim()) {
      props.onSaveSourceScript(sourceScript.trim());
      setSourceDialogOpen(false);
    }
  };

  const openImportDialog = () => {
    setSourceDialogOpen(false);
    setImportDialogOpen(true);
  };

  const saveImportedScript = () => {
    if (!importScript.trim()) return;
    props.onImportContinuousScript(importScript.trim());
    setImportDialogOpen(false);
  };

  const loadImportedScriptFile = (file?: File) => {
    if (!file) return;
    void file.text().then((text) => setImportScript(text));
  };

  const primaryActionButton = (className: string, mobile = false) => displayAction ? (
    <button
      type="button"
      className={`primary-action ${className}`}
      disabled={displayAction.disabled || actionPending || Boolean(unknownMissing)}
      onClick={runPrimaryAction}
      aria-label={`${mobile ? "移动端：" : ""}${unknownMissing ? "暂无法继续" : displayAction.id === "publish" ? "将当前项目标记为已发布" : displayAction.label}`}
    >
      <span className="primary-action__icon" aria-hidden="true">
        {unknownMissing ? <Info size={19} /> : primaryActionIcon(displayAction.id)}
      </span>
      <span className="primary-action__copy">
        <small>{actionPending ? "正在执行" : displayAction.disabled ? "流程处理中" : "建议下一步"}</small>
        <strong>{unknownMissing ? "暂无法继续" : displayAction.label}</strong>
      </span>
      <span className="primary-action__arrow" aria-hidden="true"><ArrowRight size={17} /></span>
    </button>
  ) : null;

  return (
    <main className="project-workbench">
      <header className="workbench-masthead">
        <button type="button" className="workbench-icon-button" onClick={props.onBack} aria-label="返回项目看板">
          <ArrowLeft size={19} aria-hidden="true" />
        </button>
        <div className="workbench-title">
          <div className="workbench-title__meta">
            <span className="workbench-account">{props.accountName}</span>
            <span className={`workbench-stage-badge workbench-stage-badge--${stage}`}>
              <span aria-hidden="true" />
              {stageLabels[stage]}
            </span>
          </div>
          <h1>{detail.project.title}</h1>
          <div className="workbench-title__details">
            <p>{montageKindLabel(mixKind)} · #{detail.project.id.slice(0, 8)}</p>
            {mixKind === "image-video" ? (
              <p>
                这是旧的「口播配静帧」混剪，仍会使用风景素材库。要用 AI 图片做成剪映草稿，请回首页进入「图文制作」。
              </p>
            ) : null}
          </div>
        </div>
        <div className="workbench-masthead__actions">
          <label className="workbench-theme-control">
            主题
            <select
              aria-label="选择项目工作台主题"
              value={props.theme}
              onChange={(event) => props.onThemeChange(event.target.value as "light" | "dark")}
            >
              <option value="light">日间</option>
              <option value="dark">夜间</option>
            </select>
          </label>
          <button type="button" className="workbench-delete" onClick={props.onDelete} aria-label="删除当前项目" disabled={projectPending}>
            <Trash2 size={16} aria-hidden="true" />
            <span>删除项目</span>
          </button>
        </div>
      </header>

      {props.message ? <div className="workbench-notice" role="alert">{props.message}</div> : null}
      <ProductionRail currentStage={stage} />

      {stage === "script" && (
        <section className="source-script-entry" aria-label="同行原文输入">
          <div className="source-script-entry__copy">
            <FileText size={18} aria-hidden="true" />
            <div>
              <h2>{sourceReady ? "同行原文已保存" : "同行原文"}</h2>
              <p>{sourceReady ? `已载入 ${sourceScript.length || "…"} 字` : "需要时再打开。"}</p>
            </div>
          </div>
          <div className="source-script-entry__actions">
            <button
              ref={sourceOpenButtonRef}
              type="button"
              className="source-script-entry__open"
              onClick={() => setSourceDialogOpen(true)}
              disabled={sourceRemixPending}
            >
              {sourceRemixPending ? "正在保存/启动…" : sourceReady ? "查看或替换同行原文" : "粘贴同行原文"}
            </button>
            <button
              ref={importOpenButtonRef}
              type="button"
              className="source-script-entry__skip"
              onClick={openImportDialog}
              disabled={importScriptPending}
            >
              {importScriptPending ? "正在导入…" : "导入成品文案"}
            </button>
          </div>
        </section>
      )}

      {sourceDialogOpen ? (
        <div
          className="source-script-dialog-backdrop"
          role="presentation"
          onClick={(event) => {
            if (event.target === event.currentTarget) setSourceDialogOpen(false);
          }}
        >
          <section
            ref={sourceDialogRef}
            className="source-script-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="source-script-dialog-title"
          >
            <header>
              <div>
                <span className="panel-kicker">SOURCE SCRIPT</span>
                <h2 id="source-script-dialog-title">{sourceReady ? "查看或替换同行原文" : "粘贴同行原文"}</h2>
                <p>保存后按所选方式开始二创。</p>
              </div>
              <button type="button" className="source-script-dialog__close" aria-label="关闭原文输入" onClick={() => setSourceDialogOpen(false)}>
                <X size={18} aria-hidden="true" />
              </button>
            </header>
            <textarea
              autoFocus
              aria-label="同行原文"
              value={sourceScript}
              onChange={(event) => setSourceScript(event.target.value)}
              placeholder="把同行文章全文粘贴到这里…"
            />
            <TaskModelFields
              value={props.taskModel}
              onChange={props.onTaskModelChange}
              defaults={props.taskModelDefaults}
              labelPrefix="工作台"
              purpose="remix"
              multiModel
            />
            <footer>
              <small />
              <div>
                <button type="button" className="source-script-dialog__cancel" onClick={() => setSourceDialogOpen(false)}>取消</button>
                <button
                  type="button"
                  className="source-script-dialog__save"
                  onClick={saveSourceScriptAndRemix}
                  disabled={!sourceScript.trim() || sourceRemixPending}
                  aria-busy={sourceRemixPending}
                >
                  {sourceRemixPending ? "正在保存/启动…" : "保存原文并开始二创"}
                </button>
              </div>
            </footer>
          </section>
        </div>
      ) : null}

      {importDialogOpen ? (
        <div
          className="source-script-dialog-backdrop"
          role="presentation"
          onClick={(event) => {
            if (event.target === event.currentTarget) setImportDialogOpen(false);
          }}
        >
          <section
            ref={importDialogRef}
            className="source-script-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="import-script-dialog-title"
          >
            <header>
              <div>
                <span className="panel-kicker">FINISHED SCRIPT</span>
                <h2 id="import-script-dialog-title">导入成品文案</h2>
                <p>跳过二创。可粘贴连续正文，或换说法模型返回的 JSON。</p>
              </div>
              <button type="button" className="source-script-dialog__close" aria-label="关闭成品文案导入" onClick={() => setImportDialogOpen(false)}>
                <X size={18} aria-hidden="true" />
              </button>
            </header>
            <textarea
              autoFocus
              aria-label="成品文案"
              value={importScript}
              onChange={(event) => setImportScript(event.target.value)}
              placeholder="把连续正文，或换说法模型返回的 JSON 贴到这里…"
            />
            <label className="import-script-file">
              或选择本地文案文件
              <input
                type="file"
                accept=".txt,.md,text/plain,text/markdown"
                aria-label="选择成品文案文件"
                onChange={(event) => {
                  loadImportedScriptFile(event.target.files?.[0]);
                  event.target.value = "";
                }}
              />
            </label>
            <footer>
              <small />
              <div>
                <button type="button" className="source-script-dialog__cancel" onClick={() => setImportDialogOpen(false)}>取消</button>
                <button
                  type="button"
                  className="source-script-dialog__save"
                  onClick={saveImportedScript}
                  disabled={!importScript.trim() || importScriptPending}
                  aria-busy={importScriptPending}
                >
                  {importScriptPending ? "正在导入…" : "保存并生成口播稿"}
                </button>
              </div>
            </footer>
          </section>
        </div>
      ) : null}

      <div className={`workbench-grid${showPublishingCopy ? " workbench-grid--review" : ""}`}>
        <section className="primary-action-panel" aria-label="下一主动作">
          <div className="primary-action-panel__head">
            <div>
              <span className="panel-kicker">PRODUCTION CONTROL</span>
              <p className="primary-action-panel__stage">当前阶段 · {stageLabels[stage]}</p>
            </div>
            <span className="primary-action-panel__stage-index" aria-label={`五个阶段中的第 ${stagePositions[stage]} 阶段`}>
              {String(stagePositions[stage]).padStart(2, "0")}
              <small>/ 05</small>
            </span>
          </div>
          <h2 className="primary-action-panel__title">{stage === "review" || stage === "published" ? "确认发布信息" : "继续当前制作"}</h2>
          {displayAction ? (
            <>
              {primaryActionButton("desktop-primary-action")}
              {oneClickAvailable ? (
                <button
                  type="button"
                  className="remake-montage-action"
                  onClick={armOneClickProduce}
                  disabled={oneClickBusy || oneClickArmed}
                  aria-busy={oneClickArmed || oneClickBusy}
                  title="确认当前连续文案后，自动生成口播稿、配音字幕并做到剪映草稿。已有剪映草稿不会重做。"
                  aria-label={oneClickArmed ? "正在一键生成到剪映草稿" : "一键生成到剪映草稿"}
                >
                  {oneClickArmed ? "正在一键生成到剪映草稿" : "一键生成到剪映草稿"}
                </button>
              ) : null}
              {remakeCurrent && onRemakeCurrent ? (
                <button
                  type="button"
                  className="remake-montage-action"
                  onClick={onRemakeCurrent}
                >
                  {remakeLabel}
                </button>
              ) : null}
              {displayAction.id === "start-source-remix" ? (
                <button
                  type="button"
                  className="remake-montage-action"
                  onClick={openImportDialog}
                  disabled={importScriptPending}
                >
                  导入成品文案，接着生成口播稿
                </button>
              ) : null}
              {showTaskModel ? (
                <TaskModelFields
                  value={props.taskModel}
                  onChange={props.onTaskModelChange}
                  defaults={props.taskModelDefaults}
                  labelPrefix="工作台"
                  purpose={
                    displayAction.id === "start-source-remix"
                      ? "remix"
                      : displayAction.id === "start-spoken-lines"
                        ? "spoken"
                        : "codex"
                  }
                  multiModel={displayAction.id === "start-source-remix"}
                />
              ) : null}
              {unknownMissing ? (
                <p className="primary-action-panel__hint">{`无法识别缺项 ${unknownMissing}，请刷新项目。`}</p>
              ) : null}
            </>
          ) : (
            <div className="primary-action-panel__complete"><CircleCheck size={20} aria-hidden="true" /> 项目流程已完成</div>
          )}

          <div className="input-track" aria-label="制作输入检查">
            <div className="input-track__head">
              <span>INPUT CHECK</span>
              <strong className={missing.length ? "is-pending" : "is-ready"}>
                {missing.length ? `${missing.length} 项待补齐` : "输入已齐备"}
              </strong>
            </div>
            <div className="input-track__line" aria-hidden="true"><span /></div>
            <p>{missing.length ? missing.map((item) => missingLabels[item] || item).join(" · ") : "输入已齐备"}</p>
          </div>

          <div className="active-task-summary">
            <div className="active-task-summary__head">
              <span>{currentTaskIsLive ? "ACTIVE TASK" : "RECENT TASK"}</span>
              <span className={`task-presence ${currentTaskIsLive ? "task-presence--live" : "task-presence--idle"}`}>
                <span aria-hidden="true" />
                {currentTaskIsLive ? "运行中" : currentTask ? "最近记录" : "空闲"}
              </span>
            </div>
            {currentTask ? (
              <button type="button" onClick={() => props.onOpenTask(currentTask)}>
                <strong>{currentTaskIsLive
                  ? currentTask.action === "montage.execute"
                    ? currentTask.skill_name === "jianying-movie-montage" ? "电影混剪草稿处理中" : "混剪草稿处理中"
                    : "任务处理中"
                  : "最近任务"}</strong>
                <small>{currentTask.result_summary || "查看进度与问题"}</small>
                <span className="active-task-summary__link">查看任务 <ArrowRight size={13} aria-hidden="true" /></span>
              </button>
            ) : <p>空闲</p>}
          </div>
        </section>

        {showPublishingCopy ? (
          <section className="publishing-review" aria-label="发布文案">
            <div className="workbench-section-heading publishing-review__heading">
              <div>
                <span>PUBLISHING COPY</span>
                <h2>发布文案</h2>
              </div>
              {publishingTask ? <span className="publishing-review__source">来自最近二创结果</span> : null}
            </div>
            {publishingPackage ? (
              <div className="publishing-review__content publishing-review__content--compact">
                <section className="publishing-copy-block publishing-copy-block--description">
                  <div className="publishing-copy-block__head">
                    <span>视频描述</span>
                    <CopyFieldButton
                      label="复制视频描述"
                      value={descriptionForCopy}
                      copied={copiedKey === "description"}
                      onCopy={() => void copyPublishingText("description", descriptionForCopy)}
                    />
                  </div>
                  <p>{description || "暂未生成视频描述"}</p>
                  {publishingPackage.cta ? <small>{publishingPackage.cta}</small> : null}
                </section>
                <section className="publishing-copy-block">
                  <div className="publishing-copy-block__head">
                    <span>短标题</span>
                    <CopyFieldButton
                      label="复制短标题"
                      value={primaryShortTitle}
                      copied={copiedKey === "short-title"}
                      onCopy={() => void copyPublishingText("short-title", primaryShortTitle)}
                    />
                  </div>
                  {shortTitles.length ? (
                    <div className="short-title-row">
                      {shortTitles.map((title) => (
                        <button
                          type="button"
                          key={title}
                          className={`short-title-chip${copiedKey === `short-title:${title}` ? " is-copied" : ""}`}
                          onClick={() => void copyPublishingText(`short-title:${title}`, title)}
                          title="点击复制"
                        >
                          {title}
                        </button>
                      ))}
                    </div>
                  ) : (
                    <p>暂未生成短标题</p>
                  )}
                </section>
              </div>
            ) : (
              <div className="publishing-review__empty">
                <strong>暂未找到发布文案</strong>
                <p>二创完成后会显示在这里。</p>
              </div>
            )}
          </section>
        ) : null}

        {detail.montage_qc ? <MontagePlanQCCard qc={detail.montage_qc} /> : null}

        <ProjectAssets
          detail={detail}
          registeredDraft={registeredDraftTask?.montage?.registered_asset
            ? { ...registeredDraftTask.montage.registered_asset, task_id: registeredDraftTask.id }
            : undefined}
          onUpload={props.onUpload}
          onReplaceBackground={props.onReplaceBackground}
          onViewAsset={props.onViewAsset}
          onReviseContinuousScript={props.onReviseContinuousScript}
          onImportContinuousScript={stage === "script" ? openImportDialog : undefined}
          onRemakeMontage={remakeCurrent ? onRemakeCurrent : undefined}
          onStartSpokenLines={props.onStartSpokenLines}
          spokenLinesLive={spokenLinesLive}
          onStartCaptionKeywords={props.onStartCaptionKeywords}
          captionKeywordsLive={captionKeywordsLive}
          onExportVideo={props.onExportVideo}
          videoExporting={props.videoExporting}
          onGenerateNarration={props.onGenerateNarration}
          pendingActions={props.pendingActions}
          uploadRequest={uploadRequest}
        />
        <ProjectTaskSummary
          task={currentTask}
          remixTasks={[...props.tasks]
            .filter((task) => task.action === "remix.standard")
            .sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))
            .slice(0, 4)}
          currentScriptVersionID={detail.assets.continuous_script?.id || ""}
          onOpenTask={props.onOpenTask}
        />
      </div>

      <div className="mobile-primary-action-bar" aria-label="移动端下一主动作">
        {displayAction
          ? primaryActionButton("mobile-primary-action", true)
          : <div className="primary-action-panel__complete"><CircleCheck size={20} aria-hidden="true" /> 项目流程已完成</div>}
      </div>
    </main>
  );
}

function percent(value: number) {
  return `${Math.round(value * 100)}%`;
}

function warningText(warning: string | { code?: string; message?: string }) {
  if (typeof warning === "string") return warning;
  return warning.message || warning.code || "";
}

function MontagePlanQCCard(props: { qc: MontagePlanQC }) {
  const { qc } = props;
  const captionLabel = qc.caption_mode === "off"
    ? "关闭字幕"
    : qc.caption_mode === "spoken"
      ? "口播稿逐行字幕"
      : "只显示重点句";
  const warnings = (qc.warnings || []).map(warningText).filter(Boolean);
  return (
    <section className="montage-plan-qc" aria-label="本次混剪计划摘要">
      <h2>本次混剪计划</h2>
      <dl>
        <div><dt>B-roll 占比</dt><dd>{percent(qc.broll_ratio)}</dd></div>
        <div><dt>电影片段占比</dt><dd>{percent(qc.movie_ratio)}</dd></div>
        <div><dt>图片占比</dt><dd>{percent(qc.image_ratio)}</dd></div>
        <div><dt>字幕</dt><dd>{captionLabel}，覆盖 {percent(qc.caption_coverage)}</dd></div>
        <div><dt>明显效果</dt><dd>{qc.obvious_effect_count} 个</dd></div>
      </dl>
      {warnings.length > 0 ? (
        <ul>
          {warnings.map((warning) => <li key={warning}>{warning}</li>)}
        </ul>
      ) : null}
    </section>
  );
}
