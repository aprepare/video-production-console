import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { ChatDetail, ChatMessage, ChatSession, HistoryThread } from "../types";

function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

function isTechnicalChatMessage(message: ChatMessage) {
  return ["event", "tool", "technical", "protocol"].includes(message.kind);
}

type ChatWorkbenchOptions = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  historyLimit: number;
  setMessage: (message: string) => void;
};

export function useChatWorkbench({ api, historyLimit, setMessage }: ChatWorkbenchOptions) {
  const [open, setOpen] = useState(false);
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [creating, setCreating] = useState(false);
  const [creationSource, setCreationSource] = useState<"console" | "desktop">("console");
  const [detail, setDetail] = useState<ChatDetail | null>(null);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [refreshRevision, setRefreshRevision] = useState(0);
  const [historyThreads, setHistoryThreads] = useState<HistoryThread[]>([]);
  const [historySource, setHistorySource] = useState("");
  const sessionIDRef = useRef("");
  const generationRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  const activeSessionID = detail?.session.id;

  useEffect(() => {
    if (!open || !activeSessionID) return;
    const sessionID = activeSessionID;
    sessionIDRef.current = sessionID;
    const generation = ++generationRef.current;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    let stopped = false;
    let timer: number | undefined;
    const refresh = async () => {
      try {
        const response = await api(`/api/chat/sessions/${sessionID}`, {
          signal: controller.signal,
        });
        if (!response.ok) return;
        const next = (await response.json()) as ChatDetail;
        if (
          !stopped &&
          generation === generationRef.current &&
          sessionIDRef.current === sessionID &&
          next.session.id === sessionID
        ) setDetail(next);
      } catch (error) {
        if (!isAbortError(error)) {
          // Keep the last stable messages; the next poll retries.
        }
      } finally {
        if (!stopped && !controller.signal.aborted)
          timer = window.setTimeout(() => void refresh(), 2500);
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

  const loadSession = async (session: ChatSession) => {
    const sessionID = session.id;
    sessionIDRef.current = sessionID;
    const generation = ++generationRef.current;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const response = await api(`/api/chat/sessions/${sessionID}`, { signal: controller.signal });
      if (!response.ok) {
        setMessage("实时 Codex 对话服务暂时不可用，请检查设置页状态。");
        return;
      }
      const next = (await response.json()) as ChatDetail;
      if (
        !controller.signal.aborted &&
        generation === generationRef.current &&
        sessionIDRef.current === sessionID &&
        next.session.id === sessionID
      ) {
        setDetail(next);
        setRefreshRevision((value) => value + 1);
      }
    } catch (error) {
      if (!isAbortError(error) && sessionIDRef.current === sessionID)
        setMessage("对话暂时离线，已保留当前消息。");
    }
  };

  const createSession = async (source: "console" | "desktop" = creationSource) => {
    if (creating) return;
    setCreating(true);
    setMessage("正在创建 Codex 对话，请稍候……");
    try {
      const response = await api("/api/chat/sessions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          kind: "general",
          title: `${source === "desktop" ? "桌面版" : "控制台"}对话 ${new Date().toLocaleDateString("zh-CN")}`,
          source,
          skill_names: [],
        }),
      });
      if (!response.ok) {
        const failure = (await response.json().catch(() => null)) as
          | { code?: string; message?: string }
          | null;
        if (response.status === 404) {
          setMessage("实时 Codex 对话服务尚未在本次启动中加载。请重启控制台后再试。");
        } else if (failure?.message) {
          setMessage(`新建 Codex 对话失败：${failure.message}`);
        } else {
          setMessage("新建 Codex 对话失败，请稍后重试。");
        }
        return;
      }
      const created = (await response.json()) as ChatSession;
      setSessions((current) => [created, ...current]);
      await loadSession(created);
      setMessage(
        `${source === "desktop" ? "桌面版" : "控制台"} Codex 对话已创建，可以开始发送消息。`,
      );
    } catch {
      setMessage("新建 Codex 对话失败：控制台暂时无法连接 Codex 服务。");
    } finally {
      setCreating(false);
    }
  };

  const openWorkbench = async () => {
    setOpen(true);
    const response = await api("/api/chat/sessions");
    if (!response.ok) {
      setMessage("Codex 对话服务尚未启用。");
      return;
    }
    const nextSessions = (await response.json()) as ChatSession[];
    setSessions(nextSessions);
    const historyResponse = await api(`/api/codex/history?limit=${historyLimit}`);
    if (historyResponse.ok) setHistoryThreads((await historyResponse.json()) as HistoryThread[]);
    const active = nextSessions.find((item) => item.id === detail?.session.id) || nextSessions[0];
    if (active) await loadSession(active);
    else await createSession();
  };

  const refreshHistory = async (source = historySource) => {
    const suffix = source ? `&source=${encodeURIComponent(source)}` : "";
    const response = await api(`/api/codex/history?limit=${historyLimit}${suffix}`);
    if (response.ok) setHistoryThreads((await response.json()) as HistoryThread[]);
  };

  const applyHistoryThread = async (thread: HistoryThread, mode: "resume" | "fork") => {
    const response = await api(
      `/api/codex/history/${encodeURIComponent(thread.id)}/${mode}`,
      { method: "POST" },
    );
    if (!response.ok) {
      setMessage(
        response.status === 409
          ? "原会话正在桌面端运行，请选择“复制到控制台”。"
          : "历史对话接入失败，请检查实时 Codex 对话服务状态。",
      );
      return;
    }
    const session = (await response.json()) as ChatSession;
    setSessions((current) => [session, ...current.filter((item) => item.id !== session.id)]);
    await loadSession(session);
    await refreshHistory();
  };

  const sendMessage = async (event: FormEvent) => {
    event.preventDefault();
    if (!detail || !input.trim() || sending) return;
    const sessionID = sessionIDRef.current || detail.session.id;
    if (sessionID !== detail.session.id) return;
    const sessionSnapshot = detail.session;
    const content = input.trim();
    setInput("");
    setSending(true);
    try {
      const response = await api(`/api/chat/sessions/${sessionID}/messages`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          text: content,
          delivery: "auto",
          client_key: globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`,
        }),
      });
      if (!response.ok) {
        setMessage("消息发送失败，请查看实时 Codex 对话服务状态。");
        setInput(content);
        return;
      }
      if (sessionIDRef.current === sessionID) await loadSession(sessionSnapshot);
    } catch (error) {
      if (!isAbortError(error) && sessionIDRef.current === sessionID) {
        setMessage("消息发送失败，已保留输入内容。");
        setInput(content);
      }
    } finally {
      setSending(false);
    }
  };

  const deleteSession = async (session: ChatSession) => {
    if (
      !window.confirm(
        `删除对话“${session.title}”吗？这只删除控制台映射，不会终止正在运行的 Codex。`,
      )
    ) return;
    const response = await api(`/api/chat/sessions/${session.id}`, { method: "DELETE" });
    if (!response.ok) {
      setMessage("删除对话失败。");
      return;
    }
    const remaining = sessions.filter((item) => item.id !== session.id);
    setSessions(remaining);
    if (sessionIDRef.current !== session.id) return;
    if (remaining.length) await loadSession(remaining[0]);
    else {
      sessionIDRef.current = "";
      generationRef.current += 1;
      abortRef.current?.abort();
      setDetail(null);
    }
  };

  const visibleMessages = useMemo(
    () => detail?.messages.filter((item) => !isTechnicalChatMessage(item)) || [],
    [detail],
  );
  const technicalMessages = useMemo(
    () => detail?.messages.filter(isTechnicalChatMessage) || [],
    [detail],
  );

  return {
    open,
    setOpen,
    sessions,
    creating,
    creationSource,
    setCreationSource,
    detail,
    input,
    setInput,
    sending,
    historyThreads,
    historySource,
    setHistorySource,
    visibleMessages,
    technicalMessages,
    openWorkbench,
    createSession,
    loadSession,
    deleteSession,
    refreshHistory,
    applyHistoryThread,
    sendMessage,
  };
}
