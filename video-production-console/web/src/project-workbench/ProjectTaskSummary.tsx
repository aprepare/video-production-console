import { Bot, ChevronDown, MessageSquare } from "lucide-react";
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
  if (task.action === "montage.execute") return "生成混剪草稿";
  if (task.action?.startsWith("remix.")) return "生成二创文案";
  return task.skill_name || "生产任务";
}

type ProjectTaskSummaryProps = {
  task?: ProjectTask;
  onOpenTask: (task: ProjectTask) => void;
};

export function ProjectTaskSummary({ task, onOpenTask }: ProjectTaskSummaryProps) {
  const assistantMessages = task?.messages?.filter((message) => message.role === "assistant") || [];
  const latestAssistant = assistantMessages[assistantMessages.length - 1];

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
              <h2>当前任务</h2>
              <p>查看状态、回复问题或打开执行记录</p>
            </div>
          </div>

          {task ? (
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
              {task.model || task.reasoning_effort ? (
                <p className="task-summary-model">{[task.model, task.reasoning_effort].filter(Boolean).join(" · ")}</p>
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
              <p>执行左侧主动作后，任务进度和需要确认的问题会集中显示在这里。</p>
            </div>
          )}
        </div>
      </details>
    </section>
  );
}
