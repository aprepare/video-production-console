import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ArrowLeft, Download, Eye, EyeOff, FileText, Play, RefreshCw, Trash2 } from "lucide-react";
import { reasoningEfforts } from "../taskModel";
import type { ReasoningEffort } from "../taskModel";
import type { ImageProject, ImageProjectDetail, ImageProjectItem, PublishingCandidate } from "../types";
import { PublishingDialog, publishingCandidatesFrom, selectedPublishingFrom } from "./PublishingDialog";
import { DEFAULT_IMAGE_TEXT_MODEL, QuickGenerateForm, resolveImageTextModel } from "./QuickGenerateForm";
import "./image-mode.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;

type Props = {
  api: API;
  mode?: "quick" | "advanced";
  initialProjectID?: string;
  onProjectOpen?: (id: string) => void;
  onProjectClose?: () => void;
  defaultRatio?: ImageProject["ratio"];
  defaultStyle?: string;
  defaultConcurrency?: number;
  defaultTextModel?: string;
  defaultReasoningEffort?: ReasoningEffort | "";
  defaultImageModel?: string;
  defaultImageAttempts?: number;
  onAdvancedMode?: () => void;
};
type EditableItemField = "source_text" | "title" | "prompt";
type DraftSegment = {
  sequence: number;
  role: "cover" | "content";
  title: string;
  source_text: string;
  rationale?: string;
};

const styles = [
  ["finance_documentary", "财经纪实插画"],
  ["red_ink", "赤墨风"],
  ["old_newspaper", "旧报档案风"],
  ["ledger_investigation", "账本调查风"],
  ["dark_crisis", "暗黑危机风"],
  ["city_era", "城市时代感"],
  ["blackboard", "黑板讲解风"],
  ["custom", "自定义风格"],
] as const;

const ratios: ImageProject["ratio"][] = ["3:4", "4:3", "9:16", "1:1"];
const concurrencyOptions = Array.from({ length: 18 }, (_, index) => index + 1);
const attemptOptions = [1, 2, 3, 4];
const phaseOrder = ["planning", "prompting", "imaging", "completed"] as const;

function clampAttempts(value: number) {
  if (!Number.isFinite(value) || value < 1 || value > 4) return 2;
  return value;
}

function quickStageItems(project: ImageProject) {
  const success = project.success_count ?? 0;
  const failure = project.failure_count ?? 0;
  const total = project.image_count || 0;
  const progressed = success + failure;
  const current = Math.max(0, phaseOrder.indexOf((project.run_phase || "planning") as typeof phaseOrder[number]));
  return [
    { key: "planning", label: "分析文案" },
    { key: "prompting", label: "生成提示词" },
    { key: "imaging", label: `生成图片 ${progressed}/${total}` },
    { key: "completed", label: project.run_status === "completed" ? `完成 ${success}/${total}` : "完成" },
  ].map((stage, index) => ({
    ...stage,
    state: project.run_status === "completed" || index < current ? "done" : index === current ? "current" : "pending",
  }));
}
function segmentErrorMessage(code?: string, detail?: string, status?: number) {
  const d = detail?.trim();
  if (code === "image_text_model_not_configured") return "\u56fe\u6587\u6587\u672c\u6a21\u578b\u5730\u5740\u3001\u6a21\u578b\u548c API Key \u5c1a\u672a\u914d\u7f6e\uff0c\u4e14\u548c\u751f\u56fe Key \u4e0d\u662f\u540c\u4e00\u5957";
  if (code === "image_text_model_failed" || (!code && status === 499)) return d ? `\u4e0a\u6e38\u88ab\u53d6\u6d88\u3002\u8be6\u60c5\uff1a${d}` : "\u4e0a\u6e38\u88ab\u53d6\u6d88";
  if (code === "invalid_image_project") return d ? `\u9879\u76ee\u540d称\u3001\u6587\u7a3f或\u98ce格不完整。\u8be6\u60c5\uff1a${d}` : "\u9879\u76ee名称、\u6587\u7a3f或\u98ce\u683c不\u5b8c\u6574";
  return d || "\u5206\u6bb5\u5efa\u8bae\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5";
}

function orderedItems(items: ImageProjectItem[]) {
  return [...items].sort((left, right) => left.sequence - right.sequence);
}

function orderedDetail(value: ImageProjectDetail): ImageProjectDetail {
  return { ...value, items: orderedItems(value.items) };
}

function imageURL(projectID: string, itemID: string, revision: string | number) {
  return `/api/image-projects/${projectID}/items/${itemID}/image?v=${encodeURIComponent(String(revision))}`;
}

export function ImageModeWorkbench({
  api,
  mode = "quick",
  initialProjectID,
  onProjectOpen,
  onProjectClose,
  defaultRatio = "3:4",
  defaultStyle = "finance_documentary",
  defaultConcurrency = 3,
  defaultTextModel = "",
  defaultReasoningEffort = "",
  defaultImageModel = "",
  defaultImageAttempts = 2,
  onAdvancedMode,
}: Props) {
  const [projects, setProjects] = useState<ImageProject[]>([]);
  const [detail, setDetail] = useState<ImageProjectDetail | null>(null);
  const [previewItem, setPreviewItem] = useState<ImageProjectItem | null>(null);
  const [segments, setSegments] = useState<DraftSegment[]>([]);
  const [publishingCandidates, setPublishingCandidates] = useState<PublishingCandidate[]>([]);
  const [selectedPublishing, setSelectedPublishing] = useState(1);
  const [publishingOpen, setPublishingOpen] = useState(false);
  const [plannerModel, setPlannerModel] = useState("");
  const [title, setTitle] = useState("");
  const [script, setScript] = useState("");
  const [count, setCount] = useState(0);
  const [ratio, setRatio] = useState<ImageProject["ratio"]>(defaultRatio);
  const [style, setStyle] = useState(defaultStyle);
  const [customStyle, setCustomStyle] = useState("");
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort | "">(defaultReasoningEffort);
  const [concurrency, setConcurrency] = useState(Math.min(18, Math.max(1, defaultConcurrency)));
  const [imageAttempts, setImageAttempts] = useState(clampAttempts(defaultImageAttempts));
  const [textModel, setTextModel] = useState(resolveImageTextModel(defaultTextModel));
  const [titleDraft, setTitleDraft] = useState("");
  const [showAdvancedEdit, setShowAdvancedEdit] = useState(false);
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [routeState, setRouteState] = useState<"idle" | "loading" | "error404" | "error">("idle");
  const routeRequestRef = useRef(0);
  const loadedDetailRef = useRef<ImageProjectDetail | null>(null);
  const inFlightIDRef = useRef<string | null>(null);
  const editedDefaults = useRef({ ratio: false, style: false, concurrency: false, reasoningEffort: false, imageAttempts: false, textModel: false });
  const previewCloseRef = useRef<HTMLButtonElement | null>(null);
  const previewReturnFocusRef = useRef<HTMLElement | null>(null);
  const onProjectOpenRef = useRef(onProjectOpen);
  useEffect(() => {
    onProjectOpenRef.current = onProjectOpen;
  }, [onProjectOpen]);

  useEffect(() => {
    if (!editedDefaults.current.ratio) setRatio(defaultRatio);
  }, [defaultRatio]);

  useEffect(() => {
    if (!editedDefaults.current.style) setStyle(defaultStyle);
  }, [defaultStyle]);

  useEffect(() => {
    if (!editedDefaults.current.concurrency) {
      setConcurrency(Math.min(18, Math.max(1, defaultConcurrency)));
    }
  }, [defaultConcurrency]);

  useEffect(() => { if (!editedDefaults.current.reasoningEffort) setReasoningEffort(defaultReasoningEffort); }, [defaultReasoningEffort]);
  useEffect(() => { if (!editedDefaults.current.imageAttempts) setImageAttempts(clampAttempts(defaultImageAttempts)); }, [defaultImageAttempts]);
  useEffect(() => { if (!editedDefaults.current.textModel) setTextModel(resolveImageTextModel(defaultTextModel)); }, [defaultTextModel]);
  const detailID = detail?.project.id ?? "";
  const detailTitle = detail?.project.title ?? "";
  useEffect(() => {
    if (detailID) setTitleDraft(detailTitle);
  }, [detailID, detailTitle]);
  useEffect(() => {
    setShowAdvancedEdit(false);
  }, [detailID]);

  useEffect(() => {
    if (!previewItem) return;
    previewCloseRef.current?.focus();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setPreviewItem(null);
      } else if (event.key === "Tab") {
        event.preventDefault();
        previewCloseRef.current?.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      previewReturnFocusRef.current?.focus();
    };
  }, [previewItem]);

  const refreshList = useCallback(async (signal?: AbortSignal) => {
    try {
      const response = await api("/api/image-projects", signal ? { signal } : undefined);
      if (!response.ok) throw new Error("list failed");
      const loaded = await response.json() as ImageProject[];
      setProjects((current) => {
        const currentIDs = new Set(current.map((project) => project.id));
        return [...current, ...loaded.filter((project) => !currentIDs.has(project.id))];
      });
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      setMessage("图文项目读取失败，请稍后重试。");
    }
  }, [api]);

  useEffect(() => {
    const controller = new AbortController();
    void refreshList(controller.signal);
    return () => controller.abort();
  }, [refreshList]);

  const loadProject = useCallback(async (id: string, notify = false) => {
    if (inFlightIDRef.current === id) return;
    const request = ++routeRequestRef.current;
    inFlightIDRef.current = id;
    const held = loadedDetailRef.current;
    const sameID = held?.project.id === id;
    if (!sameID) {
      setRouteState("loading");
      setDetail(null);
      setPreviewItem(null);
    }
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${id}`);
      if (request !== routeRequestRef.current) return;
      if (!response.ok) {
        if (response.status === 404 && sameID) {
          setRouteState("idle");
          return;
        }
        setRouteState(response.status === 404 ? "error404" : "error");
        return;
      }
      const loaded = orderedDetail(await response.json() as ImageProjectDetail);
      if (request !== routeRequestRef.current) return;
      loadedDetailRef.current = loaded;
      setDetail(loaded);
      setPublishingCandidates(publishingCandidatesFrom(loaded));
      setSelectedPublishing(selectedPublishingFrom(loaded));
      setRouteState("idle");
      if (notify) onProjectOpenRef.current?.(id);
    } catch {
      if (request === routeRequestRef.current) setRouteState(sameID ? "idle" : "error");
    } finally {
      if (inFlightIDRef.current === id) inFlightIDRef.current = null;
    }
  }, [api]);

  const refreshProject = useCallback(async (id: string) => {
    try {
      const response = await api(`/api/image-projects/${id}`);
      if (!response.ok) return;
      const loaded = orderedDetail(await response.json() as ImageProjectDetail);
      if (loaded.project.id !== id) return;
      const held = loadedDetailRef.current;
      if (held && held.project.id !== id) return;
      loadedDetailRef.current = loaded;
      setDetail(loaded);
      setPublishingCandidates(publishingCandidatesFrom(loaded));
      setSelectedPublishing(selectedPublishingFrom(loaded));
    } catch {
      // Quiet refresh must not replace a held detail or flash loading.
    }
  }, [api]);

  useEffect(() => {
    if (initialProjectID) void loadProject(initialProjectID);
    else {
      routeRequestRef.current += 1;
      loadedDetailRef.current = null;
      inFlightIDRef.current = null;
      setDetail(null);
      setPreviewItem(null);
      setRouteState("idle");
    }
  }, [initialProjectID, loadProject]);

  useEffect(() => {
    if (detail?.project.run_status !== "running") return;
    const id = detail.project.id;
    const timer = window.setInterval(() => { void refreshProject(id); }, 1500);
    return () => window.clearInterval(timer);
  }, [detail?.project.id, detail?.project.run_status, refreshProject]);

  const openProject = (project: ImageProject) => {
    if (onProjectOpen) {
      onProjectOpen(project.id);
      return;
    }
    void loadProject(project.id);
  };

  const draftPayload = () => ({
    title: title.trim(),
    script,
    image_count: count,
    ratio,
    style,
    custom_style: customStyle.trim(),
    concurrency,
    text_model: resolveImageTextModel(textModel),
    reasoning_effort: reasoningEffort,
    image_attempts: clampAttempts(imageAttempts),
    image_model: defaultImageModel.trim(),
  });

  const requestSegments = async (event: FormEvent) => {
    event.preventDefault();
    const cleanTitle = title.trim();
    if (!cleanTitle || !script.trim() || (count !== 0 && (count < 1 || count > 18))) return;
    if (style === "custom" && !customStyle.trim()) {
      setMessage("请填写自定义风格。");
      return;
    }
    setBusy("suggest");
    setMessage("");
    try {
      const response = await api("/api/image-projects/segment-preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(draftPayload()),
      });
      const next = await response.json() as { model?: string; segments?: DraftSegment[]; publishing_candidates?: PublishingCandidate[]; code?: string; message?: string };
      if (!response.ok) { setMessage(segmentErrorMessage(next.code, next.message, response.status)); return; }
      const loaded = Array.isArray(next.segments) ? next.segments : [];
      if (!loaded.length) throw new Error("empty segments");
      setSegments(loaded);
      setPublishingCandidates(next.publishing_candidates || []);
      setCount(loaded.length);
      setPlannerModel(next.model || resolveImageTextModel(textModel));
      setMessage(`分段建议已生成，共 ${loaded.length} 段。第一张是封面，确认原文无改写后再生成提示词。`);
    } catch {
      setMessage("分段建议失败。请先检查图文文本模型设置，或稍后重试。");
    } finally {
      setBusy("");
    }
  };

  const updateSegment = (index: number, key: keyof DraftSegment, value: string) => {
    setSegments((current) => current.map((segment, currentIndex) => (
      currentIndex === index ? { ...segment, [key]: value } : segment
    )));
  };

  const create = async () => {
    const cleanTitle = title.trim();
    if (!cleanTitle || !script.trim() || !segments.length) {
      setMessage("请先确认分段建议，再生成提示词。");
      return;
    }
    if (style === "custom" && !customStyle.trim()) {
      setMessage("请填写自定义风格。");
      return;
    }
    setBusy("create");
    setMessage("");
    try {
      const response = await api("/api/image-projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...draftPayload(),
          segments,
          publishing_candidates: publishingCandidates,
          selected_position: 1,
        }),
      });
      if (!response.ok) throw new Error("create failed");
      const next = orderedDetail(await response.json() as ImageProjectDetail);
      loadedDetailRef.current = next;
      setDetail(next);
      setProjects((current) => [next.project, ...current.filter((item) => item.id !== next.project.id)]);
      setSegments([]);
      setMessage("分段已确认，提示词已生成；最终文案不会二创。");
    } catch {
      setMessage("确认分段后生成提示词失败。请检查分段是否覆盖原文，以及文本模型是否可用。");
    } finally {
      setBusy("");
    }
  };

  const replaceDetail = (next: ImageProjectDetail) => {
    const ordered = orderedDetail(next);
    loadedDetailRef.current = ordered;
    setDetail(ordered);
    setProjects((current) => [ordered.project, ...current.filter((item) => item.id !== ordered.project.id)]);
  };

  const saveTitle = async () => {
    if (!detail || busy) return;
    const nextTitle = titleDraft.trim();
    if (!nextTitle) return;
    setBusy("title");
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${detail.project.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: nextTitle }),
      });
      if (!response.ok) throw new Error("title failed");
      replaceDetail(await response.json() as ImageProjectDetail);
    } catch {
      setMessage("项目名称保存失败，请稍后重试。");
    } finally {
      setBusy("");
    }
  };

  const resumeQuick = async () => {
    if (!detail || busy) return;
    setBusy("resume");
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${detail.project.id}/resume`, { method: "POST" });
      if (!response.ok) throw new Error("resume failed");
      await refreshProject(detail.project.id);
    } catch {
      setMessage("继续生成失败，请稍后重试。");
    } finally {
      setBusy("");
    }
  };

  const generateAll = async () => {
    if (!detail || busy) return;
    setBusy("all");
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${detail.project.id}/generate`, { method: "POST" });
      if (!response.ok) throw new Error("generate failed");
      replaceDetail(await response.json() as ImageProjectDetail);
      setMessage("缺失图片已生成。");
    } catch {
      setMessage("图片生成失败，请先检查生图服务设置。");
    } finally {
      setBusy("");
    }
  };

  const updateLocalItem = (id: string, key: EditableItemField, value: string) => {
    setDetail((current) => current ? {
      ...current,
      items: current.items.map((item) => item.id === id ? { ...item, [key]: value } : item),
    } : current);
  };

  const saveItem = async (itemID: string) => {
    if (!detail || busy) return;
    const item = detail.items.find((value) => value.id === itemID);
    if (!item) return;
    if (!item.source_text.trim() || !item.title.trim() || !item.prompt.trim()) {
      setMessage("对应原文、图片名称和提示词都不能为空。");
      return;
    }
    setBusy(`save-${itemID}`);
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${detail.project.id}/items/${itemID}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source_text: item.source_text,
          title: item.title.trim(),
          prompt: item.prompt.trim(),
        }),
      });
      if (!response.ok) throw new Error("save failed");
      replaceDetail(await response.json() as ImageProjectDetail);
      setMessage(`图片 ${String(item.sequence).padStart(3, "0")} 已保存。`);
    } catch {
      setMessage("图片文字和提示词保存失败，请稍后重试。");
    } finally {
      setBusy("");
    }
  };

  const generateOne = async (itemID: string) => {
    if (!detail || busy) return;
    const item = detail.items.find((value) => value.id === itemID);
    if (!item) return;
    if (!item.source_text.trim() || !item.title.trim() || !item.prompt.trim()) {
      setMessage("请先补全并保存对应原文、图片名称和提示词，再重新生成。");
      return;
    }
    setBusy(`generate-${itemID}`);
    setMessage("");
    try {
      const saveResponse = await api(`/api/image-projects/${detail.project.id}/items/${itemID}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source_text: item.source_text,
          title: item.title.trim(),
          prompt: item.prompt.trim(),
        }),
      });
      if (!saveResponse.ok) throw new Error("save before generate failed");
      const response = await api(`/api/image-projects/${detail.project.id}/items/${itemID}/generate`, { method: "POST" });
      if (!response.ok) throw new Error("generate failed");
      replaceDetail(await response.json() as ImageProjectDetail);
      setMessage(`图片 ${String(item.sequence).padStart(3, "0")} 已保存并重新生成。`);
    } catch {
      setMessage("单张图片生成失败，请检查提示词和生图服务。");
    } finally {
      setBusy("");
    }
  };

  const deleteProject = async () => {
    if (!detail || busy) return;
    const readyCount = detail.items.filter((item) => item.status === "ready").length;
    if (!window.confirm(`确定删除图文项目“${detail.project.title}”吗？将删除 ${detail.items.length} 张卡片及 ${readyCount} 张已生成图片。此操作不可撤销。`)) return;
    const id = detail.project.id;
    setBusy("delete");
    try {
      const response = await api(`/api/image-projects/${id}`, { method: "DELETE" });
      if (!response.ok) throw new Error("delete failed");
      setDetail(null);
      setPreviewItem(null);
      setProjects((current) => current.filter((item) => item.id !== id));
      onProjectClose?.();
      setMessage("图文项目已删除。");
    } catch {
      setMessage("图文项目删除失败，请稍后重试。");
    } finally {
      setBusy("");
    }
  };

  const downloadArchive = async () => {
    if (!detail || busy || detail.items.some((item) => item.status !== "ready")) return;
    setBusy("download");
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${detail.project.id}/download`);
      if (!response.ok) throw new Error("download failed");
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `${detail.project.title}.zip`;
      anchor.click();
      URL.revokeObjectURL(url);
      setMessage("图文图片包已开始下载。");
    } catch {
      setMessage("打包下载失败，请确认全部图片已生成后重试。");
    } finally {
      setBusy("");
    }
  };

  const sortedProjects = useMemo(
    () => [...projects].sort((left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at)),
    [projects],
  );

  if (routeState === "loading") return <main className="image-mode-workbench"><div className="image-mode-notice">正在读取图文项目…</div></main>;
  if (routeState === "error404" || routeState === "error") return <main className="image-mode-workbench"><div className="image-mode-notice" role="alert"><p>{routeState === "error404" ? "图文项目不存在（404）" : "图文项目读取失败"}</p><button type="button" onClick={() => initialProjectID && void loadProject(initialProjectID)}>重试</button></div></main>;
  if (detail) {
    const isQuick = detail.project.run_mode === "quick";
    const hideEditors = isQuick && !showAdvancedEdit;
    const canResume = isQuick && (detail.project.run_status === "failed" || detail.project.run_status === "interrupted");
    const allReady = detail.items.length > 0 && detail.items.every((item) => item.status === "ready");
    const primaryAction = canResume ? "resume" : allReady ? "download" : "generate";
    return (
    <main className="image-mode-workbench">
      <div className="image-mode-head">
        <div>
          <span className="eyebrow">图文模式</span>
          <h1>{detail.project.title}</h1>
          {isQuick ? (
            <div className="image-title-edit">
              <label>
                图文项目名称
                <input value={titleDraft} maxLength={120} onChange={(event) => setTitleDraft(event.target.value)} />
              </label>
              <button type="button" disabled={Boolean(busy) || !titleDraft.trim() || titleDraft.trim() === detail.project.title} onClick={() => void saveTitle()}>{busy === "title" ? "正在保存…" : "保存名称"}</button>
            </div>
          ) : null}
          <p>比例 {detail.project.ratio} · {detail.items.length} 张 · 第一张是封面 · 最终文案不二创{typeof detail.project.success_count === "number" ? ` · 成功 ${detail.project.success_count}` : ""}</p>
        </div>
        <div className="image-mode-actions">
          <button type="button" disabled={Boolean(busy)} onClick={() => { loadedDetailRef.current = null; setDetail(null); setPreviewItem(null); setRouteState("idle"); onProjectClose?.(); }}>
            <ArrowLeft size={16} aria-hidden="true" />
            返回图文项目
          </button>
          {canResume ? (
            <button type="button" className={primaryAction === "resume" ? "primary" : undefined} disabled={Boolean(busy)} onClick={() => void resumeQuick()}>
              <Play size={16} aria-hidden="true" />
              {busy === "resume" ? "正在继续…" : "继续生成"}
            </button>
          ) : null}
          <button type="button" className={primaryAction === "generate" ? "primary" : undefined} disabled={Boolean(busy)} onClick={() => void generateAll()}>
            <RefreshCw size={16} aria-hidden="true" />
            {busy === "all" ? "正在生成…" : "生成缺失图片"}
          </button>
          <button type="button" disabled={Boolean(busy)} onClick={() => setPublishingOpen(true)}>
            <FileText size={16} aria-hidden="true" />
            图文标题及描述
          </button>
          <button type="button" className={primaryAction === "download" ? "primary" : undefined} disabled={Boolean(busy) || !allReady} onClick={() => void downloadArchive()}>
            <Download size={16} aria-hidden="true" />
            {busy === "download" ? "正在打包…" : "打包下载"}
          </button>
          <button type="button" className="danger" disabled={Boolean(busy)} onClick={() => void deleteProject()}>
            <Trash2 size={16} aria-hidden="true" />
            {busy === "delete" ? "正在删除…" : "删除图文项目"}
          </button>
        </div>
      </div>
      {isQuick ? (
        <ol className="image-stage-rail" aria-label="生成进度">
          {quickStageItems(detail.project).map((stage, index) => (
            <li key={stage.key} className={`image-stage-rail__item image-stage-rail__item--${stage.state}`}>
              <span className="image-stage-rail__index">{String(index + 1).padStart(2, "0")}</span>
              <span className="image-stage-rail__label">{stage.label}</span>
            </li>
          ))}
        </ol>
      ) : null}
      {detail.project.phase_error ? <div className="image-mode-notice" role="alert">{detail.project.phase_error}</div> : null}
      {detail.project.publishing_error ? <div className="image-mode-notice" role="status">发布信息未生成：{detail.project.publishing_error}</div> : null}
      {message ? <div className="image-mode-notice" role="status">{message}</div> : null}
      {isQuick ? (
        <button type="button" className="quick-advanced-link" onClick={() => setShowAdvancedEdit((open) => !open)}>
          {showAdvancedEdit ? <EyeOff size={16} aria-hidden="true" /> : <Eye size={16} aria-hidden="true" />}
          {showAdvancedEdit ? "隐藏高级编辑" : "显示高级编辑"}
        </button>
      ) : null}
      <section className="image-card-grid" aria-label="有序图片画廊">
        {detail.items.map((item) => {
          const sequence = String(item.sequence).padStart(3, "0");
          const revision = item.updated_at || `${item.status}-${item.width || 0}-${item.height || 0}`;
          return (
            <article className={`image-card image-card--${item.status}`} key={item.id} aria-label={`图片 ${sequence}`}>
              <div className="image-card-number">{sequence}{item.role === "cover" || item.sequence === 1 ? " 封面" : ""}</div>
              {item.status === "ready" ? (
                <button type="button" className="image-preview-trigger" onClick={(event) => { previewReturnFocusRef.current = event.currentTarget; setPreviewItem(item); }} aria-label={`预览图片 ${sequence}`}>
                  <img src={imageURL(detail.project.id, item.id, revision)} alt="" />
                </button>
              ) : (
                <div className="image-placeholder">{item.status === "generating" ? "生成中" : item.status === "failed" ? "生成失败" : "等待生成"}</div>
              )}
              <label>图片名称<input value={item.title} onChange={(event) => updateLocalItem(item.id, "title", event.target.value)} /></label>
              {hideEditors ? null : <label>对应原文<textarea rows={3} value={item.source_text} onChange={(event) => updateLocalItem(item.id, "source_text", event.target.value)} /></label>}
              {hideEditors ? null : <label>图片 {sequence} 提示词<textarea rows={7} value={item.prompt} onChange={(event) => updateLocalItem(item.id, "prompt", event.target.value)} /></label>}
              {typeof item.attempt_count === "number" ? <small>已尝试 {item.attempt_count} 次</small> : null}
              {item.width && item.height ? <small>实际尺寸：{item.width}×{item.height}</small> : null}
              {item.error_message ? <p className="warning">{item.error_message}</p> : null}
              <div className="image-card-actions">
                <button type="button" disabled={Boolean(busy)} onClick={() => void saveItem(item.id)}>{busy === `save-${item.id}` ? "正在保存…" : `保存图片 ${sequence}`}</button>
                <button type="button" className="primary" disabled={Boolean(busy)} onClick={() => void generateOne(item.id)}>{busy === `generate-${item.id}` ? "正在生成…" : `重新生成 ${sequence}`}</button>
              </div>
            </article>
          );
        })}
      </section>
      {previewItem ? (
        <div className="image-preview-backdrop" onClick={() => setPreviewItem(null)}>
          <div className="image-preview-dialog" role="dialog" aria-modal="true" aria-labelledby="image-preview-title" onClick={(event) => event.stopPropagation()}>
            <div className="image-preview-head">
              <div>
                <strong id="image-preview-title">图片 {String(previewItem.sequence).padStart(3, "0")} 预览 · {previewItem.title}</strong>
                {previewItem.width && previewItem.height ? <small>{previewItem.width}×{previewItem.height}</small> : null}
              </div>
              <button ref={previewCloseRef} type="button" onClick={() => setPreviewItem(null)} aria-label="关闭图片预览">关闭</button>
            </div>
            <img src={imageURL(detail.project.id, previewItem.id, previewItem.updated_at || previewItem.status)} alt={`图片 ${String(previewItem.sequence).padStart(3, "0")} 大图`} />
          </div>
        </div>
      ) : null}
      {publishingOpen ? <PublishingDialog api={api} projectID={detail.project.id} candidates={publishingCandidates.length ? publishingCandidates : publishingCandidatesFrom(detail)} selected={selectedPublishing} publishingError={detail.project.publishing_error} onClose={() => setPublishingOpen(false)} onSaved={(next, selected) => { setPublishingCandidates(next); setSelectedPublishing(selected); }} /> : null}
    </main>
    );
  }

  const projectList = (
    <section className="image-project-list" aria-labelledby="image-project-list-title">
      <h2 id="image-project-list-title">已有图文项目</h2>
      {sortedProjects.length ? sortedProjects.map((project) => (
        <button type="button" key={project.id} disabled={Boolean(busy)} onClick={() => void openProject(project)}>
          <strong>{project.title}</strong>
          <span>{project.image_count} 张 · {project.ratio} · {project.status}</span>
        </button>
      )) : <p>还没有图文项目。</p>}
    </section>
  );

  return (
    <main className="image-mode-workbench">
      <div className="image-mode-head">
        <div>
          <span className="eyebrow">图文模式</span>
          <h1>图文项目</h1>
          <p>{mode === "quick" ? "粘贴最终文案后直接生成图片。项目名称由模型生成，无需确认分段。" : "粘贴最终文案，先看 AI 分段建议，确认后再生成提示词和图片。最多 18 张，第一张是封面。"}</p>
        </div>
      </div>
      {message ? <div className="image-mode-notice" role="status">{message}</div> : null}
      <div className="image-mode-start image-mode-start--with-list">
        {mode === "quick" ? (
          <QuickGenerateForm
            api={api}
            defaultRatio={ratio}
            defaultStyle={style}
            defaultConcurrency={concurrency}
            defaultTextModel={defaultTextModel}
            defaultReasoningEffort={reasoningEffort}
            defaultImageModel={defaultImageModel}
            defaultImageAttempts={imageAttempts}
            onCreated={(projectID) => {
              if (onProjectOpen) onProjectOpen(projectID);
              else void loadProject(projectID);
            }}
            onAdvancedMode={() => onAdvancedMode?.()}
          />
        ) : (
        <form className="image-create-form" onSubmit={requestSegments}>
          <h2>新建图文项目</h2>
          <label>项目名称<input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="例如：养老现金流" maxLength={120} /></label>
          <label>最终文案<textarea rows={12} value={script} onChange={(event) => { setScript(event.target.value); setSegments([]); }} placeholder="粘贴已经定稿的完整文案；系统不会二创" /></label>
          <div className="image-param-grid">
            <label>{"\u5efa\u8bae\u5f20\u6570"}<input type="number" min={1} max={18} value={count || ""} onChange={(event) => setCount(Math.min(18, Math.max(0, Number(event.target.value) || 0)))} placeholder={"\u7559\u7a7a\u5219\u81ea\u52a8\u5206\u6bb5"} /></label>
            <label>{"\u601d\u8003\u5f3a\u5ea6"}<select aria-label={"\u601d\u8003\u5f3a\u5ea6"} value={reasoningEffort} onChange={(event) => { editedDefaults.current.reasoningEffort = true; setReasoningEffort(event.target.value as ReasoningEffort | ""); }}><option value="">{"\u8ddf\u968f\u8bbe\u7f6e"}</option>{reasoningEfforts.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
            <label>图片比例<select value={ratio} onChange={(event) => { editedDefaults.current.ratio = true; setRatio(event.target.value as ImageProject["ratio"]); }}>{ratios.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
            <label>视觉风格<select value={style} onChange={(event) => { editedDefaults.current.style = true; setStyle(event.target.value); }}>{styles.map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
            <label>项目并发<select value={concurrency} onChange={(event) => { editedDefaults.current.concurrency = true; setConcurrency(Number(event.target.value)); }}>{concurrencyOptions.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
            <label>每张图片最多请求次数<select aria-label="每张图片最多请求次数" value={imageAttempts} onChange={(event) => { editedDefaults.current.imageAttempts = true; setImageAttempts(Number(event.target.value)); }}>{attemptOptions.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
          </div>
          <div className="image-fixed-models">
            <p className="image-fixed-model">
              <span>图片模型</span>
              <strong aria-label="图片模型">{defaultImageModel || "未配置，请到设置中填写"}</strong>
            </p>
            <label>
              文本模型
              <input
                aria-label="文本模型"
                value={textModel}
                onChange={(event) => { editedDefaults.current.textModel = true; setTextModel(event.target.value); }}
                placeholder={DEFAULT_IMAGE_TEXT_MODEL}
                maxLength={128}
              />
            </label>
          </div>
          {style === "custom" ? <label>自定义风格<textarea rows={4} value={customStyle} onChange={(event) => setCustomStyle(event.target.value)} placeholder="描述画面材质、光线、色彩与构图" /></label> : null}
          <p className="image-style-note">第一张必须是封面。默认禁止伪文字、日期、收益和水印；优先中国家庭、养老、住房、银行与商业场景。</p>
          <button className="primary" disabled={busy === "suggest" || !title.trim() || !script.trim() || (style === "custom" && !customStyle.trim())}>{busy === "suggest" ? "正在分段…" : "生成分段建议"}</button>
          {segments.length ? (
            <section className="image-segment-preview" aria-label="分段建议">
              <h3>分段建议{plannerModel ? ` · ${plannerModel}` : ""}</h3>
              <p className="image-style-note">请核对每段原文。第一张是封面。确认无改写后，再生成提示词。</p>
              {segments.map((segment, index) => (
                <article className="image-segment-card" key={`${segment.sequence}-${index}`}>
                  <strong>{String(segment.sequence).padStart(3, "0")} {segment.role === "cover" || segment.sequence === 1 ? "封面" : "内容"}</strong>
                  <label>段落标题<input value={segment.title} onChange={(event) => updateSegment(index, "title", event.target.value)} /></label>
                  <label>对应原文<textarea rows={4} value={segment.source_text} onChange={(event) => updateSegment(index, "source_text", event.target.value)} /></label>
                  {segment.rationale ? <small>{segment.rationale}</small> : null}
                </article>
              ))}
              <button type="button" className="primary" disabled={Boolean(busy)} onClick={() => void create()}>{busy === "create" ? "正在生成提示词…" : "确认分段并生成提示词"}</button>
            </section>
          ) : null}
        </form>
        )}
        {projectList}
      </div>
    </main>
  );
}
