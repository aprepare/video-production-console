import { ArrowLeft, CircleCheck, MessageSquare, Trash2 } from "lucide-react";
import { useState } from "react";
import type { ProjectAsset, ProjectDetail, ProjectTask } from "./types";
import { deriveProductionStage, missingProductionInputs, nextPrimaryAction } from "./workflow";
import { ProductionRail } from "./ProductionRail";
import { ProjectAssets } from "./ProjectAssets";
import type { AssetUploadRequest, ProjectAssetUploadType } from "./ProjectAssets";
import { ProjectConversation } from "./ProjectConversation";
import "./project-workbench.css";

type UploadAssetType = "narration" | "subtitle_srt" | "final_video";

export type ProjectWorkbenchProps = {
  detail: ProjectDetail;
  tasks: ProjectTask[];
  accountName: string;
  message?: string;
  onBack: () => void;
  onDelete: () => void;
  onRemix: () => void;
  onMix: () => void;
  onPublish: () => void;
  onUpload: (type: UploadAssetType, file: File) => void;
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
  final_video: "成片",
};

export function ProjectWorkbench(props: ProjectWorkbenchProps) {
  const { detail } = props;
  const stage = deriveProductionStage(detail);
  const action = nextPrimaryAction(detail);
  const missing = missingProductionInputs(detail);
  const currentTask = [...props.tasks]
    .filter((task) => ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(task.status))
    .sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0]
    || [...props.tasks].sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0];
  const currentTaskIsLive = Boolean(currentTask && ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(currentTask.status));
  const [uploadRequest, setUploadRequest] = useState<AssetUploadRequest>(null);
  const projectPending = props.pendingActions.length > 0;

  const knownMissing = new Set(["continuous_script", "narration", "subtitle_srt", "account_background", "mix_draft", "final_video"]);
  const unknownMissing = missing.find((type) => !knownMissing.has(type));
  const pendingForAction = action?.id === "start-remix"
    ? "remix"
    : action?.id === "start-mixing"
      ? "montage"
      : action?.id === "publish"
        ? "publish"
        : action?.id === "upload-final-video"
          ? "upload:final_video"
          : "";
  const actionPending = projectPending || (action?.id === "prepare-assets"
    ? missing.some((type) => props.pendingActions.includes(`upload:${type}`))
    : pendingForAction ? props.pendingActions.includes(pendingForAction) : false);

  const requestUpload = (type: ProjectAssetUploadType) => {
    setUploadRequest((current) => ({ type, token: (current?.token || 0) + 1 }));
  };

  const runPrimaryAction = () => {
    if (!action || action.disabled) return;
    if (action.id === "start-remix") props.onRemix();
    else if (action.id === "start-mixing") props.onMix();
    else if (action.id === "publish") props.onPublish();
    else {
      const target = action.id === "upload-final-video" ? "final_video" : missing[0];
      if (["narration", "subtitle_srt", "account_background", "final_video"].includes(target))
        requestUpload(target as ProjectAssetUploadType);
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
      {action.id === "publish" ? <CircleCheck size={19} aria-hidden="true" /> : <MessageSquare size={19} aria-hidden="true" />}
      {unknownMissing ? "暂无法继续" : action.label}
    </button>
  ) : null;

  return (
    <main className="project-workbench">
      <header className="workbench-masthead">
        <button type="button" className="workbench-icon-button" onClick={props.onBack} aria-label="返回项目看板">
          <ArrowLeft size={19} aria-hidden="true" />
        </button>
        <div className="workbench-title">
          <span>{props.accountName}</span>
          <h1>{detail.project.title}</h1>
          <p>项目 #{detail.project.id.slice(0, 8)} · 所有资产与任务均限定在当前项目</p>
        </div>
        <button type="button" className="workbench-delete" onClick={props.onDelete} aria-label="删除当前项目" disabled={projectPending}>
          <Trash2 size={16} aria-hidden="true" />
          删除项目
        </button>
      </header>

      {props.message ? <div className="workbench-notice" role="status">{props.message}</div> : null}
      <ProductionRail currentStage={stage} />

      <div className="workbench-grid">
        <section className="primary-action-panel" aria-label="下一主动作">
          <span className="panel-kicker">NEXT MOVE</span>
          <p className="primary-action-panel__stage">当前阶段 · {stage === "script" ? "文案" : stage === "assets" ? "素材" : stage === "mixing" ? "混剪" : stage === "review" ? "审核" : "已发布"}</p>
          {action ? (
            <>
              {primaryActionButton("desktop-primary-action")}
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
                        ? "确认已发布后，项目会移入已发布阶段。"
                        : "先补齐当前阶段所需文件。"}
              </p>
            </>
          ) : (
            <div className="primary-action-panel__complete"><CircleCheck size={20} aria-hidden="true" /> 项目流程已完成</div>
          )}

          <div className="input-track" aria-label="后续耗时轨">
            <span>当前输入</span>
            <div className="input-track__line" aria-hidden="true" />
            <strong>{missing.length ? missing.map((item) => missingLabels[item] || item).join(" · ") : "已齐备"}</strong>
          </div>

          <div className="active-task-summary">
            <span>{currentTaskIsLive ? "ACTIVE TASK" : "RECENT TASK"}</span>
            {currentTask ? (
              <button type="button" onClick={() => props.onOpenTask(currentTask)}>
                <strong>{currentTaskIsLive
                  ? currentTask.action === "montage.execute" ? "混剪草稿处理中" : "Codex 正在处理"
                  : "最近任务"}</strong>
                <small>{currentTask.result_summary || "查看进度与问题"}</small>
              </button>
            ) : <p>当前没有运行中的任务。</p>}
          </div>
        </section>

        <ProjectAssets
          detail={detail}
          onUpload={props.onUpload}
          onReplaceBackground={props.onReplaceBackground}
          onViewAsset={props.onViewAsset}
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
