import { Bot, ChevronDown, MessageSquare } from "lucide-react";
import { taskModelLabel } from "../tasks/task-view";
import type { ProjectTask } from "./types";

const statusLabels: Record<string, string> = {
  queued: "排队中",
  running: "处理中",
  awaiting_input: "需要回复",
  waiting_input: "需要回复",
  resuming: "继续处理中",
  completed: "已完成",
  failed: "未完成",
  canceled: "已停止",
  cancelled: "已停止",
};

function statusTone(status: string) {
  if (["running", "queued", "resuming"].includes(status)) return "active";
  if (["awaiting_input", "waiting_input"].includes(status)) return "attention";
  if (status === "completed") return "complete";
  if (status === "failed") return "failed";
  return "neutral";
}

function taskLabel(task: ProjectTask) {
  if (task.action === "montage.execute") {
    return task.skill_name === "jianying-movie-montage" ? "生成电影混剪草稿" : "生成混剪草稿";
  }
  if (task.action?.startsWith("remix.")) return "生成二创文案";
  return task.skill_name || "生产任务";
}

function scriptPreview(text: string, limit = 280) {
  const compact = text.replace(/\s+/g, " ").trim();
  if (compact.length <= limit) return compact;
  return `${compact.slice(0, limit)}…`;
}

type ProjectTaskSummaryProps = {
  task?: ProjectTask;
  remixTasks?: ProjectTask[];
  currentScriptVersionID?: string;
  onOpenTask: (task: ProjectTask) => void;
};

export function ProjectTaskSummary({
  task,
  remixTasks = [],
  currentScriptVersionID = "",
  onOpenTask,
}: ProjectTaskSummaryProps) {
  const assistantMessages = task?.messages?.filter((message) => message.role === "assistant") || [];
  const latestAssistant = assistantMessages[assistantMessages.length - 1];
  const showRemixList = remixTasks.length > 1;

  return (
    <section className="project-task-summary" aria-label="当前任务摘要">
      <details className="mobile-accordion" open>
        <summary aria-label="收起或展开当前任务">
          <span>当前任务</span>
          <ChevronDown size={18} aria-hidden="true" />
        </summary>
        <div className="mobile-accordion__content">
          <div className="workbench-section-heading workbench-section-heading--task">
            <div>
              <span>CURRENT TASK</span>
              <h2>{showRemixList ? "各模型文案" : "当前任务"}</h2>
            </div>
          </div>

          {showRemixList ? (
            <div className="remix-compare" aria-label="文案任务对比">
              <p className="remix-compare__hint">点卡片看全文或采用。模型多于两份时，右侧栏往下滚动即可看到全部。</p>
              <div className="remix-compare__grid">
                {remixTasks.map((remixTask) => {
                  const current = Boolean(
                    currentScriptVersionID
                    && remixTask.continuous_script_version_id === currentScriptVersionID,
                  );
                  const preview = scriptPreview(remixTask.continuous_script || remixTask.result_summary || "");
                  return (
                    <button
                      type="button"
                      key={remixTask.id}
                      className={`remix-compare__card${current ? " is-current" : ""}`}
                      onClick={() => onOpenTask(remixTask)}
                    >
                      <span className="remix-compare__meta">
                        <strong>{taskModelLabel(remixTask) || "默认模型"}</strong>
                        <span className={`task-summary-status task-summary-status--${statusTone(remixTask.status)}`}>
                          <span aria-hidden="true" />
                          {statusLabels[remixTask.status] || remixTask.status}
                        </span>
                      </span>
                      {current ? <span className="remix-compare__badge">当前采用</span> : null}
                      <p className="remix-compare__preview">
                        {preview || "文案还在生成，点开看进度。"}
                      </p>
                    </button>
                  );
                })}
              </div>
            </div>
          ) : task ? (
            <div className="task-summary-thread">
              <button type="button" className="task-summary-card" onClick={() => onOpenTask(task)}>
                <span className="task-summary-card__title">
                  <small>PRODUCTION TASK</small>
                  <strong>{taskLabel(task)}</strong>
                </span>
                <span className={`task-summary-status task-summary-status--${statusTone(task.status)}`}>
                  <span aria-hidden="true" />
                  {statusLabels[task.status] || task.status}
                </span>
              </button>
              {taskModelLabel(task) ? (
                <p className="task-summary-model">{taskModelLabel(task)}</p>
              ) : null}
              <div className="task-summary-message">
                <span className="task-summary-message__author"><Bot size={14} aria-hidden="true" /> 任务结果</span>
                <p>{task.result_summary || latestAssistant?.content || "任务已建立，正在整理下一步。"}</p>
              </div>
              {task.result_summary && latestAssistant?.content ? (
                <div className="task-summary-question">
                  <span>需要确认</span>
                  <p>{latestAssistant.content}</p>
                </div>
              ) : null}
              <details className="technical-history">
                <summary>
                  <ChevronDown size={15} aria-hidden="true" />
                  技术记录
                </summary>
                <dl>
                  <dt>任务</dt><dd>{task.id}</dd>
                  <dt>动作</dt><dd>{task.action || task.type}</dd>
                  {task.prompt_snapshot ? <><dt>任务说明</dt><dd>{task.prompt_snapshot}</dd></> : null}
                </dl>
              </details>
            </div>
          ) : (
            <div className="task-summary-empty">
              <span className="task-summary-empty__icon" aria-hidden="true"><MessageSquare size={22} /></span>
              <strong>还没有任务记录</strong>
            </div>
          )}
        </div>
      </details>
    </section>
  );
}
