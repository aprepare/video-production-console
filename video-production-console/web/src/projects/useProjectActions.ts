import { useCallback, useRef, useState } from "react";
import type { FormEvent, RefObject } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../query/keys";
import type { RemixPromptStyle } from "../RemixPromptStyleFields";
import type { TaskModelOverride } from "../taskModel";
import type { Asset, NarrationGeneration, Project, ProjectDetail } from "../types";
import { unwrapImportedScript } from "./import-script";

function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

// Narration failures carry a ready-to-read Chinese message from the server;
// only a body the browser could not parse falls back to a generic line.
async function backendMessage(response: Response, fallback: string) {
  try {
    const payload = (await response.json()) as { message?: string };
    const message = payload.message?.trim();
    if (message) return message;
  } catch {
    /* ignore non-JSON bodies */
  }
  return fallback;
}

type ProjectActionsOptions = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  selected: Project | null;
  setSelected: (project: Project) => void;
  detail: ProjectDetail | null;
  refreshProject: (projectID: string) => Promise<void>;
  setProjects: (updater: (current: Project[]) => Project[]) => void;
  setMessage: (message: string) => void;
  selectedIDRef: RefObject<string>;
  newProjectTitle: string;
  selectedAccountID: string;
  onProjectCreated: () => void;
  onContinuousScriptSaved: (content: string) => void;
  onRemixReviewStarted: () => void;
  onProjectDeleted: () => void;
};

export function useProjectActions({
  api,
  selected,
  setSelected,
  detail,
  refreshProject,
  setProjects,
  setMessage,
  selectedIDRef,
  newProjectTitle,
  selectedAccountID,
  onProjectCreated,
  onContinuousScriptSaved,
  onRemixReviewStarted,
  onProjectDeleted,
}: ProjectActionsOptions) {
  const client = useQueryClient();
  const [pendingActions, setPendingActions] = useState<string[]>([]);
  const pendingActionsRef = useRef(new Set<string>());
  const [taskModel, setTaskModel] = useState<TaskModelOverride>({
    model: "",
    reasoningEffort: "",
  });
  const [remixPromptStyle, setRemixPromptStyle] = useState<RemixPromptStyle>("rewrite");

  const lockAction = useCallback((projectID: string, action: string) => {
    const key = `${projectID}:${action}`;
    if ([...pendingActionsRef.current].some((pending) => pending.startsWith(`${projectID}:`)))
      return "";
    pendingActionsRef.current.add(key);
    setPendingActions([...pendingActionsRef.current]);
    return key;
  }, []);
  const unlockAction = useCallback((key: string) => {
    pendingActionsRef.current.delete(key);
    setPendingActions([...pendingActionsRef.current]);
  }, []);

  const loadDetail = useCallback(
    (project: Project) => refreshProject(project.id),
    [refreshProject],
  );

  // Writes go through mutations so react-query owns the request and the cache
  // refresh that follows it; the per-project action lock stays because it encodes
  // a product rule (one action per project) rather than a fetch concern.
  // A rejected request and a rejecting server mean different things to the user,
  // so these resolve to `ok` for an HTTP failure and only reject when the request
  // itself could not be made. Callers keep their two distinct messages.
  const createProjectMutation = useMutation({
    mutationFn: async (input: { accountID: string; title: string }) => {
      const response = await api("/api/projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ account_id: input.accountID, title: input.title }),
      });
      return response.ok;
    },
    onSuccess: async (ok) => {
      if (!ok) return;
      onProjectCreated();
      await client.invalidateQueries({ queryKey: queryKeys.projects() });
    },
  });

  const startProjectTaskMutation = useMutation({
    mutationFn: async (input: { projectID: string; body: Record<string, unknown> }) => {
      const response = await api(`/api/projects/${input.projectID}/tasks`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input.body),
      });
      return response.ok;
    },
    onSuccess: (ok, input) => (ok ? refreshProject(input.projectID) : undefined),
  });

  // Saving a revision bumps the asset version and marks downstream assets stale,
  // so the refresh has to cover the project detail as well as the task list.
  const saveContinuousScriptMutation = useMutation({
    mutationFn: async (input: { projectID: string; content: string }) => {
      const body = new FormData();
      body.set(
        "file",
        new File([input.content], "continuous-script.txt", { type: "text/plain" }),
      );
      const response = await api(`/api/projects/${input.projectID}/assets/continuous_script`, {
        method: "POST",
        body,
      });
      return response.ok;
    },
    onSuccess: (ok, input) => (ok ? refreshProject(input.projectID) : undefined),
  });

  // One call produces both the narration audio and the SRT, so the refresh has to
  // land before the caller reports success on either card.
  const generateNarrationMutation = useMutation({
    mutationFn: async (input: { projectID: string }) => {
      const response = await api(`/api/projects/${input.projectID}/narration`, {
        method: "POST",
      });
      if (!response.ok)
        return {
          ok: false as const,
          message: await backendMessage(response, "配音与字幕生成失败，请稍后重试。"),
        };
      const result = (await response.json()) as NarrationGeneration;
      return { ok: true as const, result };
    },
    onSuccess: (outcome, input) => (outcome.ok ? refreshProject(input.projectID) : undefined),
  });

  const modelOverrideBody = (includeEffort = true) => ({
    ...(taskModel.model.trim() ? { model: taskModel.model.trim() } : {}),
    ...(includeEffort && taskModel.reasoningEffort ? { reasoning_effort: taskModel.reasoningEffort } : {}),
  });

  const createProject = async (event: FormEvent) => {
    event.preventDefault();
    if (!newProjectTitle.trim() || !selectedAccountID) return;
    const created = await createProjectMutation.mutateAsync({
      accountID: selectedAccountID,
      title: newProjectTitle.trim(),
    });
    if (!created) setMessage("项目创建失败。");
  };

  const loadSourceScriptContent = async (assetID: string): Promise<string> => {
    const response = await api(`/api/assets/${assetID}/content`);
    if (!response.ok) throw new Error("同行原文读取失败");
    return response.text();
  };

  const saveSourceScriptAndStartRemix = async (content: string) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockAction(projectID, "source-remix");
    if (!lockKey) return;
    try {
      let sourceVersionID = "";
      const savedSource = detail?.project.id === projectID ? detail.assets.source_script : undefined;
      if (savedSource?.state === "ready") {
        try {
          const existingContent = await loadSourceScriptContent(savedSource.id);
          if (selectedIDRef.current !== projectID) return;
          if (existingContent === content) sourceVersionID = savedSource.id;
        } catch {
          if (selectedIDRef.current !== projectID) return;
        }
      }
      if (!sourceVersionID) {
        const body = new FormData();
        body.set("file", new File([content], "source-script.txt", { type: "text/plain" }));
        const response = await api(`/api/projects/${projectID}/assets/source_script`, {
          method: "POST",
          body,
        });
        if (selectedIDRef.current !== projectID) return;
        if (!response.ok) {
          setMessage("同行原文保存失败，请稍后重试。");
          await loadDetail(project);
          return;
        }
        const asset = (await response.json()) as Asset;
        if (selectedIDRef.current !== projectID) return;
        sourceVersionID = asset.id;
      }
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "remix",
          action: "remix.standard",
          prompt: "基于当前项目保存的同行原文生成正式连续二创文案，并登记为项目资产。",
          source_version_id: sourceVersionID,
          remix_prompt_style: remixPromptStyle,
          ...modelOverrideBody(),
        },
      });
      if (selectedIDRef.current !== projectID) return;
      if (!started) {
        setMessage("原文已保存，但二创任务启动失败，请检查模型配置后重试。");
        await loadDetail(project);
        return;
      }
      setTaskModel({ model: "", reasoningEffort: "" });
      setMessage("同行原文已保存，正式二创任务已启动。完成后会自动出现在项目资产中。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("原文保存或二创任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const startMontageTask = async (prompt: string, options?: { remake?: boolean }) => {
    if (!selected) return;
    const project = selected;
    const projectDetail = detail;
    if (!projectDetail || projectDetail.project.id !== project.id) {
      setMessage("项目详情仍在刷新，请确认当前项目后再启动任务。");
      return;
    }
    const projectID = project.id;
    const lockKey = lockAction(projectID, "montage");
    if (!lockKey) return;
    try {
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "montage",
          prompt,
          ...modelOverrideBody(),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("混剪任务启动失败，请检查项目素材与 Codex 配置后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setTaskModel({ model: "", reasoningEffort: "" });
      if (options?.remake) {
        setMessage("已重新排队混剪，会生成新的剪映草稿；旧草稿不会被删。");
      }
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("混剪任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const startImageVideoTask = async (prompt: string, options?: { remake?: boolean }) => {
    if (!selected) return;
    const project = selected;
    const projectDetail = detail;
    if (!projectDetail || projectDetail.project.id !== project.id) {
      setMessage("项目详情仍在刷新，请确认当前项目后再启动任务。");
      return;
    }
    const projectID = project.id;
    const lockKey = lockAction(projectID, "montage");
    if (!lockKey) return;
    try {
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "image_video",
          prompt,
          ...modelOverrideBody(),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("图片视频任务启动失败，请检查静帧库与项目素材后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setTaskModel({ model: "", reasoningEffort: "" });
      if (options?.remake) {
        setMessage("已重新排队图片视频，会生成新的剪映草稿；旧草稿不会被删。");
      }
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("图片视频任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const startMovieMontageTask = async (prompt: string, options?: { remake?: boolean }) => {
    if (!selected) return;
    const project = selected;
    const projectDetail = detail;
    if (!projectDetail || projectDetail.project.id !== project.id) {
      setMessage("项目详情仍在刷新，请确认当前项目后再启动任务。");
      return;
    }
    const projectID = project.id;
    const lockKey = lockAction(projectID, "montage");
    if (!lockKey) return;
    try {
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "movie_montage",
          prompt,
          ...modelOverrideBody(),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("电影混剪任务启动失败，请检查电影切镜库与项目素材后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setTaskModel({ model: "", reasoningEffort: "" });
      if (options?.remake) {
        setMessage("已重新排队电影混剪，会生成新的剪映草稿；旧草稿不会被删。");
      }
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("电影混剪任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const publishProject = async () => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockAction(projectID, "publish");
    if (!lockKey) return;
    try {
      const response = await api(`/api/projects/${projectID}/publish`, { method: "POST" });
      if (!response.ok) {
        if (selectedIDRef.current === projectID) setMessage("发布状态更新失败，请稍后重试。");
        return;
      }
      const published = { ...project, stage: "published" as const };
      setProjects((current) => current.map((item) => (item.id === projectID ? published : item)));
      if (selectedIDRef.current !== projectID) return;
      setSelected(published);
      client.setQueryData<ProjectDetail>(queryKeys.project(projectID), (current) =>
        current && current.project.id === projectID
          ? { ...current, project: { ...current.project, stage: "published" } }
          : current,
      );
      setMessage("项目已标记为已发布。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("发布状态更新失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const saveContinuousScript = async (content: string) => {
    if (!selected) return;
    const unpacked = unwrapImportedScript(content);
    if (!unpacked.ok) {
      setMessage(unpacked.message);
      return;
    }
    if (!unpacked.script) {
      setMessage("请先粘贴成品文案。");
      return;
    }
    const project = selected;
    const projectID = project.id;
    const lockKey = lockAction(projectID, "save-continuous-script");
    if (!lockKey) return;
    try {
      const saved = await saveContinuousScriptMutation.mutateAsync({ projectID, content });
      if (!saved) {
        if (selectedIDRef.current === projectID) setMessage("连续文案保存失败，请稍后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      onContinuousScriptSaved(unpacked.script);
      setMessage("连续文案已保存为新版本；下游配音/字幕等可能已标记为失效。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("连续文案保存失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const importContinuousScript = async (content: string) => {
    if (!selected) return;
    const unpacked = unwrapImportedScript(content);
    if (!unpacked.ok) {
      setMessage(unpacked.message);
      return;
    }
    if (!unpacked.script) {
      setMessage("请先粘贴成品文案。");
      return;
    }
    const project = selected;
    const projectID = project.id;
    const lockKey = lockAction(projectID, "save-continuous-script");
    if (!lockKey) return;
    try {
      const saved = await saveContinuousScriptMutation.mutateAsync({ projectID, content });
      if (!saved) {
        if (selectedIDRef.current === projectID) setMessage("成品文案导入失败，请稍后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      onContinuousScriptSaved(unpacked.script);
      setMessage(unpacked.fromJSON && unpacked.hasPublishing
        ? "成品文案和发布标题已导入，已跳到配音步骤。可直接生成配音与字幕。"
        : "成品文案已导入，已跳到配音步骤。可直接生成配音与字幕。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("成品文案导入失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const startRemixReview = async (notes: string) => {
    if (!selected || !detail?.assets.continuous_script) return;
    const project = selected;
    const projectID = project.id;
    const revisionNotes = notes.trim();
    if (!revisionNotes) {
      setMessage("请先填写修改要求，再打回重做。");
      return;
    }
    const lockKey = lockAction(projectID, "remix-review");
    if (!lockKey) return;
    try {
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "remix",
          action: "remix.review",
          prompt: `按修改要求重写当前连续文案。\n\n修改要求：\n${revisionNotes}`,
          revision_notes: revisionNotes,
          ...modelOverrideBody(),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("打回重做任务启动失败，请检查当前连续文案与 Codex 配置后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      onRemixReviewStarted();
      setTaskModel({ model: "", reasoningEffort: "" });
      setMessage("已按修改要求打回 AI 重做；完成后会生成新的连续文案版本。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("打回重做任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const generateNarration = async () => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    if (detail?.project.id !== projectID || detail.assets.continuous_script?.state !== "ready") {
      setMessage("请先备好连续文案，再生成配音与字幕。");
      return;
    }
    const lockKey = lockAction(projectID, "generate-narration");
    if (!lockKey) return;
    try {
      const outcome = await generateNarrationMutation.mutateAsync({ projectID });
      if (selectedIDRef.current !== projectID) return;
      if (!outcome.ok) {
        setMessage(outcome.message);
        return;
      }
      const warnings = outcome.result.warnings?.length || 0;
      setMessage(
        warnings
          ? `配音与字幕已生成，但有 ${warnings} 条提醒，建议打开字幕确认。`
          : "配音与字幕已生成，可继续下一步。",
      );
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("配音与字幕生成失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const uploadAsset = async (type: "narration" | "subtitle_srt", file: File) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockAction(projectID, `upload:${type}`);
    if (!lockKey) return;
    if (type === "narration") {
      const name = file.name.toLowerCase();
      if (!(name.endsWith(".mp3") || name.endsWith(".wav") || name.endsWith(".m4a"))) {
        setMessage("配音仅支持 mp3、wav、m4a 文件。");
        unlockAction(lockKey);
        return;
      }
    }
    if (type === "subtitle_srt" && !file.name.toLowerCase().endsWith(".srt")) {
      setMessage("字幕仅支持 .srt 文件。");
      unlockAction(lockKey);
      return;
    }
    const body = new FormData();
    body.set("file", file);
    try {
      const response = await api(`/api/projects/${projectID}/assets/${type}`, {
        method: "POST",
        body,
      });
      if (!response.ok) {
        if (selectedIDRef.current !== projectID) return;
        let code = "";
        try {
          const payload = (await response.json()) as { code?: string };
          code = payload.code || "";
        } catch {
          /* ignore non-JSON bodies */
        }
        if (response.status === 413 || code === "payload_too_large")
          setMessage("素材过大，配音请控制在 200MB 以内。");
        else if (response.status === 403 || code === "csrf_invalid")
          setMessage("登录状态已失效，请刷新页面后重新登录再上传。");
        else if (response.status === 401 || code === "authentication_required")
          setMessage("未登录或会话过期，请重新登录后再上传。");
        else if (code === "invalid_asset_type")
          setMessage("当前服务不支持该素材类型，请重启控制台到最新版本后重试。");
        else setMessage("素材上传失败，请检查文件格式后重试。");
        return;
      }
      if (selectedIDRef.current === projectID) await loadDetail(project);
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("素材上传失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const replaceBackground = async (file: File) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockAction(projectID, "upload:account_background");
    if (!lockKey) return;
    const body = new FormData();
    body.set("background", file);
    try {
      const response = await api(`/api/accounts/${project.account_id}/background`, {
        method: "POST",
        body,
      });
      if (!response.ok) {
        if (selectedIDRef.current === projectID)
          setMessage("账号背景图上传失败，请选择 PNG、JPEG 或 WebP 图片后重试。");
        return;
      }
      if (selectedIDRef.current === projectID) await loadDetail(project);
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("账号背景图上传失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const deleteProject = async () => {
    if (!selected) return;
    const project = selected;
    if (
      !window.confirm(
        `确定删除项目“${project.title}”吗？项目专属文案、配音、SRT、草稿和任务记录都会一并删除，此操作无法恢复。`,
      )
    )
      return;
    const projectID = project.id;
    const lockKey = lockAction(projectID, "delete");
    if (!lockKey) return;
    try {
      const response = await api(`/api/projects/${projectID}`, { method: "DELETE" });
      if (!response.ok) {
        if (selectedIDRef.current === projectID) setMessage("项目删除失败，请稍后重试。");
        return;
      }
      setProjects((current) => current.filter((item) => item.id !== projectID));
      if (selectedIDRef.current !== projectID) return;
      onProjectDeleted();
      setMessage(`项目“${project.title}”已删除。`);
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("项目删除失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  const deleteProjects = async (ids: string[]) => {
    const unique = [...new Set(ids.map((id) => id.trim()).filter(Boolean))];
    if (unique.length === 0) return;
    const lockKey = lockAction("board", "batch-delete");
    if (!lockKey) return;
    try {
      const response = await api("/api/projects/batch-delete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ids: unique }),
      });
      if (!response.ok) {
        setMessage("批量删除失败，请稍后重试。");
        return;
      }
      const body = (await response.json()) as {
        deleted?: string[];
        failed?: Array<{ id: string; code?: string }>;
      };
      const deleted = body.deleted ?? [];
      const failed = body.failed ?? [];
      if (deleted.length > 0) {
        setProjects((current) => current.filter((item) => !deleted.includes(item.id)));
        await client.invalidateQueries({ queryKey: queryKeys.projects() });
        if (selectedIDRef.current && deleted.includes(selectedIDRef.current))
          onProjectDeleted();
      }
      const busy = failed.filter((item) => item.code === "project_active_task").length;
      if (deleted.length > 0 && failed.length === 0)
        setMessage(`已删除 ${deleted.length} 个项目。`);
      else if (deleted.length > 0 && busy > 0)
        setMessage(`已删除 ${deleted.length} 个项目，另有 ${busy} 个因任务未结束未删。`);
      else if (deleted.length > 0)
        setMessage(`已删除 ${deleted.length} 个项目，另有 ${failed.length} 个未删。`);
      else if (busy > 0)
        setMessage("选中的项目还有进行中的任务，请等任务结束后再删。");
      else
        setMessage("批量删除失败，请稍后重试。");
    } catch (error) {
      if (!isAbortError(error)) setMessage("批量删除失败，请检查网络连接后重试。");
    } finally {
      unlockAction(lockKey);
    }
  };

  return {
    pendingActions,
    taskModel,
    setTaskModel,
    remixPromptStyle,
    setRemixPromptStyle,
    createProject,
    loadSourceScriptContent,
    saveSourceScriptAndStartRemix,
    startMontageTask,
    startMovieMontageTask,
    startImageVideoTask,
    publishProject,
    saveContinuousScript,
    importContinuousScript,
    startRemixReview,
    generateNarration,
    uploadAsset,
    replaceBackground,
    deleteProject,
    deleteProjects,
  };
}
