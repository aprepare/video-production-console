// Presentation logic for Codex tasks, shared by the shell and the task dialog.
import type { MontageResult } from "../project-workbench/types";
import type { Task, TaskPhaseRun, TaskTimingSummary } from "../types";
import type { SemanticEvent, TaskEvent } from "./event-types";

export const liveTaskStatuses = new Set([
  "queued",
  "running",
  "awaiting_input",
  "resuming",
  "waiting_input",
]);

export const cancellableTaskStatuses = new Set([
  "queued",
  "running",
  "resuming",
  "awaiting_input",
  "waiting_input",
]);

export const statusLabels: Record<string, string> = {
  queued: "排队中",
  running: "运行中",
  awaiting_input: "等待回复",
  resuming: "恢复中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  interrupted: "已中断",
  waiting_input: "等待回复",
  cancelled: "已取消",
};

export const taskPhaseStateLabels: Record<string, string> = {
  queued: "排队中",
  running: "运行中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  cancelled: "已取消",
  interrupted: "已中断",
};

export function taskModelLabel(task: { type?: string; model?: string; reasoning_effort?: string }) {
  if (task.type === "remix") return task.model || "";
  return [task.model, task.reasoning_effort].filter(Boolean).join(" · ");
}

export const taskActionLabels: Record<string, string> = {
  "topic.brainstorm": "选题分析",
  "topic.commit": "保存选题卡",
  "topic.deepen": "深化选题",
  "remix.standard": "二创文案",
  "remix.enhanced": "增强二创文案",
  "remix.from_topic_card": "根据选题写文案",
  "remix.review": "文案检查",
  "montage.plan": "混剪方案",
  "montage.execute": "混剪草稿",
};

export const montagePhaseLabels: Record<string, string> = {
  agent_running: "正在生成明文草稿",
  plaintext: "明文草稿已生成",
  registering: "正在登记到剪映",
  failed: "登记失败",
  interrupted: "登记已中断",
  registered: "已登记为正式资产",
};

function latestRegistrationState(montage: MontageResult) {
  return [...(montage.registration_attempts || [])]
    .sort((left, right) => right.attempt - left.attempt)[0]?.state?.toLowerCase();
}

export function derivedMontagePhase(task: Task) {
  if (!task.montage) return task.completion_phase || "agent_running";
  const attemptState = latestRegistrationState(task.montage);
  if (attemptState === "failed" || attemptState === "interrupted") return attemptState;
  if (attemptState === "succeeded") return "registered";
  if (attemptState === "queued" || attemptState === "running") return "registering";
  const completion = task.completion_phase || task.montage.phase;
  if (completion === "plaintext_ready") return "plaintext";
  if (["agent_running", "registering", "registered"].includes(completion)) return completion;
  return completion || "agent_running";
}

export function montageHeadline(montage: MontageResult, phase: string) {
  switch (phase) {
    case "agent_running":
      return "Codex 正在生成可登记的明文草稿";
    case "registered":
      return "草稿已登记，可以在剪映中继续编辑";
    case "registering":
      return "明文草稿已完成，正在登记剪映";
    case "failed":
      return "草稿已生成，但登记剪映失败";
    case "interrupted":
      return "草稿已保留，登记过程被中断";
    case "plaintext":
      return "明文草稿已完成，等待登记剪映";
    default:
      return montage.can_retry_registration
        ? "草稿已生成，可以只重试剪映登记"
        : "正在生成可登记的明文草稿";
  }
}

export function taskTitle(task: Task) {
  if (task.action === "montage.execute" && task.skill_name === "jianying-movie-montage") {
    return "电影混剪草稿";
  }
  return taskActionLabels[task.action || ""] || task.skill_name || task.type || "Codex 任务";
}

export function taskMessageContent(content: string) {
  const value = content.trim();
  if (value === "Plaintext montage draft completed and validated.") {
    return "明文混剪草稿已生成并校验完成，等待登记到剪映。";
  }
  if (!value.startsWith("{")) return content;
  try {
    const envelope = JSON.parse(value) as Record<string, unknown>;
    if (envelope.schema_version !== "2.0" || typeof envelope.action !== "string") return content;
    const action = envelope.action;
    const status = envelope.status;
    if (status === "failed") return "任务没有完成，请查看下方失败原因。";
    if (status === "awaiting_input") return "Codex 需要你补充信息，请在下方回复。";
    if (action === "montage.execute") return "明文混剪草稿已生成并校验完成，尚未登记到剪映。";
    if (action === "topic.brainstorm") return "候选选题已生成，可以选择一个继续深化。";
    if (action === "topic.commit" || action === "topic.deepen")
      return "选题卡已处理完成，可在项目素材中查看。";
    if (action.startsWith("remix.")) return "文案结果已生成，可在项目素材中查看。";
    return typeof envelope.summary === "string" && envelope.summary.trim()
      ? envelope.summary
      : "任务已处理完成。";
  } catch {
    return content;
  }
}

export function phaseDisplayName(phaseKey: string, displayName: string, action = "") {
  if (phaseKey === "codex_execution" || displayName === "Codex 执行") return "模型执行";
  if (
    action.startsWith("remix.") &&
    (phaseKey === "web_research" || displayName === "联网研究")
  ) {
    return "模型执行";
  }
  return displayName || phaseKey || "未命名阶段";
}

export function remixProgressText(text: string) {
  if (/Grok 联网|爆款库|选题/.test(text)) return "正在写二创文案";
  return text;
}

export function taskEventProgress(event: TaskEvent, action = "") {
  const kind = event.kind || "";
  const displayText = event.display_text || "";
  const raw = event.raw_json || "";
  if (action.startsWith("remix.")) {
    if (displayText) return remixProgressText(taskMessageContent(displayText));
    return "正在写二创文案";
  }
  if (displayText) return taskMessageContent(displayText);
  if (/baokuan_search_materials/i.test(raw)) return "正在检索爆款库素材";
  if (/baokuan_list_snippets/i.test(raw)) return "正在筛选可借鉴的爆款片段";
  if (
    /thread\.started|turn\.started/i.test(kind) ||
    /thread\.started|turn\.started/i.test(raw)
  )
    return "模型已启动，正在写二创文案";
  if (/turn\.completed/i.test(kind) || /turn\.completed/i.test(raw))
    return "正在整理候选选题";
  if (/error|failed/i.test(kind) || /error|failed/i.test(raw))
    return "任务遇到问题，正在等待处理";
  if (/item\.started/i.test(kind) || /item\.started/i.test(raw))
    return "正在分析素材与选题方向";
  return "正在推进选题分析";
}

export function taskProgressStatus(task: Task) {
  if (task.status === "failed") return task.error_message || "任务失败，请查看任务详情";
  if (task.status === "awaiting_input" || task.status === "waiting_input")
    return "正在等待你的回复";
  if (task.status === "completed") {
    if (task.action === "topic.brainstorm") return "候选选题已生成";
    if (task.action === "montage.execute") {
      return task.skill_name === "jianying-movie-montage" ? "电影混剪草稿已处理完成" : "混剪草稿已处理完成";
    }
    return "任务已完成";
  }
  return statusLabels[task.status] || task.status;
}

export function normalizeSemanticEvents(events: SemanticEvent[]) {
  return events.map((event) => ({
    id: event.id || "",
    sequence: event.sequence ?? 0,
    kind: event.kind || "",
    phase: event.phase || "",
    level: event.level || "",
    title: (event.title || "").trim(),
    detail: event.detail || "",
    created_at: event.created_at || "",
  }));
}

export function taskTimeline(task: Task): string[] {
  const semantic = normalizeSemanticEvents(task.semantic_events || [])
    .filter((event) => event.title)
    .sort((left, right) => left.sequence - right.sequence);
  const messages = semantic.length
    ? semantic.map((event) =>
        (task.action || "").startsWith("remix.")
          ? remixProgressText(event.title)
          : event.title,
      )
    : (task.events || []).map((event) => taskEventProgress(event, task.action));
  const unique = messages.filter(
    (message, index) => message && messages.indexOf(message) === index,
  );
  if (task.status === "failed" && task.error_message) {
    const withoutGenericFailure = unique.filter(
      (message) => message !== "任务遇到问题，正在等待处理",
    );
    if (!withoutGenericFailure.includes(task.error_message)) {
      withoutGenericFailure.push(task.error_message);
    }
    return withoutGenericFailure.slice(-4);
  }
  return unique.slice(-4);
}

export function taskQuestions(task: Task): string[] {
  const message = [...(task.messages || [])]
    .reverse()
    .find((item) => item.role === "assistant" && item.question_schema);
  if (!message?.question_schema) return [];
  try {
    const parsed = JSON.parse(message.question_schema) as Array<string | { text?: string }>;
    return parsed
      .map((question) => (typeof question === "string" ? question : question.text || ""))
      .filter(Boolean);
  } catch {
    return [];
  }
}

type TimingNumberKey = "total_ms" | "preparation_ms" | "queue_ms" | "execution_ms";
type PhaseStringKey = "id" | "phase_key" | "display_name" | "state" | "started_at";

export function timingValue(summary: TaskTimingSummary | undefined, key: TimingNumberKey) {
  const value = summary?.[key];
  return typeof value === "number" ? value : 0;
}

export function phaseStringValue(phase: TaskPhaseRun, key: PhaseStringKey) {
  const value = phase[key];
  return typeof value === "string" ? value : "";
}

export function taskTimingPhases(task: Task) {
  if (task.timing_runs?.length) return task.timing_runs;
  return task.timing_summary?.phases || [];
}

export function isRunningPhase(phase: TaskPhaseRun) {
  return phaseStringValue(phase, "state") === "running";
}

export function phaseDurationMS(phase: TaskPhaseRun, now: number) {
  if (typeof phase.duration_ms === "number") return phase.duration_ms;
  if (!isRunningPhase(phase)) return undefined;
  const startedAt = Date.parse(phaseStringValue(phase, "started_at"));
  return Number.isFinite(startedAt) ? Math.max(0, now - startedAt) : undefined;
}

export function taskElapsedMS(task: Task, now: number) {
  const summaryTotal = timingValue(task.timing_summary, "total_ms");
  if (summaryTotal > 0) return summaryTotal;
  const finishedAt = Date.parse(task.finished_at || "");
  const startedAt = Date.parse(task.started_at || task.created_at || "");
  if (Number.isFinite(finishedAt) && Number.isFinite(startedAt) && finishedAt >= startedAt) {
    return finishedAt - startedAt;
  }
  if (
    Number.isFinite(startedAt)
    && ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(task.status)
  ) {
    return Math.max(0, now - startedAt);
  }
  return undefined;
}

export function formatDuration(ms?: number) {
  if (typeof ms !== "number") return "暂无";
  if (ms < 1000) return `${ms} ms`;
  const totalSeconds = ms / 1000;
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)} s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds - minutes * 60;
  if (minutes < 60) return `${minutes} 分 ${seconds.toFixed(0)} 秒`;
  const hours = Math.floor(minutes / 60);
  const remainMinutes = minutes % 60;
  return `${hours} 小时 ${remainMinutes} 分`;
}

export function formatSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}
