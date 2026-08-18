import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { TaskModelOverride } from "../taskModel";
import type { IdeaCandidate, IdeaSession, IdeaSessionDetail, Project, Task } from "../types";

function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

type IdeaPlannerOptions = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  account: string;
  accountIDs: string[];
  setMessage: (message: string) => void;
  onProjectCreated: (project: Project, topicCardTaskError?: string) => void;
};

export function useIdeaPlanner({
  api,
  account,
  accountIDs,
  setMessage,
  onProjectCreated,
}: IdeaPlannerOptions) {
  const [open, setOpen] = useState(false);
  const [session, setSession] = useState<IdeaSession | null>(null);
  const [sessions, setSessions] = useState<IdeaSession[]>([]);
  const [draft, setDraft] = useState(false);
  const [task, setTask] = useState<Task | null>(null);
  const [input, setInput] = useState("");
  const [creatingProject, setCreatingProject] = useState("");
  const [refreshRevision, setRefreshRevision] = useState(0);
  const [taskModel, setTaskModel] = useState<TaskModelOverride>({
    model: "",
    reasoningEffort: "",
  });
  const sessionIDRef = useRef("");
  const generationRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  const activeSessionID = draft ? undefined : session?.id;

  useEffect(() => {
    if (!open || !activeSessionID) return;
    const sessionID = activeSessionID;
    sessionIDRef.current = sessionID;
    const generation = ++generationRef.current;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    let timer: number | undefined;
    let stopped = false;
    const refresh = async () => {
      try {
        const response = await api(`/api/ideas/${sessionID}`, { signal: controller.signal });
        if (!response.ok) return;
        const next = (await response.json()) as IdeaSessionDetail;
        if (
          stopped ||
          generation !== generationRef.current ||
          sessionIDRef.current !== sessionID ||
          next.session.id !== sessionID
        ) return;
        const messages = next.messages || [];
        setSession({ ...next.session, messages, candidates: next.candidates || [] });
        const taskID = [...messages].reverse().find((item) => item.task_id)?.task_id;
        if (!taskID) {
          setTask(null);
          return;
        }
        const taskResponse = await api(`/api/tasks/${taskID}`, { signal: controller.signal });
        if (
          taskResponse.ok &&
          !stopped &&
          generation === generationRef.current &&
          sessionIDRef.current === sessionID
        ) setTask((await taskResponse.json()) as Task);
      } catch (error) {
        if (!isAbortError(error)) {
          // Keep the last stable conversation visible; the next poll retries.
        }
      } finally {
        if (!stopped && !controller.signal.aborted)
          timer = window.setTimeout(() => void refresh(), 3500);
      }
    };
    void refresh();
    return () => {
      stopped = true;
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [api, open, activeSessionID, refreshRevision]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const createConversation = () => {
    sessionIDRef.current = "";
    generationRef.current += 1;
    abortRef.current?.abort();
    const planningAccount = account || accountIDs[0] || undefined;
    setDraft(true);
    setSession({
      id: "draft",
      account_id: planningAccount,
      title: "新选题规划",
      status: "planning",
      messages: [],
      candidates: [],
    });
    setTask(null);
    setInput("");
    setOpen(true);
  };

  const refresh = async (id: string) => {
    sessionIDRef.current = id;
    const generation = ++generationRef.current;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const response = await api(`/api/ideas/${id}`, { signal: controller.signal });
      if (!response.ok) return;
      const next = (await response.json()) as IdeaSessionDetail;
      if (
        controller.signal.aborted ||
        generation !== generationRef.current ||
        sessionIDRef.current !== id ||
        next.session.id !== id
      ) return;
      const messages = next.messages || [];
      setSession({ ...next.session, messages, candidates: next.candidates || [] });
      setDraft(false);
      setSessions((current) =>
        current.map((item) => (item.id === next.session.id ? next.session : item)),
      );
      const taskID = [...messages].reverse().find((message) => message.task_id)?.task_id;
      if (!taskID) {
        setTask(null);
        setRefreshRevision((value) => value + 1);
        return;
      }
      const taskResponse = await api(`/api/tasks/${taskID}`, { signal: controller.signal });
      if (
        taskResponse.ok &&
        generation === generationRef.current &&
        sessionIDRef.current === id
      ) setTask((await taskResponse.json()) as Task);
      if (generation === generationRef.current && sessionIDRef.current === id)
        setRefreshRevision((value) => value + 1);
    } catch (error) {
      if (!isAbortError(error)) setMessage("选题对话暂时无法刷新，请稍后重试。");
    }
  };

  const openPlanner = async () => {
    const sessionsResponse = await api("/api/ideas");
    if (sessionsResponse.ok) {
      const payload = await sessionsResponse.json();
      const allSessions = Array.isArray(payload) ? (payload as IdeaSession[]) : [];
      const planningAccount = account || accountIDs[0] || "";
      const accountSessions = planningAccount
        ? allSessions.filter((item) => item.account_id === planningAccount)
        : allSessions;
      setSessions(accountSessions);
      const existing =
        accountSessions.find((item) => item.id === session?.id) || accountSessions[0];
      if (existing) {
        setDraft(false);
        setSession(existing);
        setOpen(true);
        await refresh(existing.id);
        return;
      }
    }
    createConversation();
  };

  const switchConversation = async (next: IdeaSession) => {
    sessionIDRef.current = next.id;
    setDraft(false);
    setSession(next);
    setTask(null);
    setInput("");
    await refresh(next.id);
  };

  const sendMessage = async (event: FormEvent) => {
    event.preventDefault();
    if (!session || !input.trim()) return;
    const sessionSnapshot = session;
    const content = input.trim();
    setInput("");
    let sessionID = sessionSnapshot.id;
    let createdSessionID = "";
    if (draft) {
      const createResponse = await api("/api/ideas", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          account_id: sessionSnapshot.account_id || account || undefined,
          title: sessionSnapshot.title,
        }),
      });
      if (!createResponse.ok) {
        setMessage("选题会话创建失败");
        setInput(content);
        return;
      }
      const created = (await createResponse.json()) as IdeaSession;
      sessionID = created.id;
      sessionIDRef.current = created.id;
      createdSessionID = created.id;
      setDraft(false);
      setSession({ ...created, messages: [], candidates: [] });
      setSessions((current) => [created, ...current]);
    }
    const response = await api(`/api/ideas/${sessionID}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        content,
        account_id: sessionSnapshot.account_id || account || undefined,
        ...(taskModel.model.trim() ? { model: taskModel.model.trim() } : {}),
        ...(taskModel.reasoningEffort ? { reasoning_effort: taskModel.reasoningEffort } : {}),
      }),
    });
    if (!response.ok) {
      if (sessionIDRef.current !== sessionID) return;
      let errorMessage = "选题消息发送失败";
      try {
        const payload = (await response.json()) as { code?: string; message?: string };
        if (payload.message) {
          errorMessage = `${errorMessage}：${payload.message}`;
        } else if (payload.code) {
          errorMessage = `${errorMessage}（${payload.code}）`;
        }
      } catch {
        // Keep the generic message when the server did not return JSON.
      }
      if (createdSessionID) {
        await api(`/api/ideas/${createdSessionID}`, { method: "DELETE" });
        setDraft(true);
        setSession({ ...sessionSnapshot, id: "draft", messages: [], candidates: [] });
        setSessions((current) => current.filter((item) => item.id !== createdSessionID));
      }
      setMessage(errorMessage);
      setInput(content);
      return;
    }
    setTaskModel({ model: "", reasoningEffort: "" });
    if (sessionIDRef.current === sessionID) await refresh(sessionID);
  };

  const deleteConversation = async (target: IdeaSession) => {
    if (target.id === "draft") {
      createConversation();
      return;
    }
    if (!window.confirm(`确定删除“${target.title}”吗？删除后无法恢复。`)) return;
    const response = await api(`/api/ideas/${target.id}`, { method: "DELETE" });
    if (!response.ok) {
      setMessage(
        response.status === 409 ? "该对话仍有运行中的任务，暂时不能删除" : "对话删除失败",
      );
      return;
    }
    const remaining = sessions.filter((item) => item.id !== target.id);
    setSessions(remaining);
    if (session?.id !== target.id) return;
    if (remaining.length) {
      await switchConversation(remaining[0]);
    } else {
      createConversation();
    }
  };

  const selectCandidate = async (candidate: IdeaCandidate) => {
    if (!session || creatingProject) return;
    const sessionID = session.id;
    setCreatingProject(candidate.id);
    try {
      const response = await api(`/api/ideas/${sessionID}/select`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          candidate_id: candidate.id,
          // The current sidebar account is the user's confirmation-time
          // choice and must override the account remembered by the planner.
          account_id: session.account_id || account || undefined,
        }),
      });
      if (!response.ok) {
        let reason = "候选题确认失败";
        try {
          const payload = (await response.json()) as { message?: string };
          if (payload.message) reason = payload.message;
        } catch {
          // Keep the concise fallback.
        }
        setMessage(reason);
        return;
      }
      const result = (await response.json()) as {
        project?: Project;
        topic_card_task_error?: string;
      };
      if (sessionIDRef.current !== sessionID) return;
      if (result.project) {
        setOpen(false);
        onProjectCreated(result.project, result.topic_card_task_error);
      } else await refresh(sessionID);
    } finally {
      setCreatingProject("");
    }
  };

  return {
    open,
    setOpen,
    session,
    setSession,
    sessions,
    draft,
    task,
    input,
    setInput,
    creatingProject,
    taskModel,
    setTaskModel,
    openPlanner,
    createConversation,
    switchConversation,
    deleteConversation,
    selectCandidate,
    sendMessage,
  };
}
