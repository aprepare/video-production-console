import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ArrowLeft, PenLine, Plus, Trash2 } from "lucide-react";
import {
  adoptRemixLabRun,
  createRemixLabExperiment,
  fetchRemixLabDefaults,
  fetchRemixLabExperiment,
  fetchRemixLabExperiments,
  patchRemixLabRunComment,
  type RemixLabApi,
  type RemixLabCreateSlot,
  type RemixLabDefaults,
  type RemixLabExperiment,
  type RemixLabExperimentSummary,
  type RemixLabRunView,
} from "./api";
import "./remix-lab.css";

type RemixLabPageProps = {
  api: RemixLabApi;
  experimentID?: string;
  onNavigate: (href: string) => void;
};

type DraftSlot = {
  base_url: string;
  model: string;
  api_key: string;
  reasoning_effort: string;
  run_count: number;
  preset_index: number | null;
  keyConfigured: boolean;
};

type ProjectOption = {
  id: string;
  title: string;
};

const MAX_SLOTS = 4;

function clampRunCount(value: number): number {
  if (!Number.isFinite(value)) return 1;
  return Math.min(3, Math.max(1, Math.trunc(value)));
}

function slotsFromDefaults(defaults: RemixLabDefaults): DraftSlot[] {
  const slots: DraftSlot[] = [
    {
      base_url: defaults.remix_base_url,
      model: defaults.remix_model,
      api_key: "",
      reasoning_effort: defaults.remix_reasoning_effort,
      run_count: 1,
      preset_index: null,
      keyConfigured: defaults.remix_api_key_configured,
    },
  ];
  for (const preset of defaults.presets) {
    if (slots.length >= MAX_SLOTS) break;
    slots.push({
      base_url: preset.base_url,
      model: preset.model,
      api_key: "",
      reasoning_effort: preset.reasoning_effort,
      run_count: clampRunCount(preset.run_count || 1),
      preset_index: preset.preset_index,
      keyConfigured: preset.api_key_configured,
    });
  }
  return slots;
}

function firstSentences(script: string, count = 3): string {
  const parts = script.split(/(?<=[。！？])/).map((part) => part.trim()).filter(Boolean);
  return parts.slice(0, count).join("");
}

function parseTitles(titlesJSON: string): string[] {
  try {
    const parsed = JSON.parse(titlesJSON) as unknown;
    return Array.isArray(parsed) ? parsed.map((item) => String(item)) : [];
  } catch {
    return [];
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case "queued":
      return "排队中";
    case "running":
      return "生成中";
    case "completed":
      return "已完成";
    case "failed":
      return "失败";
    default:
      return status;
  }
}

function statusTone(status: string): "ok" | "danger" | "live" | "idle" {
  if (status === "completed") return "ok";
  if (status === "failed") return "danger";
  if (status === "running" || status === "queued") return "live";
  return "idle";
}

function StatusSeal({ status }: { status: string }) {
  return (
    <span className={`remix-lab-seal remix-lab-seal--${statusTone(status)}`} data-status={status}>
      {statusLabel(status)}
    </span>
  );
}

function toCreateSlots(slots: DraftSlot[]): RemixLabCreateSlot[] {
  return slots.map((slot) => {
    const body: RemixLabCreateSlot = {
      base_url: slot.base_url,
      model: slot.model,
      api_key: slot.api_key,
      reasoning_effort: slot.reasoning_effort,
      run_count: clampRunCount(slot.run_count),
    };
    if (slot.preset_index != null) body.preset_index = slot.preset_index;
    return body;
  });
}

export function RemixLabPage({ api, experimentID, onNavigate }: RemixLabPageProps) {
  const [defaults, setDefaults] = useState<RemixLabDefaults | null>(null);
  const [history, setHistory] = useState<RemixLabExperimentSummary[]>([]);
  const [source, setSource] = useState("");
  const [slots, setSlots] = useState<DraftSlot[]>([]);
  const [experiment, setExperiment] = useState<RemixLabExperiment | null>(null);
  const [message, setMessage] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [adoptingRunID, setAdoptingRunID] = useState<string | null>(null);
  const [projectOptions, setProjectOptions] = useState<ProjectOption[] | null>(null);
  const [adopted, setAdopted] = useState<{ runID: string; projectID: string } | null>(null);
  const pendingComments = useRef<Map<string, { timer: number; comment: string }>>(new Map());
  const saveCommentRef = useRef<(runID: string, comment: string) => void>(() => {});

  const flushPendingComments = () => {
    const pending = [...pendingComments.current.entries()];
    pendingComments.current.clear();
    for (const [runID, entry] of pending) {
      window.clearTimeout(entry.timer);
      saveCommentRef.current(runID, entry.comment);
    }
  };

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const nextDefaults = await fetchRemixLabDefaults(api);
        if (cancelled) return;
        setDefaults(nextDefaults);
        setSlots(slotsFromDefaults(nextDefaults));
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "进化台加载失败。");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const nextHistory = await fetchRemixLabExperiments(api);
        if (!cancelled) setHistory(nextHistory);
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "进化台实验列表读取失败。");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api, experimentID]);

  useEffect(() => {
    if (!experimentID) {
      setExperiment(null);
      return;
    }
    let cancelled = false;
    let timer: number | undefined;

    const load = async () => {
      try {
        const detail = await fetchRemixLabExperiment(api, experimentID);
        if (cancelled) return;
        setExperiment(detail);
        if (detail.status === "running") {
          timer = window.setTimeout(() => {
            void load();
          }, 2000);
        }
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "实验详情读取失败。");
      }
    };

    void load();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
      flushPendingComments();
    };
  }, [api, experimentID]);

  const updateSlot = (index: number, patch: Partial<DraftSlot>) => {
    setSlots((current) =>
      current.map((slot, slotIndex) => (slotIndex === index ? { ...slot, ...patch } : slot)),
    );
  };

  const addSlot = () => {
    if (slots.length >= MAX_SLOTS) return;
    setSlots((current) => [
      ...current,
      {
        base_url: defaults?.remix_base_url || "",
        model: "",
        api_key: "",
        reasoning_effort: defaults?.remix_reasoning_effort || "",
        run_count: 1,
        preset_index: null,
        keyConfigured: false,
      },
    ]);
  };

  const removeSlot = (index: number) => {
    setSlots((current) => (current.length <= 1 ? current : current.filter((_, slotIndex) => slotIndex !== index)));
  };

  const startExperiment = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setMessage("");
    try {
      const created = await createRemixLabExperiment(api, source, toCreateSlots(slots));
      const summary: RemixLabExperimentSummary = {
        id: created.id,
        title: created.title,
        prompt_stamp: created.prompt_stamp,
        status: created.status,
        created_at: created.created_at,
        updated_at: created.updated_at,
      };
      setHistory((current) => [summary, ...current.filter((item) => item.id !== created.id)]);
      onNavigate(`/remix-lab/${created.id}`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "开始二创失败。");
    } finally {
      setSubmitting(false);
    }
  };

  const saveComment = async (runID: string, comment: string) => {
    try {
      await patchRemixLabRunComment(api, runID, comment);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "批注保存失败。");
    }
  };
  saveCommentRef.current = (runID, comment) => {
    void saveComment(runID, comment);
  };

  const scheduleCommentSave = (runID: string, comment: string) => {
    const existing = pendingComments.current.get(runID);
    if (existing !== undefined) window.clearTimeout(existing.timer);
    const timer = window.setTimeout(() => {
      pendingComments.current.delete(runID);
      void saveComment(runID, comment);
    }, 400);
    pendingComments.current.set(runID, { timer, comment });
  };

  const flushComment = (runID: string, comment: string) => {
    const existing = pendingComments.current.get(runID);
    if (existing !== undefined) {
      window.clearTimeout(existing.timer);
      pendingComments.current.delete(runID);
    }
    void saveComment(runID, comment);
  };

  const openAdoptPicker = async (runID: string) => {
    setAdoptingRunID(runID);
    setMessage("");
    try {
      const response = await api("/api/projects");
      if (!response.ok) throw new Error("项目列表读取失败。");
      const projects = (await response.json()) as Array<{ id: string; title: string }>;
      setProjectOptions(projects.map((project) => ({ id: project.id, title: project.title })));
    } catch (error) {
      setAdoptingRunID(null);
      setMessage(error instanceof Error ? error.message : "项目列表读取失败。");
    }
  };

  const confirmAdopt = async (projectID: string) => {
    if (!adoptingRunID) return;
    try {
      await adoptRemixLabRun(api, adoptingRunID, projectID);
      setAdopted({ runID: adoptingRunID, projectID });
      setAdoptingRunID(null);
      setProjectOptions(null);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "采用到项目失败。");
    }
  };

  const renderRunCard = (run: RemixLabRunView) => {
    const titles = parseTitles(run.titles_json);
    const canAdopt = run.status === "completed" && Boolean(run.continuous_script.trim());
    const preview = firstSentences(run.continuous_script);
    return (
      <article key={run.id} className="remix-lab-run">
        <div className="remix-lab-run__meta">
          <strong>运行 {run.run_index}</strong>
          <StatusSeal status={run.status} />
        </div>
        {preview ? <p className="remix-lab-run__preview">{preview}</p> : null}
        {run.continuous_script ? (
          <pre className="remix-lab-run__script">{run.continuous_script}</pre>
        ) : null}
        {titles.length > 0 ? (
          <ul className="remix-lab-run__titles">
            {titles.map((title) => (
              <li key={title}>{title}</li>
            ))}
          </ul>
        ) : null}
        {run.error_message ? <p className="remix-lab-run__error">{run.error_message}</p> : null}
        <label className="remix-lab-run__comment">
          批注
          <textarea
            aria-label="批注"
            defaultValue={run.comment}
            onChange={(event) => scheduleCommentSave(run.id, event.target.value)}
            onBlur={(event) => flushComment(run.id, event.target.value)}
            rows={3}
            placeholder="开头、数字、课尾，写在这里"
          />
        </label>
        <div className="remix-lab-run__actions">
          <button
            type="button"
            disabled={!canAdopt}
            onClick={() => void openAdoptPicker(run.id)}
          >
            采用到项目
          </button>
          {adopted?.runID === run.id ? (
            <span className="remix-lab-run__adopted">
              已采用，可去项目生成口播{" "}
              <button type="button" onClick={() => onNavigate(`/projects/${adopted.projectID}`)}>
                打开项目
              </button>
            </span>
          ) : null}
        </div>
        {adoptingRunID === run.id && projectOptions ? (
          <div className="remix-lab-adopt">
            <p>选择要采用到的项目</p>
            <ul>
              {projectOptions.map((project) => (
                <li key={project.id}>
                  <button type="button" onClick={() => void confirmAdopt(project.id)}>
                    {project.title}
                  </button>
                </li>
              ))}
            </ul>
            <button
              type="button"
              className="header-button"
              onClick={() => {
                setAdoptingRunID(null);
                setProjectOptions(null);
              }}
            >
              取消
            </button>
          </div>
        ) : null}
      </article>
    );
  };

  const renderGroupedRuns = (detail: RemixLabExperiment) => {
    const slotsOrdered = [...detail.slots].sort((a, b) => a.sort_index - b.sort_index);
    const knownSlotIDs = new Set(slotsOrdered.map((slot) => slot.id));
    const orphanRuns = detail.runs.filter((run) => !knownSlotIDs.has(run.slot_id));
    return (
      <div className="remix-lab-runs">
        {slotsOrdered.map((slot) => {
          const slotRuns = detail.runs
            .filter((run) => run.slot_id === slot.id)
            .sort((a, b) => a.run_index - b.run_index);
          const heading = slot.label || slot.model || slot.id;
          return (
            <section key={slot.id} className="remix-lab-slot-group" aria-label={heading}>
              <h3>{heading}</h3>
              {slotRuns.map(renderRunCard)}
            </section>
          );
        })}
        {orphanRuns.length > 0 ? (
          <section className="remix-lab-slot-group" aria-label="其他运行">
            <h3>其他运行</h3>
            {orphanRuns.map(renderRunCard)}
          </section>
        ) : null}
      </div>
    );
  };

  return (
    <div className="remix-lab">
      <header>
        <div className="remix-lab-brand">
          <span className="remix-lab-mark" aria-hidden="true">
            <PenLine size={18} strokeWidth={2} />
          </span>
          <div>
            <span className="eyebrow">视频生产控制台</span>
            <h1>进化台</h1>
            <p>对照几家模型的口播，批注留下，提示词在对话里改。</p>
          </div>
        </div>
        <div className="remix-lab-header-actions">
          <button type="button" className="header-button remix-lab-icon-btn" onClick={() => onNavigate("/projects")}>
            <ArrowLeft size={16} strokeWidth={2} />
            返回风景混剪
          </button>
        </div>
      </header>
      <div className="remix-lab__body">
        <aside className="remix-lab__history">
          <div className="remix-lab__history-head">
            <h2>历史实验</h2>
            {experimentID ? (
              <button type="button" className="header-button" onClick={() => onNavigate("/remix-lab")}>
                新开实验
              </button>
            ) : null}
          </div>
          {history.length === 0 ? <p className="remix-lab__history-empty">还没有实验。右侧贴原文，选模型开跑。</p> : null}
          <ul>
            {history.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  className={item.id === experimentID ? "selected" : undefined}
                  onClick={() => onNavigate(`/remix-lab/${item.id}`)}
                >
                  <strong>{item.title || item.id}</strong>
                  <span className="remix-lab__history-meta">
                    <StatusSeal status={item.status} />
                    {item.prompt_stamp ? <span className="remix-lab-chip">{item.prompt_stamp}</span> : null}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <main className="remix-lab__main">
          {message ? <p className="notice" role="status">{message}</p> : null}
          {experimentID ? (
            experiment ? (
              <section className="remix-lab__detail">
                <div className="remix-lab-detail-head">
                  <div>
                    <h2>{experiment.title || "实验详情"}</h2>
                    <div className="remix-lab-detail-meta">
                      <StatusSeal status={experiment.status} />
                      {experiment.prompt_stamp ? (
                        <span className="remix-lab-chip">{experiment.prompt_stamp}</span>
                      ) : null}
                    </div>
                  </div>
                </div>
                <div className="remix-lab-paper">
                  <p className="remix-lab-paper__title">对标原文</p>
                  <label className="remix-lab__source">
                    原文
                    <textarea
                      aria-label="原文"
                      value={experiment.source_text}
                      readOnly
                      rows={8}
                    />
                  </label>
                </div>
                {renderGroupedRuns(experiment)}
              </section>
            ) : (
              <p className="remix-lab-muted">正在读取实验…</p>
            )
          ) : (
            <form className="remix-lab__compose" onSubmit={(event) => void startExperiment(event)}>
              <div className="remix-lab-paper">
                <p className="remix-lab-paper__title">对标原文</p>
                <label>
                  原文
                  <textarea
                    aria-label="原文"
                    value={source}
                    onChange={(event) => setSource(event.target.value)}
                    rows={8}
                    required
                    placeholder="粘贴完整对标口播"
                  />
                </label>
              </div>
              <div className="remix-lab-slots">
                {slots.map((slot, index) => (
                  <fieldset key={index} className="remix-lab-slot">
                    <legend>
                      模型槽 {index + 1}
                      <button
                        type="button"
                        className="header-button remix-lab-icon-btn"
                        disabled={slots.length <= 1}
                        onClick={() => removeSlot(index)}
                        aria-label={`删除模型槽 ${index + 1}`}
                      >
                        <Trash2 size={14} strokeWidth={2} />
                        删除
                      </button>
                    </legend>
                    <div className="remix-lab-slot__fields">
                      <label>
                        Base URL
                        <input
                          value={slot.base_url}
                          onChange={(event) => updateSlot(index, { base_url: event.target.value })}
                        />
                      </label>
                      <label>
                        模型
                        <input
                          value={slot.model}
                          onChange={(event) => updateSlot(index, { model: event.target.value })}
                          required
                        />
                      </label>
                      <label>
                        思考强度
                        <input
                          value={slot.reasoning_effort}
                          onChange={(event) =>
                            updateSlot(index, { reasoning_effort: event.target.value })
                          }
                        />
                      </label>
                      <label>
                        API Key
                        <input
                          type="password"
                          value={slot.api_key}
                          placeholder={slot.keyConfigured ? "已配置" : ""}
                          onChange={(event) => updateSlot(index, { api_key: event.target.value })}
                          autoComplete="off"
                        />
                      </label>
                      <label>
                        运行次数
                        <input
                          type="number"
                          min={1}
                          max={3}
                          value={slot.run_count}
                          onChange={(event) =>
                            updateSlot(index, { run_count: clampRunCount(Number(event.target.value)) })
                          }
                        />
                      </label>
                    </div>
                  </fieldset>
                ))}
              </div>
              <div className="remix-lab__compose-actions">
                <button
                  type="button"
                  className="header-button remix-lab-icon-btn"
                  disabled={slots.length >= MAX_SLOTS}
                  onClick={addSlot}
                >
                  <Plus size={16} strokeWidth={2} />
                  添加模型槽
                </button>
                <button className="remix-lab-start" type="submit" disabled={submitting || !source.trim()}>
                  开始二创
                </button>
              </div>
            </form>
          )}
        </main>
      </div>
    </div>
  );
}
