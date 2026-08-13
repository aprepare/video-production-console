import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { ImageProject, ImageProjectDetail, ImageProjectItem } from "../types";
import "./image-mode.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;

type Props = {
  api: API;
  defaultRatio?: ImageProject["ratio"];
  defaultStyle?: string;
  defaultConcurrency?: number;
  defaultTextModel?: string;
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
  defaultRatio = "3:4",
  defaultStyle = "finance_documentary",
  defaultConcurrency = 3,
  defaultTextModel = "",
}: Props) {
  const [projects, setProjects] = useState<ImageProject[]>([]);
  const [detail, setDetail] = useState<ImageProjectDetail | null>(null);
  const [previewItem, setPreviewItem] = useState<ImageProjectItem | null>(null);
  const [segments, setSegments] = useState<DraftSegment[]>([]);
  const [plannerModel, setPlannerModel] = useState("");
  const [title, setTitle] = useState("");
  const [script, setScript] = useState("");
  const [count, setCount] = useState(8);
  const [ratio, setRatio] = useState<ImageProject["ratio"]>(defaultRatio);
  const [style, setStyle] = useState(defaultStyle);
  const [customStyle, setCustomStyle] = useState("");
  const [textModel, setTextModel] = useState(defaultTextModel);
  const [concurrency, setConcurrency] = useState(Math.min(18, Math.max(1, defaultConcurrency)));
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const editedDefaults = useRef({ ratio: false, style: false, concurrency: false, textModel: false });
  const previewCloseRef = useRef<HTMLButtonElement | null>(null);
  const previewReturnFocusRef = useRef<HTMLElement | null>(null);

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

  useEffect(() => {
    if (!editedDefaults.current.textModel) setTextModel(defaultTextModel);
  }, [defaultTextModel]);

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

  const openProject = async (project: ImageProject) => {
    setBusy(`open-${project.id}`);
    setMessage("");
    try {
      const response = await api(`/api/image-projects/${project.id}`);
      if (!response.ok) throw new Error("open failed");
      setDetail(orderedDetail(await response.json() as ImageProjectDetail));
    } catch {
      setMessage("图文项目读取失败，请稍后重试。");
    } finally {
      setBusy("");
    }
  };

  const draftPayload = () => ({
    title: title.trim(),
    script,
    image_count: count,
    ratio,
    style,
    custom_style: customStyle.trim(),
    concurrency,
    text_model: textModel.trim(),
  });

  const requestSegments = async (event: FormEvent) => {
    event.preventDefault();
    const cleanTitle = title.trim();
    if (!cleanTitle || !script.trim() || count < 1 || count > 18) return;
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
      if (!response.ok) throw new Error("suggest failed");
      const next = await response.json() as { model?: string; segments?: DraftSegment[] };
      const loaded = Array.isArray(next.segments) ? next.segments : [];
      if (!loaded.length) throw new Error("empty segments");
      setSegments(loaded);
      setPlannerModel(next.model || textModel.trim());
      setMessage("分段建议已生成。第一张是封面，确认原文无改写后再生成提示词。");
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
        }),
      });
      if (!response.ok) throw new Error("create failed");
      const next = orderedDetail(await response.json() as ImageProjectDetail);
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
    setDetail(ordered);
    setProjects((current) => [ordered.project, ...current.filter((item) => item.id !== ordered.project.id)]);
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

  if (detail) return (
    <main className="image-mode-workbench">
      <div className="image-mode-head">
        <div>
          <span className="eyebrow">图文模式</span>
          <h1>{detail.project.title}</h1>
          <p>比例 {detail.project.ratio} · {detail.items.length} 张 · 第一张是封面 · 最终文案不二创</p>
        </div>
        <div className="image-mode-actions">
          <button type="button" disabled={Boolean(busy)} onClick={() => { setDetail(null); setPreviewItem(null); }}>返回图文项目</button>
          <button type="button" className="primary" disabled={Boolean(busy)} onClick={() => void generateAll()}>{busy === "all" ? "正在生成…" : "生成缺失图片"}</button>
          <button type="button" className="primary" disabled={Boolean(busy) || detail.items.some((item) => item.status !== "ready")} onClick={() => void downloadArchive()}>{busy === "download" ? "正在打包…" : "打包下载"}</button>
          <button type="button" className="danger" disabled={Boolean(busy)} onClick={() => void deleteProject()}>{busy === "delete" ? "正在删除…" : "删除图文项目"}</button>
        </div>
      </div>
      {message ? <div className="notice" role="status">{message}</div> : null}
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
              <label>对应原文<textarea rows={3} value={item.source_text} onChange={(event) => updateLocalItem(item.id, "source_text", event.target.value)} /></label>
              <label>图片 {sequence} 提示词<textarea rows={7} value={item.prompt} onChange={(event) => updateLocalItem(item.id, "prompt", event.target.value)} /></label>
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
    </main>
  );

  return (
    <main className="image-mode-workbench">
      <div className="image-mode-head">
        <div>
          <span className="eyebrow">图文模式</span>
          <h1>图文项目</h1>
          <p>粘贴最终文案，先看 AI 分段建议，确认后再生成提示词和图片。最多 18 张，第一张是封面。</p>
        </div>
      </div>
      {message ? <div className="notice" role="status">{message}</div> : null}
      <div className="image-mode-start">
        <form className="image-create-form" onSubmit={requestSegments}>
          <h2>新建图文项目</h2>
          <label>项目名称<input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="例如：养老现金流" maxLength={120} /></label>
          <label>最终文案<textarea rows={12} value={script} onChange={(event) => { setScript(event.target.value); setSegments([]); }} placeholder="粘贴已经定稿的完整文案；系统不会二创" /></label>
          <div className="image-param-grid">
            <label>建议张数<input type="number" min={1} max={18} value={count} onChange={(event) => setCount(Math.min(18, Math.max(1, Number(event.target.value) || 1)))} /></label>
            <label>图片比例<select value={ratio} onChange={(event) => { editedDefaults.current.ratio = true; setRatio(event.target.value as ImageProject["ratio"]); }}>{ratios.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
            <label>视觉风格<select value={style} onChange={(event) => { editedDefaults.current.style = true; setStyle(event.target.value); }}>{styles.map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
            <label>项目并发<select value={concurrency} onChange={(event) => { editedDefaults.current.concurrency = true; setConcurrency(Number(event.target.value)); }}>{concurrencyOptions.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
          </div>
          <label>文本模型<input value={textModel} onChange={(event) => { editedDefaults.current.textModel = true; setTextModel(event.target.value); }} placeholder="可覆盖设置里的图文文本模型" /></label>
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
        <section className="image-project-list" aria-labelledby="image-project-list-title">
          <h2 id="image-project-list-title">已有图文项目</h2>
          {sortedProjects.length ? sortedProjects.map((project) => (
            <button type="button" key={project.id} disabled={Boolean(busy)} onClick={() => void openProject(project)}>
              <strong>{project.title}</strong>
              <span>{project.image_count} 张 · {project.ratio} · {project.status}</span>
            </button>
          )) : <p>还没有图文项目。</p>}
        </section>
      </div>
    </main>
  );
}
