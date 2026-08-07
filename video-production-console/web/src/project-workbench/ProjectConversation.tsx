import { ChevronDown, MessageSquare } from "lucide-react";
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

function taskLabel(task: ProjectTask) {
  if (task.action === "montage.execute") return "生成混剪草稿";
  if (task.action?.startsWith("remix.")) return "生成二创文案";
  return task.skill_name || "Codex 任务";
}

type ProjectConversationProps = {
  task?: ProjectTask;
  onOpenConversation: () => void;
  onOpenTask: (task: ProjectTask) => void;
};

export function ProjectConversation({ task, onOpenConversation, onOpenTask }: ProjectConversationProps) {
  const assistantMessages = task?.messages?.filter((message) => message.role === "assistant") || [];
  const latestAssistant = assistantMessages[assistantMessages.length - 1];

  return (
    <section className="project-conversation" aria-label="Codex 对话摘要">
      <div className="workbench-section-heading">
        <div>
          <span>CODEX CONVERSATION</span>
          <h2>Codex 对话</h2>
        </div>
        <button type="button" className="conversation-entry" onClick={onOpenConversation} aria-label="打开 Codex 对话">
          <MessageSquare size={16} aria-hidden="true" />
          打开对话
        </button>
      </div>

      {task ? (
        <div className="conversation-thread">
          <button type="button" className="conversation-task" onClick={() => onOpenTask(task)}>
            <span>{taskLabel(task)}</span>
            <strong>{statusLabels[task.status] || task.status}</strong>
          </button>
          {task.model || task.reasoning_effort ? (
            <p className="conversation-model">{[task.model, task.reasoning_effort].filter(Boolean).join(" · ")}</p>
          ) : null}
          <div className="conversation-message conversation-message--assistant">
            <span>Codex</span>
            <p>{task.result_summary || latestAssistant?.content || "任务已建立，正在整理下一步。"}</p>
          </div>
          {task.result_summary && latestAssistant?.content ? (
            <div className="conversation-message conversation-message--question">
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
        <div className="conversation-empty">
          <MessageSquare size={20} aria-hidden="true" />
          <p>当前项目还没有 Codex 任务。执行主动作后，摘要会出现在这里。</p>
        </div>
      )}
    </section>
  );
}
