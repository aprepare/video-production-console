import { X } from "lucide-react";
import type { DirectoryManifest, Task } from "../types";
import {
  cancellableTaskStatuses,
  derivedMontagePhase,
  formatDuration,
  formatSize,
  montageHeadline,
  montagePhaseLabels,
  phaseDurationMS,
  phaseStringValue,
  statusLabels,
  taskElapsedMS,
  taskMessageContent,
  taskPhaseStateLabels,
  taskQuestions,
  taskTimeline,
  taskTimingPhases,
  taskTitle,
  timingValue,
} from "./task-view";

type TaskDetailDialogProps = {
  task: Task;
  projectTitle: string;
  timingNow: number;
  directoryManifest: DirectoryManifest | null;
  directoryManifestStatus: string;
  canOpenDirectory: boolean;
  openingDirectory: boolean;
  answerInput: string;
  onAnswerInputChange: (value: string) => void;
  onClose: () => void;
  onCancelTask: (task: Task) => void;
  onRetryRegistration: (task: Task) => void;
  onOpenDirectory: (assetID: string) => void;
  onAnswer: (task: Task, answer: string) => void;
};

export function TaskDetailDialog({
  task,
  projectTitle,
  timingNow,
  directoryManifest,
  directoryManifestStatus,
  canOpenDirectory,
  openingDirectory,
  answerInput,
  onAnswerInputChange,
  onClose,
  onCancelTask,
  onRetryRegistration,
  onOpenDirectory,
  onAnswer,
}: TaskDetailDialogProps) {
  const montagePhase = task.montage ? derivedMontagePhase(task) : "";
  const elapsed = taskElapsedMS(task, timingNow);
  const questions = taskQuestions(task);
  const timeline = taskTimeline(task);

  return (
    <div className="modal-backdrop">
      <section
        className="preview-modal task-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="task-dialog-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">Codex 任务详情</span>
            <h2 id="task-dialog-title">{taskTitle(task)}</h2>
            {projectTitle ? <p className="task-project-context">当前项目：{projectTitle}</p> : null}
          </div>
          <button className="close" aria-label="关闭任务详情" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        <div className="task-modal-meta">
          <span className={`task-status status-${task.status}`}>
            {statusLabels[task.status] || task.status}
          </span>
          <span>{task.id}</span>
          {(task.model || task.reasoning_effort) && (
            <span>{[task.model, task.reasoning_effort].filter(Boolean).join(" · ")}</span>
          )}
          {typeof elapsed === "number" ? (
            <span aria-label="任务总耗时">总耗时 {formatDuration(elapsed)}</span>
          ) : null}
          {cancellableTaskStatuses.has(task.status) && (
            <button className="secondary" onClick={() => onCancelTask(task)}>
              停止任务
            </button>
          )}
        </div>
        {task.action === "montage.execute" && task.montage ? (
          <section className={`montage-result phase-${montagePhase}`}>
            <div className="montage-result-head">
              <div>
                <span>剪映草稿登记</span>
                <h3>{montageHeadline(task.montage, montagePhase)}</h3>
              </div>
              <span className="montage-phase-badge">
                {montagePhaseLabels[montagePhase] || "正在准备混剪草稿"}
              </span>
            </div>
            {task.montage.workspace ? (
              <p className="montage-retained">
                明文产物已保留：{task.montage.workspace.filename || "未命名草稿"}。登记失败时不会重新生成或删除。
              </p>
            ) : null}
            {task.montage.registration_attempts?.[0]?.error_message ? (
              <p className="warning">{task.montage.registration_attempts?.[0]?.error_message}</p>
            ) : null}
            {task.montage.registered_asset ? (
              <div className="registered-directory">
                <details className="registered-directory-technical">
                  <summary>路径与文件清单</summary>
                  <dl>
                    <dt>正式项目资产</dt>
                    <dd>{task.montage.registered_asset.filename || "未命名草稿"}</dd>
                    <dt>剪映路径</dt>
                    <dd>
                      {directoryManifest?.registered_path || task.montage.registered_asset.path}
                    </dd>
                  </dl>
                  {directoryManifest ? (
                    <div className="directory-manifest">
                      <p>目录文件清单（{directoryManifest.entries.length} 项）</p>
                      <ul>
                        {directoryManifest.entries.slice(0, 80).map((entry) => (
                          <li key={`${entry.kind}-${entry.path}`}>
                            <span>{entry.path}</span>
                            <small>
                              {entry.kind === "directory" ? "文件夹" : formatSize(entry.size)}
                            </small>
                          </li>
                        ))}
                      </ul>
                      {directoryManifest.entries.length > 80 ? (
                        <p>
                          清单较长，这里只展示前 80 项；目录共 {directoryManifest.entries.length} 项。
                        </p>
                      ) : null}
                    </div>
                  ) : null}
                </details>
                {canOpenDirectory ? (
                  <button
                    type="button"
                    disabled={openingDirectory}
                    onClick={() => onOpenDirectory(task.montage!.registered_asset!.id)}
                  >
                    {openingDirectory ? "正在打开…" : "在电脑上打开剪映目录"}
                  </button>
                ) : null}
                {directoryManifestStatus ? (
                  <p className="directory-status" aria-live="polite">
                    {directoryManifestStatus}
                  </p>
                ) : null}
              </div>
            ) : null}
            {task.montage.can_retry_registration ? (
              <button onClick={() => onRetryRegistration(task)}>只重试剪映登记</button>
            ) : null}
          </section>
        ) : null}
        {task.timing_summary || task.timing_runs?.length || typeof elapsed === "number" ? (
          <section className="task-timing" aria-label="任务阶段耗时">
            <h3>阶段耗时</h3>
            {task.timing_summary ? (
              <p className="task-timing-summary">
                总计 {formatDuration(timingValue(task.timing_summary, "total_ms") || elapsed)} · 准备 {formatDuration(timingValue(task.timing_summary, "preparation_ms"))} · 队列 {formatDuration(timingValue(task.timing_summary, "queue_ms"))}{task.timing_summary.queue_estimated ? "（边界估算）" : ""} · 执行 {formatDuration(timingValue(task.timing_summary, "execution_ms"))}
              </p>
            ) : (
              <p className="task-timing-summary">总计 {formatDuration(elapsed)}</p>
            )}
            <ul className="task-timing-list">
              {taskTimingPhases(task).map((phase, index) => {
                const phaseID = phaseStringValue(phase, "id");
                const phaseKey = phaseStringValue(phase, "phase_key");
                const state = phaseStringValue(phase, "state");
                const duration = phaseDurationMS(phase, timingNow);
                return (
                  <li key={phaseID || `${phaseKey}-${index}`}>
                    <strong>
                      {phaseStringValue(phase, "display_name") || phaseKey || "未命名阶段"}
                    </strong>
                    <span>{taskPhaseStateLabels[state] || state || "暂无状态"}</span>
                    <time aria-label={state === "running" ? "运行时长" : "阶段耗时"}>
                      {formatDuration(duration)}
                    </time>
                  </li>
                );
              })}
            </ul>
            {task.timing_summary?.legacy_without_phases ? (
              <p className="muted">
                该任务没有已持久化的阶段运行记录，不能据此判定阶段是否开始。
              </p>
            ) : null}
          </section>
        ) : null}
        {task.prompt_snapshot && (
          <details className="technical-diagnostics">
            <summary>任务原始说明</summary>
            <pre className="asset-text">{task.prompt_snapshot}</pre>
          </details>
        )}
        {task.messages?.length ? (
          <section>
            <h3>对话记录</h3>
            {task.messages.map((item, index) => (
              <div
                className={`task-message ${item.role}`}
                key={item.id || `${item.role}-${item.created_at}-${index}`}
              >
                <b>{item.role === "user" ? "你" : "Codex"}</b>
                <p>{taskMessageContent(item.content)}</p>
              </div>
            ))}
          </section>
        ) : null}
        {timeline.length ? (
          <section>
            <h3>处理进度</h3>
            <ol className="task-timeline task-timeline-expanded">
              {timeline.map((item, index) => (
                <li key={`${task.id}-modal-progress-${index}`}>{item}</li>
              ))}
            </ol>
          </section>
        ) : null}
        {task.events?.length ? (
          <details className="technical-diagnostics">
            <summary>技术诊断（{task.events.length}）</summary>
            {task.events.map((item, index) => (
              <p className="event" key={item.id || `${item.sequence || 0}-${index}`}>
                {item.display_text || "技术事件"}
              </p>
            ))}
          </details>
        ) : null}
        {task.result_summary && (
          <section className="task-result-summary">
            <h3>完成结果</h3>
            <p>{task.result_summary}</p>
            {task.completion_phase ? <small>结果阶段：{task.completion_phase}</small> : null}
          </section>
        )}
        {task.error_message && <p className="warning">{task.error_message}</p>}
        {questions.length > 0 && (
          <section className="task-question">
            <strong>Codex 正在问：</strong>
            {questions.map((question, index) => (
              <p key={`${task.id}-modal-question-${index}`}>{question}</p>
            ))}
            <form
              className="task-answer-compose"
              onSubmit={(event) => {
                event.preventDefault();
                if (answerInput.trim()) onAnswer(task, answerInput.trim());
              }}
            >
              <label htmlFor="task-answer-input">回答 Codex</label>
              <textarea
                id="task-answer-input"
                rows={3}
                value={answerInput}
                onChange={(event) => onAnswerInputChange(event.target.value)}
                placeholder="在这里回答，Codex 会从当前任务继续"
              />
              <button disabled={!answerInput.trim()}>发送回答</button>
            </form>
          </section>
        )}
      </section>
    </div>
  );
}
