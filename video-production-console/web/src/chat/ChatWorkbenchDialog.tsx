import type { FormEvent } from "react";
import { X } from "lucide-react";
import { formatDate } from "../projects/stages";
import type { ChatDetail, ChatMessage, ChatSession, HistoryThread, PublicSettings } from "../types";

const historySourceLabels: Record<HistoryThread["source"], string> = {
  desktop: "桌面版",
  cli: "CLI",
  task: "控制台任务",
};

type ChatWorkbenchDialogProps = {
  detail: ChatDetail | null;
  sessions: ChatSession[];
  visibleMessages: ChatMessage[];
  technicalMessages: ChatMessage[];
  creating: boolean;
  creationSource: "console" | "desktop";
  onCreationSourceChange: (source: "console" | "desktop") => void;
  onCreate: (source: "console" | "desktop") => void;
  onSelectSession: (session: ChatSession) => void;
  onDeleteSession: (session: ChatSession) => void;
  settings: { public: PublicSettings } | null | undefined;
  historySource: string;
  onHistorySourceChange: (source: string) => void;
  historyThreads: HistoryThread[];
  onApplyHistoryThread: (thread: HistoryThread, mode: "resume" | "fork") => void;
  input: string;
  onInputChange: (value: string) => void;
  sending: boolean;
  onClose: () => void;
  onSubmit: (event: FormEvent) => void;
};

export function ChatWorkbenchDialog({
  detail,
  sessions,
  visibleMessages,
  technicalMessages,
  creating,
  creationSource,
  onCreationSourceChange,
  onCreate,
  onSelectSession,
  onDeleteSession,
  settings,
  historySource,
  onHistorySourceChange,
  historyThreads,
  onApplyHistoryThread,
  input,
  onInputChange,
  sending,
  onClose,
  onSubmit,
}: ChatWorkbenchDialogProps) {
  return (
    <div className="modal-backdrop chat-backdrop" onClick={onClose}>
      <section
        className="chat-workbench"
        role="dialog"
        aria-modal="true"
        aria-labelledby="chat-dialog-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <aside className="chat-session-rail">
          <div className="chat-rail-head">
            <div>
              <strong>Codex 对话</strong>
              <small>项目内沟通与本机历史</small>
            </div>
            <button
              className="chat-create-primary"
              disabled={creating}
              onClick={() => {
                onCreationSourceChange("console");
                onCreate("console");
              }}
            >
              {creating && creationSource === "console" ? "创建中…" : "新建对话"}
            </button>
            <button
              className="chat-create-secondary"
              disabled={creating}
              onClick={() => {
                onCreationSourceChange("desktop");
                onCreate("desktop");
              }}
            >
              {creating && creationSource === "desktop" ? "创建中…" : "在桌面版新建"}
            </button>
          </div>
          <section className="chat-rail-section chat-current-sessions" aria-label="当前对话">
            <div className="chat-rail-section__head">
              <strong>当前对话</strong>
              <small>{sessions.length} 个</small>
            </div>
            <div className="chat-session-list">
              {sessions.map((session) => (
                <div className="chat-session-item" key={session.id}>
                  <button
                    className={detail?.session.id === session.id ? "selected" : ""}
                    onClick={() => onSelectSession(session)}
                  >
                    <strong>{session.title}</strong>
                    <small>
                      {session.source === "desktop" ? "桌面版" : "控制台"} ·{" "}
                      {session.status === "running" ? "处理中" : "可继续"}
                    </small>
                  </button>
                  <button
                    className="chat-session-delete"
                    aria-label={`删除对话 ${session.title}`}
                    onClick={() => onDeleteSession(session)}
                  >
                    <X size={16} aria-hidden="true" />
                  </button>
                </div>
              ))}
              {!sessions.length ? <p className="chat-rail-empty">还没有当前对话</p> : null}
            </div>
          </section>
          <section className="chat-rail-section chat-history-section" aria-label="本机历史">
            <div className="history-rail-head">
              <div>
                <strong>本机历史</strong>
                <small>最近 {settings?.public.codex_history_limit || 10} 条</small>
              </div>
              <select
                aria-label="筛选本机历史来源"
                value={historySource}
                onChange={(event) => onHistorySourceChange(event.target.value)}
              >
                <option value="">全部</option>
                <option value="desktop">桌面版</option>
                <option value="cli">CLI</option>
                <option value="task">任务</option>
              </select>
            </div>
            <div className="history-thread-list">
              {historyThreads.map((thread) => (
                <article key={`${thread.source}-${thread.id}`}>
                  <strong>{thread.title || "未命名会话"}</strong>
                  <small>
                    {historySourceLabels[thread.source] || "本机任务"} · {formatDate(thread.recency)}
                  </small>
                  {thread.preview ? <p>{thread.preview}</p> : null}
                  <div>
                    <button
                      disabled={thread.active}
                      title={thread.active ? "该会话正在别处运行" : "恢复原来的 Codex 会话"}
                      onClick={() => onApplyHistoryThread(thread, "resume")}
                    >
                      继续
                    </button>
                    <button
                      className="history-fork"
                      onClick={() => onApplyHistoryThread(thread, "fork")}
                    >
                      复制
                    </button>
                  </div>
                </article>
              ))}
              {!historyThreads.length && <p className="chat-rail-empty">暂无可接入的本机历史</p>}
            </div>
            <p className="history-inline-help">
              “继续”接回原会话；“复制”会新建副本，不影响原会话。
            </p>
          </section>
        </aside>
        <div className="chat-main">
          <div className="chat-main-head">
            <div>
              <span className="eyebrow">
                {detail?.session.source === "desktop" ? "桌面版会话" : "控制台会话"}
              </span>
              <h2 id="chat-dialog-title">{detail?.session.title || "新建一个 Codex 对话"}</h2>
              {detail?.session ? (
                <p className="chat-session-meta">
                  {detail.session.status === "running" ? "Codex 正在处理" : "可以继续对话"}
                  {detail.session.model ? ` · ${detail.session.model}` : ""}
                  {detail.session.reasoning_effort ? ` · ${detail.session.reasoning_effort}` : ""}
                </p>
              ) : null}
            </div>
            <button className="close" aria-label="关闭 Codex 对话" onClick={onClose}>
              <X size={20} aria-hidden="true" />
            </button>
          </div>
          <div className="chat-messages" aria-live="polite">
            {visibleMessages.map((item, index) => (
              <article
                className={`chat-bubble ${item.role}`}
                key={item.id || `${item.role}-${item.created_at}-${index}`}
              >
                <b>{item.role === "user" ? "你" : "Codex"}</b>
                <p>{item.content}</p>
                {item.role === "user" && item.delivery_status === "queued" ? (
                  <small>已排队</small>
                ) : null}
              </article>
            ))}
            {!visibleMessages.length && !technicalMessages.length && (
              <div className="chat-empty">
                <strong>直接告诉 Codex 你要处理什么</strong>
                <p>任务运行中也可以继续发送补充要求；系统会自动引导当前任务或排入下一轮。</p>
              </div>
            )}
            {technicalMessages.length ? (
              <details className="chat-technical">
                <summary>技术记录（{technicalMessages.length}）</summary>
                {technicalMessages.map((item, index) => (
                  <pre key={item.id || `${item.kind}-${item.created_at}-${index}`}>
                    {item.content}
                  </pre>
                ))}
              </details>
            ) : null}
          </div>
          <form className="chat-compose" onSubmit={onSubmit}>
            <div className="chat-compose__inner">
              {detail?.session.status === "running" ? (
                <p className="chat-compose-note">
                  Codex 正在处理。现在发送会作为补充要求送入当前任务。
                </p>
              ) : null}
              <label htmlFor="chat-compose-input">发送消息</label>
              <textarea
                id="chat-compose-input"
                value={input}
                onChange={(event) => onInputChange(event.target.value)}
                placeholder="描述你要调整的内容或补充要求"
                rows={3}
              />
              <button disabled={!input.trim() || sending || !detail}>
                {sending ? "发送中" : "发送"}
              </button>
            </div>
          </form>
        </div>
      </section>
    </div>
  );
}
