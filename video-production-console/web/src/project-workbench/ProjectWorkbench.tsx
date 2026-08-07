import { ArrowLeft, CircleCheck, MessageSquare, Trash2 } from "lucide-react";
import type { ProjectAsset, ProjectDetail, ProjectTask } from "./types";
import { deriveProductionStage, missingProductionInputs, nextPrimaryAction } from "./workflow";
import { ProductionRail } from "./ProductionRail";
import { ProjectAssets } from "./ProjectAssets";
import { ProjectConversation } from "./ProjectConversation";
import "./project-workbench.css";

type UploadAssetType = "continuous_script" | "narration" | "subtitle_srt" | "mix_draft" | "final_video";

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
  const activeTask = props.tasks.find((task) => ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(task.status));

  const runPrimaryAction = () => {
    if (!action || action.disabled) return;
    if (action.id === "start-remix") props.onRemix();
    else if (action.id === "start-mixing") props.onMix();
    else if (action.id === "publish") props.onPublish();
    else {
      const target = action.id === "upload-final-video" ? "上传成片" : "上传配音";
      document.querySelector<HTMLInputElement>(`input[aria-label="${target}"]`)?.click();
    }
  };

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
        <button type="button" className="workbench-delete" onClick={props.onDelete} aria-label="删除当前项目">
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
              <button
                type="button"
                className="primary-action"
                disabled={action.disabled}
                onClick={runPrimaryAction}
                aria-label={action.id === "publish" ? "将当前项目标记为已发布" : action.label}
              >
                {action.id === "publish" ? <CircleCheck size={19} aria-hidden="true" /> : <MessageSquare size={19} aria-hidden="true" />}
                {action.label}
              </button>
              <p className="primary-action-panel__hint">
                {action.disabled
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
            <span>ACTIVE TASK</span>
            {activeTask ? (
              <button type="button" onClick={() => props.onOpenTask(activeTask)}>
                <strong>{activeTask.action === "montage.execute" ? "混剪草稿处理中" : "Codex 正在处理"}</strong>
                <small>{activeTask.result_summary || "查看进度与问题"}</small>
              </button>
            ) : <p>当前没有运行中的任务。</p>}
          </div>
        </section>

        <ProjectAssets
          detail={detail}
          onUpload={props.onUpload}
          onReplaceBackground={props.onReplaceBackground}
          onViewAsset={props.onViewAsset}
        />
        <ProjectConversation tasks={props.tasks} onOpenConversation={props.onOpenConversation} onOpenTask={props.onOpenTask} />
      </div>
    </main>
  );
}
