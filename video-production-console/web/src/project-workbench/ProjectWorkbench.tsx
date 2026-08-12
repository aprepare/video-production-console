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
import type { ProjectAsset, ProjectDetail, ProjectTask, ProductionStage } from "./types";
import { deriveProductionStage, missingProductionInputs, nextPrimaryAction } from "./workflow";
import { ProductionRail } from "./ProductionRail";
import { ProjectAssets } from "./ProjectAssets";
import type { AssetUploadRequest, ProjectAssetUploadType } from "./ProjectAssets";
import { ProjectConversation } from "./ProjectConversation";
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
  onRemix: () => void;
  onMix: () => void;
  onPublish: () => void;
  onUpload: (type: UploadAssetType, file: File) => void;
  onSaveSourceScript: (content: string) => void;
  loadSourceScriptContent?: (assetID: string) => Promise<string>;
  onReviseContinuousScript?: () => void;
  onGenerateNarration?: () => void;
  taskModel: TaskModelOverride;
  onTaskModelChange: (value: TaskModelOverride) => void;
  taskModelDefaults?: TaskModelDefaults;
  onReplaceBackground: (file: File) => void;
  onViewAsset: (asset: ProjectAsset) => void;
  onOpenConversation: () => void;
  onOpenTask: (task: ProjectTask) => void;
  pendingActions: string[];
};

const missingLabels: Record<string, string> = {
  continuous_script: "连续文案",
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
  const stage = deriveProductionStage(detail);
  const action = nextPrimaryAction(detail);
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
  const publishingPackage = publishingTask?.publishing_package;
  const description = publishingPackage?.description || publishingPackage?.descriptions?.[0] || "";
  const descriptionForCopy = [description, publishingPackage?.cta || ""]
    .map((part) => part.trim())
    .filter(Boolean)
    .join("\n\n");
  const shortTitles = publishingPackage?.short_titles || [];
  const primaryShortTitle = shortTitles[0] || "";
  const showPublishingCopy = shouldShowPublishingCopy(stage, Boolean(publishingPackage));
  const [uploadRequest, setUploadRequest] = useState<AssetUploadRequest>(null);
  const [sourceScript, setSourceScript] = useState("");
  const [sourceDialogOpen, setSourceDialogOpen] = useState(false);
  const sourceOpenButtonRef = useRef<HTMLButtonElement>(null);
  const sourceDialogRef = useRef<HTMLElement>(null);
  const [copiedKey, setCopiedKey] = useState("");
  const projectPending = props.pendingActions.length > 0;
  const sourceReady = detail.assets.source_script?.state === "ready";
  const sourceAssetID = sourceReady ? detail.assets.source_script?.id : "";
  const sourceRemixLive = props.tasks.some((task) =>
    task.action === "remix.standard"
    && ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(task.status));
  const sourceRemixPending = props.pendingActions.includes("source-remix") || sourceRemixLive;
  const showTaskModel = Boolean(
    action && ["start-remix", "start-source-remix", "start-mixing"].includes(action.id),
  );

  useEffect(() => {
    setSourceScript("");
    setSourceDialogOpen(false);
    setCopiedKey("");
  }, [detail.project.id]);

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

  const knownMissing = new Set(["continuous_script", "narration", "subtitle_srt", "account_background", "mix_draft"]);
  const unknownMissing = missing.find((type) => !knownMissing.has(type));
  const pendingForAction = action?.id === "start-remix"
    ? "remix"
    : action?.id === "start-mixing"
      ? "montage"
      : action?.id === "publish"
        ? "publish"
        : "";
  const actionPending = projectPending || (action?.id === "start-source-remix" && sourceRemixLive) || (action?.id === "prepare-assets"
    ? missing.some((type) => props.pendingActions.includes(`upload:${type}`))
    : pendingForAction ? props.pendingActions.includes(pendingForAction) : false);

  const requestUpload = (type: ProjectAssetUploadType) => {
    setUploadRequest((current) => ({ type, token: (current?.token || 0) + 1 }));
  };

  const runPrimaryAction = () => {
    if (!action || action.disabled) return;
    if (action.id === "start-remix") props.onRemix();
    else if (action.id === "start-source-remix") {
      if (sourceScript.trim()) props.onSaveSourceScript(sourceScript.trim());
    }
    else if (action.id === "start-mixing") props.onMix();
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

  const primaryActionButton = (className: string, mobile = false) => action ? (
    <button
      type="button"
      className={`primary-action ${className}`}
      disabled={action.disabled || actionPending || Boolean(unknownMissing)}
      onClick={runPrimaryAction}
      aria-label={`${mobile ? "移动端：" : ""}${unknownMissing ? "暂无法继续" : action.id === "publish" ? "将当前项目标记为已发布" : action.label}`}
    >
      <span className="primary-action__icon" aria-hidden="true">
        {unknownMissing ? <Info size={19} /> : primaryActionIcon(action.id)}
      </span>
      <span className="primary-action__copy">
        <small>{actionPending ? "正在执行" : action.disabled ? "流程处理中" : "建议下一步"}</small>
        <strong>{unknownMissing ? "暂无法继续" : action.label}</strong>
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
            <p>项目 #{detail.project.id.slice(0, 8)}</p>
            <span>资产、任务与对话均锁定在当前项目</span>
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
              <h2>{sourceReady ? "同行原文已保存" : "需要参考同行原文？"}</h2>
              <p>{sourceReady ? `已载入 ${sourceScript.length || "…"} 字，可随时查看或替换。` : "按需打开输入框，不再占用制作状态的首屏空间。"}</p>
            </div>
          </div>
          <button
            ref={sourceOpenButtonRef}
            type="button"
            className="source-script-entry__open"
            onClick={() => setSourceDialogOpen(true)}
            disabled={sourceRemixPending}
          >
            {sourceRemixPending ? "正在保存/启动…" : sourceReady ? "查看或替换同行原文" : "粘贴同行原文"}
          </button>
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
                <p>原文只在保存后进入当前项目，并按所选模型强度启动二创。</p>
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
            {showTaskModel ? (
              <TaskModelFields
                value={props.taskModel}
                onChange={props.onTaskModelChange}
                defaults={props.taskModelDefaults}
                labelPrefix="工作台"
              />
            ) : null}
            <footer>
              <small>普通对话不会写入项目或解锁下一步。</small>
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
          {action ? (
            <>
              {primaryActionButton("desktop-primary-action")}
              {showTaskModel ? (
                <TaskModelFields
                  value={props.taskModel}
                  onChange={props.onTaskModelChange}
                  defaults={props.taskModelDefaults}
                  labelPrefix="工作台"
                />
              ) : null}
              <p className="primary-action-panel__hint">
                {unknownMissing
                  ? `无法识别项目缺项 ${unknownMissing}，请刷新项目；若仍存在，请更新控制台服务。`
                  : action.disabled
                  ? "自动工作流正在推进，完成后这里会切换到下一步。"
                  : action.id === "start-remix"
                    ? "一次启动自动完成选题卡与二创文案。"
                    : action.id === "start-mixing"
                      ? "仅使用连续文案、配音、SRT 与账号背景图。"
                      : action.id === "publish"
                        ? "检查视频描述与短标题后，可直接确认项目已发布。"
                        : "先补齐当前阶段所需文件。"}
              </p>
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
            <p>{missing.length ? missing.map((item) => missingLabels[item] || item).join(" · ") : "所有制作输入已通过检查"}</p>
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
                  ? currentTask.action === "montage.execute" ? "混剪草稿处理中" : "Codex 正在处理"
                  : "最近任务"}</strong>
                <small>{currentTask.result_summary || "查看进度与问题"}</small>
                <span className="active-task-summary__link">查看任务 <ArrowRight size={13} aria-hidden="true" /></span>
              </button>
            ) : <p>当前没有运行中的任务，执行主动作后会在此同步进展。</p>}
          </div>
        </section>

        {showPublishingCopy ? (
          <section className="publishing-review" aria-label="发布文案">
            <div className="workbench-section-heading publishing-review__heading">
              <div>
                <span>PUBLISHING COPY</span>
                <h2>发布文案</h2>
                <p>只展示视频号发布要用的「视频描述」和「短标题」，点复制即可粘贴。</p>
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
                  <p>{primaryShortTitle || "暂未生成短标题"}</p>
                  {shortTitles.length > 1 ? (
                    <ol>{shortTitles.slice(1, 4).map((title) => <li key={title}>{title}</li>)}</ol>
                  ) : null}
                </section>
              </div>
            ) : (
              <div className="publishing-review__empty">
                <strong>暂未找到发布文案</strong>
                <p>完成二创文案任务后，这里会自动显示视频描述和短标题。</p>
              </div>
            )}
          </section>
        ) : null}

        <ProjectAssets
          detail={detail}
          registeredDraft={registeredDraftTask?.montage?.registered_asset
            ? { ...registeredDraftTask.montage.registered_asset, task_id: registeredDraftTask.id }
            : undefined}
          onUpload={props.onUpload}
          onReplaceBackground={props.onReplaceBackground}
          onViewAsset={props.onViewAsset}
          onReviseContinuousScript={props.onReviseContinuousScript}
          onGenerateNarration={props.onGenerateNarration}
          pendingActions={props.pendingActions}
          uploadRequest={uploadRequest}
        />
        <ProjectConversation task={currentTask} onOpenConversation={props.onOpenConversation} onOpenTask={props.onOpenTask} />
      </div>

      <div className="mobile-primary-action-bar" aria-label="移动端下一主动作">
        {action
          ? primaryActionButton("mobile-primary-action", true)
          : <div className="primary-action-panel__complete"><CircleCheck size={20} aria-hidden="true" /> 项目流程已完成</div>}
      </div>
    </main>
  );
}
