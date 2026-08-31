import { useEffect, useRef, useState } from "react";
import { Columns2, FolderInput, Plus, Save, Trash2, Undo2, Workflow, X } from "lucide-react";
import {
  adoptRemixLabRun,
  patchRemixLabRunComment,
  reworkRemixLabRun,
  saveRemixLabRunPackage,
  type RemixLabApi,
  type RemixLabPackageInput,
  type RemixLabReviewRecord,
  type RemixLabRunView,
} from "./api";

type AccountOption = { id: string; name: string; status: string };
type ProjectOption = { id: string; title: string };

type RunWorkbenchProps = {
  api: RemixLabApi;
  run: RemixLabRunView;
  onMessage: (text: string) => void;
  /** 保存/打回/导入之后让父级重新拉实验详情。 */
  onChanged: () => void;
  onNavigate: (href: string) => void;
  /** 打开这次运行的工作流视图（n8n式节点分解）。 */
  onOpenFlow: () => void;
  /** 确认闸门放行 / 生产失败续跑（accountID 可空=沿用已选账号）。 */
  onProduce?: (accountID: string) => void;
};

const PRODUCE_STEP_LABEL: Record<string, string> = {
  confirm: "等待确认",
  project: "建项目导入",
  spoken: "口播稿",
  captions: "字幕关键词",
  narration: "配音",
  montage: "混剪草稿",
  done: "完成",
};

type ListField = "titles" | "short_titles" | "descriptions" | "topics";

const SHORT_TITLE_SLOTS = ["板标题（视频板面主标题）", "副标题", "备选"];

function parsePackage(run: RemixLabRunView): RemixLabPackageInput {
  // 旧运行（迁移前）没有 package_json，退回标题镜像列，保证还能编辑导入。
  const fallback: RemixLabPackageInput = {
    continuous_script: run.continuous_script ?? "",
    titles: parseTitlesJSON(run.titles_json ?? ""),
    short_titles: [],
    descriptions: [],
    topics: [],
    cta: "",
  };
  const raw = (run.package_json ?? "").trim();
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw) as Partial<RemixLabPackageInput>;
    return {
      continuous_script:
        typeof parsed.continuous_script === "string" && parsed.continuous_script.trim()
          ? parsed.continuous_script
          : fallback.continuous_script,
      titles: toStringList(parsed.titles),
      short_titles: toStringList(parsed.short_titles),
      descriptions: toStringList(parsed.descriptions),
      topics: toStringList(parsed.topics),
      cta: typeof parsed.cta === "string" ? parsed.cta : "",
    };
  } catch {
    return fallback;
  }
}

function parseTitlesJSON(titlesJSON: string): string[] {
  try {
    const parsed = JSON.parse(titlesJSON) as unknown;
    return Array.isArray(parsed) ? parsed.map((item) => String(item)) : [];
  } catch {
    return [];
  }
}

function toStringList(value: unknown): string[] {
  return Array.isArray(value) ? value.map((item) => String(item)) : [];
}

function parseReview(json: string): RemixLabReviewRecord | null {
  const raw = (json ?? "").trim();
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as RemixLabReviewRecord;
    return parsed.verdict ? parsed : null;
  } catch {
    return null;
  }
}

function parseDraftScript(json: string): string {
  const raw = (json ?? "").trim();
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw) as { continuous_script?: string };
    return typeof parsed.continuous_script === "string" ? parsed.continuous_script.trim() : "";
  } catch {
    return "";
  }
}

function runeCount(text: string): number {
  return [...text].length;
}

function reviewVerdictLabel(verdict: string): string {
  switch (verdict) {
    case "pass":
      return "审稿通过";
    case "fixed":
      return "审稿已修订";
    case "skipped":
      return "审稿跳过";
    case "error":
      return "审稿失败";
    default:
      return `审稿：${verdict}`;
  }
}

function reviewVerdictTone(verdict: string): string {
  switch (verdict) {
    case "pass":
      return "ok";
    case "fixed":
      return "live";
    default:
      return "warn";
  }
}

export function RunWorkbench({ api, run, onMessage, onChanged, onNavigate, onOpenFlow, onProduce }: RunWorkbenchProps) {
  const [pkg, setPkg] = useState<RemixLabPackageInput>(() => parsePackage(run));
  const [loadedKey, setLoadedKey] = useState(() => `${run.id}:${run.package_json}`);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [comment, setComment] = useState(run.comment);
  const [reworking, setReworking] = useState(false);
  const [compareOpen, setCompareOpen] = useState(true);
  const [importOpen, setImportOpen] = useState(false);
  const [accounts, setAccounts] = useState<AccountOption[]>([]);
  const [importAccountID, setImportAccountID] = useState("");
  const [importTitle, setImportTitle] = useState("");
  const [importing, setImporting] = useState(false);
  const [linkedProjectID, setLinkedProjectID] = useState(run.adopted_project_id);
  const [adoptOpen, setAdoptOpen] = useState(false);
  const [projects, setProjects] = useState<ProjectOption[]>([]);
  const [produceAccounts, setProduceAccounts] = useState<AccountOption[]>([]);
  const [produceAccountID, setProduceAccountID] = useState("");
  const commentTimer = useRef<number | undefined>(undefined);
  const pendingComment = useRef<string | null>(null);

  const production = run.production ?? null;

  // 确认闸门待放行且没预选账号时，把账号列表拉出来供选择。
  useEffect(() => {
    if (!production || production.status !== "waiting_confirm" || production.account_id) return;
    let cancelled = false;
    void (async () => {
      try {
        const response = await api("/api/accounts");
        if (!response.ok) return;
        const all = (await response.json()) as AccountOption[];
        if (cancelled) return;
        const active = all.filter((account) => account.status === "active");
        setProduceAccounts(active);
        if (active.length > 0) setProduceAccountID((current) => current || active[0].id);
      } catch {
        // 拉不到账号时按钮提示会兜底
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api, production]);

  // 打回完成或别处改动带回新定稿时，只要本地没有未保存的编辑就自动载入。
  const externalKey = `${run.id}:${run.package_json}`;
  useEffect(() => {
    if (externalKey === loadedKey) return;
    if (!dirty) {
      setPkg(parsePackage(run));
      setLoadedKey(externalKey);
    }
  }, [externalKey, loadedKey, dirty, run]);

  // 卸载或切换运行时，把还没落库的批注立刻发出去，不丢字。
  useEffect(
    () => () => {
      window.clearTimeout(commentTimer.current);
      if (pendingComment.current !== null) {
        const value = pendingComment.current;
        pendingComment.current = null;
        void patchRemixLabRunComment(api, run.id, value).catch(() => undefined);
      }
    },
    [api, run.id],
  );

  const review = parseReview(run.review_json);
  const v1Script = parseDraftScript(run.draft_v1_json);
  const hasCompare = Boolean(v1Script) && v1Script !== pkg.continuous_script.trim();
  const staleExternal = externalKey !== loadedKey && dirty;

  const update = (patch: Partial<RemixLabPackageInput>) => {
    setPkg((current) => ({ ...current, ...patch }));
    setDirty(true);
  };

  const updateListItem = (field: ListField, index: number, value: string) => {
    setPkg((current) => {
      const next = [...current[field]];
      next[index] = value;
      return { ...current, [field]: next };
    });
    setDirty(true);
  };

  const addListItem = (field: ListField) => {
    setPkg((current) => ({ ...current, [field]: [...current[field], ""] }));
    setDirty(true);
  };

  const removeListItem = (field: ListField, index: number) => {
    setPkg((current) => ({ ...current, [field]: current[field].filter((_, i) => i !== index) }));
    setDirty(true);
  };

  const normalizedPackage = (): RemixLabPackageInput => ({
    continuous_script: pkg.continuous_script.trim(),
    titles: cleanList(pkg.titles),
    short_titles: cleanList(pkg.short_titles),
    descriptions: cleanList(pkg.descriptions),
    topics: cleanList(pkg.topics),
    cta: pkg.cta.trim(),
  });

  const savePackage = async (): Promise<boolean> => {
    if (saving) return false;
    const body = normalizedPackage();
    if (runeCount(body.continuous_script) < 40) {
      onMessage("正文太短（至少40字），检查后再保存。");
      return false;
    }
    if (body.short_titles.length === 0) {
      onMessage("第1条短标题是混剪板面的标题，必须填。");
      return false;
    }
    setSaving(true);
    try {
      await saveRemixLabRunPackage(api, run.id, body);
      setDirty(false);
      setLoadedKey(`${run.id}:stale-after-save`);
      onChanged();
      onMessage("定稿已保存。");
      return true;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "定稿保存失败。");
      return false;
    } finally {
      setSaving(false);
    }
  };

  const scheduleComment = (value: string) => {
    setComment(value);
    pendingComment.current = value;
    window.clearTimeout(commentTimer.current);
    commentTimer.current = window.setTimeout(() => {
      pendingComment.current = null;
      void patchRemixLabRunComment(api, run.id, value).catch((error: unknown) => {
        onMessage(error instanceof Error ? error.message : "批注保存失败。");
      });
    }, 400);
  };

  const flushComment = (value: string) => {
    pendingComment.current = null;
    window.clearTimeout(commentTimer.current);
    void patchRemixLabRunComment(api, run.id, value).catch((error: unknown) => {
      onMessage(error instanceof Error ? error.message : "批注保存失败。");
    });
  };

  const startRework = async () => {
    if (reworking) return;
    const annotations = comment.trim();
    if (!annotations) {
      onMessage("先在批注里写清楚要改什么，再打回重做。");
      return;
    }
    if (dirty) {
      const saved = await savePackage();
      if (!saved) return;
    }
    setReworking(true);
    try {
      await reworkRemixLabRun(api, run.id, annotations);
      onMessage("已打回给审稿agent，按批注返工中，完成后自动回填。");
      onChanged();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "打回重做提交失败。");
    } finally {
      setReworking(false);
    }
  };

  const openImport = async () => {
    try {
      const response = await api("/api/accounts");
      if (!response.ok) throw new Error("账号列表读取失败。");
      const all = (await response.json()) as AccountOption[];
      const active = all.filter((account) => account.status === "active");
      if (active.length === 0) {
        onMessage("没有可用账号，先去控制台创建账号。");
        return;
      }
      setAccounts(active);
      setImportAccountID(active[0].id);
      setImportTitle((pkg.short_titles[0] || pkg.titles[0] || "二创成稿").trim().slice(0, 40));
      setAdoptOpen(false);
      setImportOpen(true);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "账号列表读取失败。");
    }
  };

  // 导入混剪 = 新建项目 + 把整份写手 JSON 交给项目的文案端点（后端自动拆
  // 正文并写 publishing_package.json，混剪板标题读 short_titles[0]/[1]）。
  const confirmImport = async () => {
    if (importing) return;
    if (!importAccountID) return;
    if (dirty) {
      const saved = await savePackage();
      if (!saved) return;
    }
    const body = normalizedPackage();
    if (body.short_titles.length === 0) {
      onMessage("第1条短标题是混剪板面的标题，必须填。");
      return;
    }
    setImporting(true);
    try {
      const createResponse = await api("/api/projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ account_id: importAccountID, title: importTitle.trim() }),
      });
      if (!createResponse.ok) throw new Error("项目创建失败。");
      const project = (await createResponse.json()) as { id: string };
      const form = new FormData();
      form.append(
        "file",
        new Blob([JSON.stringify(body)], { type: "text/plain" }),
        "continuous-script.txt",
      );
      const uploadResponse = await api(`/api/projects/${project.id}/assets/continuous_script`, {
        method: "POST",
        body: form,
      });
      if (!uploadResponse.ok) throw new Error("文案写入项目失败。");
      await adoptRemixLabRun(api, run.id, project.id).catch(() => undefined);
      setLinkedProjectID(project.id);
      setImportOpen(false);
      onChanged();
      onMessage("已导入混剪项目：口播稿会自动生成，配音和混剪去项目里点。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "导入混剪失败。");
    } finally {
      setImporting(false);
    }
  };

  const openAdopt = async () => {
    try {
      const response = await api("/api/projects");
      if (!response.ok) throw new Error("项目列表读取失败。");
      const list = (await response.json()) as ProjectOption[];
      setProjects(list.map((project) => ({ id: project.id, title: project.title })));
      setImportOpen(false);
      setAdoptOpen(true);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "项目列表读取失败。");
    }
  };

  const confirmAdopt = async (projectID: string) => {
    try {
      await adoptRemixLabRun(api, run.id, projectID);
      setLinkedProjectID(projectID);
      setAdoptOpen(false);
      onChanged();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "采用到项目失败。");
    }
  };

  const renderListEditor = (
    field: ListField,
    label: string,
    options?: { rows?: number; slotLabels?: string[]; hint?: string },
  ) => (
    <div className="remix-lab-pkg__section">
      <div className="remix-lab-pkg__head">
        <h4>
          {label} <span className="remix-lab-muted">{pkg[field].length}条</span>
        </h4>
        <button
          type="button"
          className="header-button remix-lab-icon-btn"
          onClick={() => addListItem(field)}
          aria-label={`添加${label}`}
        >
          <Plus size={13} strokeWidth={2} />
          添加
        </button>
      </div>
      {options?.hint ? <p className="remix-lab-muted remix-lab-pkg__hint">{options.hint}</p> : null}
      <ul className="remix-lab-pkg__list">
        {pkg[field].map((item, index) => (
          <li key={`${field}-${index}`}>
            {options?.slotLabels?.[index] ? (
              <span className="remix-lab-pkg__slot">{options.slotLabels[index]}</span>
            ) : null}
            {options?.rows && options.rows > 1 ? (
              <textarea
                aria-label={`${label} ${index + 1}`}
                value={item}
                rows={options.rows}
                onChange={(event) => updateListItem(field, index, event.target.value)}
              />
            ) : (
              <input
                aria-label={`${label} ${index + 1}`}
                value={item}
                onChange={(event) => updateListItem(field, index, event.target.value)}
              />
            )}
            <button
              type="button"
              className="header-button remix-lab-icon-btn"
              onClick={() => removeListItem(field, index)}
              aria-label={`删除${label} ${index + 1}`}
            >
              <Trash2 size={13} strokeWidth={2} />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );

  return (
    <article className="remix-lab-run remix-lab-workbench">
      <div className="remix-lab-run__meta">
        <div className="remix-lab-run__meta-left">
          <strong>运行 {run.run_index}</strong>
          {run.prompt_name ? <span className="remix-lab-chip">{run.prompt_name}</span> : null}
          {review ? (
            <span className={`remix-lab-chip remix-lab-chip--review-${reviewVerdictTone(review.verdict)}`}>
              {reviewVerdictLabel(review.verdict)}
              {review.round > 1 ? ` · 第${review.round}轮` : ""}
            </span>
          ) : null}
        </div>
        <span className="remix-lab-seal remix-lab-seal--ok" data-status={run.status}>
          已完成
        </span>
      </div>

      {review && (review.summary || review.error || (review.issues?.length ?? 0) > 0) ? (
        <details className="remix-lab-review" open={review.verdict === "fixed" || review.verdict === "error"}>
          <summary>
            审稿结论：{review.summary || review.error || reviewVerdictLabel(review.verdict)}
          </summary>
          {review.error ? <p className="remix-lab-run__error">{review.error}</p> : null}
          {(review.issues?.length ?? 0) > 0 ? (
            <ol className="remix-lab-review__issues">
              {review.issues?.map((issue, index) => (
                <li key={index}>
                  <strong>{issue.problem}</strong>
                  {issue.where ? <span className="remix-lab-review__where">「{issue.where}」</span> : null}
                  {issue.fix ? <p>{issue.fix}</p> : null}
                </li>
              ))}
            </ol>
          ) : null}
        </details>
      ) : null}

      {staleExternal ? (
        <p className="notice" role="status">
          后台带回了新定稿，但你有未保存的编辑。保存会覆盖后台版本；想要后台版本就刷新页面。
        </p>
      ) : null}

      {production && onProduce ? (
        <div className={`remix-lab-produce-strip remix-lab-produce-strip--${production.status}`}>
          {production.status === "waiting_confirm" ? (
            <>
              <strong>确认闸门</strong>
              <span className="remix-lab-muted">定稿满意后放行：建项目 → 口播稿 → 配音 → 混剪，一步到剪映草稿。</span>
              {!production.account_id ? (
                <select
                  aria-label="混剪账号"
                  value={produceAccountID}
                  onChange={(event) => setProduceAccountID(event.target.value)}
                >
                  {produceAccounts.length === 0 ? <option value="">读取账号…</option> : null}
                  {produceAccounts.map((account) => (
                    <option key={account.id} value={account.id}>
                      {account.name}
                    </option>
                  ))}
                </select>
              ) : null}
              <button
                type="button"
                className="remix-lab-start"
                onClick={() => onProduce(production.account_id || produceAccountID)}
              >
                确认开始混剪
              </button>
            </>
          ) : null}
          {production.status === "running" ? (
            <>
              <strong>混剪进行中</strong>
              <span className="remix-lab-muted">
                当前环节：{PRODUCE_STEP_LABEL[production.step] ?? production.step} · 进度看「工作流视图」
              </span>
              {production.project_id ? (
                <button type="button" className="header-button" onClick={() => onNavigate(`/remix-lab/${run.experiment_id}`)}>
                  回到工作流
                </button>
              ) : null}
            </>
          ) : null}
          {production.status === "failed" ? (
            <>
              <strong>生产失败</strong>
              <span className="remix-lab-run__error remix-lab-produce-strip__error">{production.error}</span>
              <button type="button" className="remix-lab-start" onClick={() => onProduce("")}>
                重试续跑
              </button>
              {production.project_id ? (
                <button type="button" className="header-button" onClick={() => onNavigate(`/remix-lab/${run.experiment_id}`)}>
                  回到工作流
                </button>
              ) : null}
            </>
          ) : null}
          {production.status === "completed" ? (
            <>
              <strong>剪映草稿已生成</strong>
              <span className="remix-lab-muted">全链路完成：口播、配音、混剪都在项目里。</span>
              {production.project_id ? (
                <button type="button" className="remix-lab-start" onClick={() => onNavigate(`/remix-lab/${run.experiment_id}`)}>
                  回到工作流看草稿
                </button>
              ) : null}
            </>
          ) : null}
        </div>
      ) : null}

      <div className="remix-lab-workbench__toolbar">
        <div className="remix-lab-workbench__toolbar-left">
          <button
            type="button"
            className="header-button remix-lab-icon-btn"
            onClick={onOpenFlow}
          >
            <Workflow size={14} strokeWidth={2} />
            工作流视图
          </button>
          {hasCompare ? (
            <button
              type="button"
              className="header-button remix-lab-icon-btn"
              onClick={() => setCompareOpen((open) => !open)}
            >
              <Columns2 size={14} strokeWidth={2} />
              {compareOpen ? "收起初稿对照" : "对照写手初稿"}
            </button>
          ) : null}
        </div>
        <span className="remix-lab-muted">
          正文 {runeCount(pkg.continuous_script.trim())} 字{dirty ? " · 有未保存的修改" : ""}
        </span>
      </div>

      <div className={hasCompare && compareOpen ? "remix-lab-workbench__versions remix-lab-workbench__versions--split" : "remix-lab-workbench__versions"}>
        {hasCompare && compareOpen ? (
          <div className="remix-lab-workbench__pane remix-lab-workbench__pane--v1">
            <h4>写手初稿（审稿前 · 只读）</h4>
            <pre className="remix-lab-run__script">{v1Script}</pre>
          </div>
        ) : null}
        <div className="remix-lab-workbench__pane">
          <h4>当前定稿（可编辑）</h4>
          <textarea
            aria-label={`运行 ${run.run_index} 正文`}
            className="remix-lab-workbench__script"
            value={pkg.continuous_script}
            rows={16}
            onChange={(event) => update({ continuous_script: event.target.value })}
          />
        </div>
      </div>

      <div className="remix-lab-pkg">
        {renderListEditor("short_titles", "短标题", {
          slotLabels: SHORT_TITLE_SLOTS,
          hint: "第1条会印在视频板面当主标题、第2条当副标题（都不超过15字）。",
        })}
        {renderListEditor("descriptions", "视频描述", { rows: 2 })}
      </div>

      {run.error_message ? <p className="remix-lab-run__error">{run.error_message}</p> : null}

      <label className="remix-lab-run__comment">
        批注
        <textarea
          aria-label="批注"
          value={comment}
          onChange={(event) => scheduleComment(event.target.value)}
          onBlur={(event) => flushComment(event.target.value)}
          rows={3}
          placeholder="开头、数字、课尾要改什么写在这里；打回重做会把这段话交给审稿agent逐条落实。"
        />
      </label>

      <div className="remix-lab-run__actions">
        <button
          type="button"
          className="remix-lab-start remix-lab-icon-btn"
          disabled={saving || !dirty}
          onClick={() => void savePackage()}
        >
          <Save size={14} strokeWidth={2} />
          {saving ? "保存中…" : "保存修改"}
        </button>
        <button
          type="button"
          className="header-button remix-lab-icon-btn"
          disabled={reworking}
          onClick={() => void startRework()}
        >
          <Undo2 size={14} strokeWidth={2} />
          {reworking ? "提交中…" : "按批注打回重做"}
        </button>
        <button
          type="button"
          className="header-button remix-lab-icon-btn"
          disabled={importing}
          onClick={() => void openImport()}
        >
          <FolderInput size={14} strokeWidth={2} />
          导入混剪（新建项目）
        </button>
        <button type="button" className="header-button" onClick={() => void openAdopt()}>
          采用到项目
        </button>
        {linkedProjectID ? (
          <span className="remix-lab-run__adopted">
            已进项目{" "}
            <button type="button" onClick={() => onNavigate(`/remix-lab/${run.experiment_id}`)}>
              回到工作流
            </button>
          </span>
        ) : null}
      </div>

      {importOpen ? (
        <div className="remix-lab-adopt remix-lab-import">
          <div className="remix-lab-import__head">
            <p>导入混剪：新建项目并写入定稿和全部发布字段</p>
            <button
              type="button"
              className="header-button remix-lab-icon-btn"
              aria-label="关闭导入"
              onClick={() => setImportOpen(false)}
            >
              <X size={13} strokeWidth={2} />
            </button>
          </div>
          <label>
            账号
            <select
              aria-label="导入账号"
              value={importAccountID}
              onChange={(event) => setImportAccountID(event.target.value)}
            >
              {accounts.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            项目名
            <input
              aria-label="项目名"
              value={importTitle}
              onChange={(event) => setImportTitle(event.target.value)}
            />
          </label>
          <button
            type="button"
            className="remix-lab-start"
            disabled={importing || !importAccountID}
            onClick={() => void confirmImport()}
          >
            {importing ? "导入中…" : "确认导入"}
          </button>
        </div>
      ) : null}

      {adoptOpen ? (
        <div className="remix-lab-adopt">
          <p>选择要采用到的项目</p>
          <ul>
            {projects.map((project) => (
              <li key={project.id}>
                <button type="button" onClick={() => void confirmAdopt(project.id)}>
                  {project.title}
                </button>
              </li>
            ))}
          </ul>
          <button type="button" className="header-button" onClick={() => setAdoptOpen(false)}>
            取消
          </button>
        </div>
      ) : null}
    </article>
  );
}

function cleanList(items: string[]): string[] {
  return items.map((item) => item.trim()).filter(Boolean);
}
