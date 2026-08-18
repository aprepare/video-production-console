import type { FormEvent } from "react";
import { X } from "lucide-react";
import { accountName } from "../projects/stages";
import { TaskModelFields } from "../TaskModelFields";
import type { TaskModelOverride } from "../taskModel";
import { taskEventProgress, taskProgressStatus } from "../tasks/task-view";
import type { Account, IdeaCandidate, IdeaSession, PublicSettings, Task } from "../types";

type IdeaPlannerDialogProps = {
  session: IdeaSession;
  onSessionChange: (session: IdeaSession) => void;
  sessions: IdeaSession[];
  draft: boolean;
  accounts: Account[];
  task: Task | null | undefined;
  creatingProject: string;
  input: string;
  onInputChange: (value: string) => void;
  taskModel: TaskModelOverride;
  onTaskModelChange: (value: TaskModelOverride) => void;
  taskModelDefaults: PublicSettings | undefined;
  onClose: () => void;
  onCreateConversation: () => void;
  onSwitchConversation: (session: IdeaSession) => void;
  onDeleteConversation: (session: IdeaSession) => void;
  onSelectCandidate: (candidate: IdeaCandidate) => void;
  onSubmit: (event: FormEvent) => void;
};

export function IdeaPlannerDialog({
  session,
  onSessionChange,
  sessions,
  draft,
  accounts,
  task,
  creatingProject,
  input,
  onInputChange,
  taskModel,
  onTaskModelChange,
  taskModelDefaults,
  onClose,
  onCreateConversation,
  onSwitchConversation,
  onDeleteConversation,
  onSelectCandidate,
  onSubmit,
}: IdeaPlannerDialogProps) {
  return (
    <div className="modal-backdrop">
      <section
        className="preview-modal idea-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="idea-dialog-title"
        tabIndex={-1}
      >
        <div className="modal-head">
          <div>
            <span className="muted">
              选题规划 ·{" "}
              {session.account_id ? accountName(session.account_id, accounts) : "未指定账号"}
            </span>
            <h2 id="idea-dialog-title">{session.title}</h2>
          </div>
          <div className="idea-head-actions">
            <label className="idea-account-control">
              <span>选题账号</span>
              <select
                aria-label="选题账号"
                value={session.account_id || ""}
                onChange={(event) =>
                  onSessionChange({
                    ...session,
                    account_id: event.target.value || undefined,
                  })
                }
              >
                <option value="">请选择账号</option>
                {accounts.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
            <button className="close" aria-label="关闭选题规划" onClick={onClose}>
              <X size={20} aria-hidden="true" />
            </button>
          </div>
        </div>
        <div className="idea-workspace">
          <nav className="idea-conversations" aria-label="选题对话">
            <button className="idea-new-conversation" onClick={onCreateConversation}>
              新建对话
            </button>
            <div className="idea-conversation-list">
              {draft ? (
                <div className="idea-conversation-item selected">
                  <button
                    className="idea-conversation-select selected"
                    onClick={() => onSessionChange(session)}
                  >
                    <strong>新选题规划</strong>
                    <small>未发送</small>
                  </button>
                </div>
              ) : null}
              {sessions.map((item) => (
                <div className="idea-conversation-item" key={item.id}>
                  <button
                    className={`idea-conversation-select ${item.id === session.id ? "selected" : ""}`}
                    onClick={() => onSwitchConversation(item)}
                  >
                    <strong>{item.title}</strong>
                    <small>{item.status === "planning" ? "规划中" : item.status}</small>
                  </button>
                  <button
                    className="idea-delete-conversation"
                    aria-label={`删除对话 ${item.title}`}
                    title="删除对话"
                    onClick={() => onDeleteConversation(item)}
                  >
                    <X size={16} aria-hidden="true" />
                  </button>
                </div>
              ))}
            </div>
          </nav>
          <div className="idea-current-conversation">
            <div className="idea-messages">
              {session.messages?.map((item) => (
                <div className={`idea-message ${item.role}`} key={item.id}>
                  <b>{item.role === "user" ? "你" : "Codex"}</b>
                  <p>{item.content}</p>
                </div>
              ))}
              {!session.messages?.length && (
                <p className="muted">告诉我你想做的财经方向、受众或近期关注的问题。</p>
              )}
            </div>
            {session.messages?.length && !session.candidates?.length ? (
              <section className="idea-pending" aria-live="polite">
                <div className="idea-progress-head">
                  <span className={`idea-progress-dot ${task?.status || "queued"}`} />
                  <strong>
                    {task ? taskProgressStatus(task) : "已发送，正在等待 Codex 启动"}
                  </strong>
                </div>
                {task?.events?.length ? (
                  <ol className="idea-progress-events">
                    {task.events
                      .slice(-3)
                      .reverse()
                      .map((item, index) => (
                        <li key={item.id || `${item.sequence || 0}-${index}`}>
                          {taskEventProgress(item)}
                        </li>
                      ))}
                  </ol>
                ) : (
                  <p className="idea-progress-note">会自动刷新，无需停留在这个窗口。</p>
                )}
              </section>
            ) : null}
            {session.candidates?.length ? (
              <div className="idea-candidates">
                <h3>候选题</h3>
                {session.candidates.map((candidate) => (
                  <article key={candidate.id}>
                    <div>
                      <strong>{candidate.title}</strong>
                      <p>{candidate.summary}</p>
                    </div>
                    <button
                      disabled={!!creatingProject}
                      onClick={() => onSelectCandidate(candidate)}
                    >
                      {creatingProject === candidate.id ? "创建中…" : "确认并建项目"}
                    </button>
                  </article>
                ))}
              </div>
            ) : null}
            <form className="idea-compose" onSubmit={onSubmit}>
              <TaskModelFields
                value={taskModel}
                onChange={onTaskModelChange}
                defaults={taskModelDefaults}
                labelPrefix="选题"
                purpose="remix"
              />
              <label htmlFor="idea-message-input">发送选题消息</label>
              <input
                id="idea-message-input"
                autoFocus
                value={input}
                onChange={(event) => onInputChange(event.target.value)}
                placeholder="输入你的想法或追问"
              />
              <button disabled={!input.trim()}>发送</button>
            </form>
          </div>
        </div>
      </section>
    </div>
  );
}
