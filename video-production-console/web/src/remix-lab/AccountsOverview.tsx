import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { Eye, EyeOff, Play, RefreshCw, Trash2, X, FileText, PenLine, ShieldCheck } from "lucide-react";
import {
  fetchRemixLabExperiment,
  fetchRemixLabExperiments,
  runRemixLabWorkflow,
  type RemixLabApi,
  type RemixLabExperiment,
  type RemixLabExperimentSummary,
} from "./api";
import { ModelCombo, ModelMultiSelect } from "../ModelSelect";
import "./accounts-overview.css";
import type { Account } from "../types";

// 账号总览：一次贴原文、勾选账号批量开跑二创，并集中跟踪每个账号
// 最新实验的二创段与混剪段进度。点击账号行进入它的实验详情。

type AccountsOverviewProps = {
  api: RemixLabApi;
  accounts: Account[];
  onClose: () => void;
  /** 点账号行：切到该账号并打开其最新实验（无实验时只切账号回画布）。 */
  onOpenAccount: (accountID: string, experimentID?: string) => void;
  /** 账号停用成功后通知宿主把它从账号列表里拿掉。 */
  onAccountDeactivated: (accountID: string) => void;
  onAccountRenamed: (account: Account) => void;
  onMessage: (text: string) => void;
};

const EXPERIMENT_STATUS: Record<string, string> = {
  queued: "排队中",
  running: "二创中",
  completed: "二创完成",
  failed: "二创失败",
};

const PRODUCE_STEP: Record<string, string> = {
  project: "建项目",
  spoken: "口播稿",
  captions: "字幕关键词",
  narration: "配音",
  montage: "混剪草稿",
};

type LaunchResult = { ok: boolean; detail: string };

const HIDDEN_ACCOUNTS_KEY = "remix-lab:overview-hidden-accounts";

function readHiddenAccounts(): string[] {
  try {
    const saved: unknown = JSON.parse(window.localStorage.getItem(HIDDEN_ACCOUNTS_KEY) || "[]");
    return Array.isArray(saved) ? [...new Set(saved.filter((id): id is string => typeof id === "string" && id.length > 0))] : [];
  } catch {
    return [];
  }
}

function produceLabel(exp: RemixLabExperiment | undefined): string {
  if (!exp) return "";
  for (const run of exp.runs) {
    const production = run.production;
    if (!production) continue;
    switch (production.status) {
      case "waiting_confirm":
        return "待确认混剪";
      case "running":
        return `混剪中 · ${PRODUCE_STEP[production.step] ?? production.step}`;
      case "completed":
        return "混剪完成";
      case "failed":
        return `混剪失败 · ${PRODUCE_STEP[production.step] ?? production.step}`;
      default:
        return production.status;
    }
  }
  return "";
}

function runsLabel(exp: RemixLabExperiment | undefined): string {
  if (!exp || exp.runs.length === 0) return "";
  const done = exp.runs.filter((run) => run.status === "completed").length;
  return `${done}/${exp.runs.length} 稿`;
}

function formatTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function AccountsOverview({ api, accounts, onClose, onOpenAccount, onAccountDeactivated, onAccountRenamed, onMessage }: AccountsOverviewProps) {
  const [editingID, setEditingID] = useState("");
  const [newName, setNewName] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [renameError, setRenameError] = useState("");
  const rename = async (account: Account) => {
    if (!newName.trim() || renaming) return;
    setRenaming(true);
    setRenameError("");
    try {
      const response = await api(`/api/accounts/${account.id}`, {
        method: "PATCH", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: newName.trim() }),
      });
      if (!response.ok) throw new Error(response.status === 409 ? "已有同名账号，请换一个名称。" : `改名失败：${response.status}`);
      onAccountRenamed(await response.json() as Account);
      setEditingID("");
      onMessage("账号名称已更新。");
    } catch (error) {
      setRenameError(error instanceof Error ? error.message : "账号改名失败。");
    } finally {
      setRenaming(false);
    }
  };
  const [source, setSource] = useState("");
  const [runCount, setRunCount] = useState(1);
  const [auto, setAuto] = useState(false);
  // 空 = 用默认档（写手节点覆盖 > 模型配置槽1 > 全局二创设置）；多选 = 每个
  // 账号里各模型各出一稿并行，快的先看、慢的再比。
  const [models, setModels] = useState<string[]>([]);
  const [reviewMode, setReviewMode] = useState("inherit");
  const [reviewModel, setReviewModel] = useState("");
  const [reviewEffort, setReviewEffort] = useState("high");
  const [reviewFast, setReviewFast] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [launching, setLaunching] = useState(false);
  const [launchResults, setLaunchResults] = useState<Record<string, LaunchResult>>({});
  const [summaries, setSummaries] = useState<RemixLabExperimentSummary[]>([]);
  const [details, setDetails] = useState<Record<string, RemixLabExperiment>>({});
  const [refreshTick, setRefreshTick] = useState(0);
  // 展开某个账号，列出它全部实验（总览行默认只显示最新一条）。
  const [expandedAccount, setExpandedAccount] = useState("");
  const [hiddenIDs, setHiddenIDs] = useState<string[]>(readHiddenAccounts);
  const [hiddenManagerOpen, setHiddenManagerOpen] = useState(false);
  const visibleAccounts = useMemo(() => accounts.filter((account) => !hiddenIDs.includes(account.id)), [accounts, hiddenIDs]);
  const hiddenAccounts = useMemo(() => accounts.filter((account) => hiddenIDs.includes(account.id)), [accounts, hiddenIDs]);
  const selectedCount = visibleAccounts.filter((account) => selected.has(account.id)).length;

  const saveHiddenAccounts = (ids: string[]) => {
    setHiddenIDs(ids);
    try {
      window.localStorage.setItem(HIDDEN_ACCOUNTS_KEY, JSON.stringify(ids));
    } catch {
      onMessage("显示已调整，但浏览器未能保存，下次打开可能需要重新设置。");
    }
  };

  const hideAccount = (id: string) => {
    if (launching) return;
    saveHiddenAccounts([...new Set([...hiddenIDs, id])]);
    setSelected((current) => {
      const next = new Set(current);
      next.delete(id);
      return next;
    });
    if (expandedAccount === id) setExpandedAccount("");
  };

  const showAccount = (id: string) => {
    if (launching) return;
    saveHiddenAccounts(hiddenIDs.filter((hiddenID) => hiddenID !== id));
  };

  // 每个账号取最新实验（列表按更新时间倒序返回，这里再排一次保险）。
  const orderedSummaries = useMemo(
    () => [...summaries].sort((a, b) => (b.updated_at || "").localeCompare(a.updated_at || "")),
    [summaries],
  );
  const latestByAccount = useMemo(() => {
    const map = new Map<string, RemixLabExperimentSummary>();
    for (const item of orderedSummaries) {
      const id = item.account_id ?? "";
      if (id && !map.has(id)) map.set(id, item);
    }
    return map;
  }, [orderedSummaries]);
  const allByAccount = useMemo(() => {
    const map = new Map<string, RemixLabExperimentSummary[]>();
    for (const item of orderedSummaries) {
      const id = item.account_id ?? "";
      if (!id) continue;
      map.set(id, [...(map.get(id) ?? []), item]);
    }
    return map;
  }, [orderedSummaries]);

  const loadProgress = useCallback(async () => {
    try {
      const list = await fetchRemixLabExperiments(api);
      setSummaries(list);
      const ordered = [...list].sort((a, b) => (b.updated_at || "").localeCompare(a.updated_at || ""));
      const wanted = new Map<string, string>();
      for (const item of ordered) {
        const id = item.account_id ?? "";
        if (id && !wanted.has(id)) wanted.set(id, item.id);
      }
      const fetched = await Promise.all(
        [...wanted.values()].map((experimentID) =>
          fetchRemixLabExperiment(api, experimentID).catch(() => null),
        ),
      );
      const next: Record<string, RemixLabExperiment> = {};
      for (const detail of fetched) {
        if (detail) next[detail.id] = detail;
      }
      setDetails(next);
    } catch {
      // 轮询失败不打扰操作，下一轮再试
    }
  }, [api]);

  useEffect(() => {
    void loadProgress();
    const timer = window.setInterval(() => void loadProgress(), 4000);
    return () => window.clearInterval(timer);
  }, [loadProgress, refreshTick]);

  const [deactivating, setDeactivating] = useState("");
  // 停用是软删除：账号从列表消失，它名下的项目、实验、工作流都留着。
  const deactivate = async (account: Account) => {
    if (!window.confirm(`删除账号「${account.name}」？账号将从可选列表移除，历史项目和文案保留。`)) return;
    setDeactivating(account.id);
    try {
      const response = await api(`/api/accounts/${account.id}`, { method: "DELETE" });
      if (!response.ok) throw new Error(`删除失败：${response.status}`);
      onAccountDeactivated(account.id);
      setSelected((current) => {
        const next = new Set(current);
        next.delete(account.id);
        return next;
      });
      onMessage(`账号「${account.name}」已删除，历史项目和文案保留。`);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "账号删除失败。");
    } finally {
      setDeactivating("");
    }
  };

  const toggleAccount = (id: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const allSelected = visibleAccounts.length > 0 && visibleAccounts.every((item) => selected.has(item.id));
  const toggleAll = () => {
    setSelected(allSelected ? new Set() : new Set(visibleAccounts.map((item) => item.id)));
  };

  const launch = async () => {
    const text = source.trim();
    if (!text) {
      onMessage("先贴上要二创的爆款原文。");
      return;
    }
    const chosen = visibleAccounts.filter((item) => selected.has(item.id));
    if (chosen.length === 0) {
      onMessage("至少勾选一个账号。");
      return;
    }
    if (auto && (runCount !== 1 || models.length > 1)) {
      onMessage("全自动混剪只支持每号跑 1 次、单个模型。");
      return;
    }
    if (reviewMode === "custom" && !reviewModel.trim()) {
      onMessage("请选择或输入本次审稿模型。");
      return;
    }
    const reviewer = reviewMode === "inherit" ? undefined : reviewMode === "off" ? { model: "" } : {
      model: reviewModel.trim(), reasoning_effort: reviewEffort, service_tier: reviewFast ? "priority" : "default",
    };
    setLaunching(true);
    const results: Record<string, LaunchResult> = {};
    // 逐个账号提交：每个账号用自己的工作流快照各跑一个实验。
    for (const account of chosen) {
      try {
        const exp = await runRemixLabWorkflow(api, text, runCount, account.id, auto, models, reviewer);
        results[account.id] = { ok: true, detail: exp.id };
      } catch (error) {
        results[account.id] = {
          ok: false,
          detail: error instanceof Error ? error.message : "开跑失败",
        };
      }
      setLaunchResults({ ...results });
    }
    setLaunching(false);
    const okCount = Object.values(results).filter((item) => item.ok).length;
    const failCount = chosen.length - okCount;
    onMessage(
      failCount === 0
        ? `已为 ${okCount} 个账号开跑二创，进度看下方表格。`
        : `${okCount} 个账号开跑成功，${failCount} 个失败（行内有原因）。`,
    );
    setRefreshTick((n) => n + 1);
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="preview-modal remix-lab-modal remix-lab-modal--wide remix-lab-overview batch-studio notranslate"
        translate="no"
        role="dialog"
        aria-modal="true"
        aria-labelledby="remix-lab-overview-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">矩阵账号统一管理</span>
            <h2 id="remix-lab-overview-title">账号总览</h2>
            <p className="batch-studio__subtitle">一篇原文，多账号协作。写手与审稿，由本次任务决定。</p>
          </div>
          <button type="button" className="close" aria-label="关闭账号总览" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>

        <section className="remix-lab-overview__launch" aria-label="批量开跑">
          <div className="batch-studio__source">
          <div className="batch-studio__section-title"><FileText size={18} /><h3>原文素材</h3><span>{source.length.toLocaleString()} 字符</span></div>
          <label>
            同一篇原文，按各账号的提示词分别创作
            <textarea
              aria-label="批量二创原文"
              rows={11}
              disabled={launching}
              value={source}
              onChange={(event) => setSource(event.target.value)}
              placeholder="把对标爆款文案整篇贴进来……"
            />
          </label>
          <div className="batch-studio__route"><span>原文</span><span>写手重构</span><span>{reviewMode === "custom" ? "指定模型审稿" : reviewMode === "off" ? "直接定稿" : "按账号流程定稿"}</span></div>
          </div>
          <fieldset className="batch-studio__settings" disabled={launching}>
          <legend className="sr-only">本次模型与执行设置</legend>
          <div className="remix-lab-overview__models">
            <div className="batch-studio__section-title"><PenLine size={18} /><h3>写手模型</h3><span>可多选对比</span></div>
            <ModelMultiSelect
              value={models}
              onChange={(next) => {
                setModels(next);
                if (next.length > 1) setAuto(false);
              }}
              inheritedLabel="各账号写手节点配置"
            />
          </div>
          <div className="batch-studio__review">
            <div className="batch-studio__section-title"><ShieldCheck size={18} /><h3>审稿模型</h3><span>仅本次生效</span></div>
            <label>审稿方式<select aria-label="审稿方式" value={reviewMode} onChange={(event) => setReviewMode(event.target.value)}>
              <option value="inherit">沿用各账号配置</option><option value="off">不审稿，写手直接定稿</option><option value="custom">指定本次审稿模型</option>
            </select></label>
            {reviewMode === "custom" ? <div className="batch-studio__review-fields">
              <label>模型<ModelCombo id="batch-review-model" aria-label="批量审稿模型" value={reviewModel} onChange={setReviewModel} placeholder="选择或输入模型名称" /></label>
              <label>思考强度<select aria-label="审稿思考强度" value={reviewEffort} onChange={(event) => setReviewEffort(event.target.value)}>
                {["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"].map((effort) => <option key={effort} value={effort}>{effort}</option>)}
              </select></label>
              <label className="batch-studio__inline"><input type="checkbox" checked={reviewFast} onChange={(event) => setReviewFast(event.target.checked)} />Fast 加速</label>
            </div> : null}
            <p className="model-multi-hint">{reviewMode === "inherit" ? "账号没有审稿节点时，写手直接定稿。" : reviewMode === "off" ? "本次跳过审稿，保留写手自查。" : "所有选中账号使用此审稿模型，不修改账号默认流程。"}</p>
          </div>
          </fieldset>
          <div className="remix-lab-overview__launch-row">
            <div className="batch-studio__estimate"><strong>{selectedCount * runCount * Math.max(models.length, 1)}</strong><span>份待生成稿件<br />{selectedCount} 个账号 · {Math.max(models.length, 1)} 路写手</span></div>
            <label className="remix-lab-overview__count">
              每号跑
              <input
                type="number"
                min={1}
                max={3}
                value={runCount}
                onChange={(event) => {
                  const value = Math.min(3, Math.max(1, Math.trunc(Number(event.target.value) || 1)));
                  setRunCount(value);
                }}
              />
              次
            </label>
            <label className="remix-lab-overview__auto">
              <input
                type="checkbox"
                checked={auto}
                disabled={models.length > 1}
                onChange={(event) => {
                  setAuto(event.target.checked);
                  if (event.target.checked) setRunCount(1);
                }}
              />
              全自动混剪（跳过确认闸门，直接出剪映草稿）
            </label>
            <button
              type="button"
              className="remix-lab-start"
              disabled={launching || selectedCount === 0 || !source.trim() || (reviewMode === "custom" && !reviewModel.trim())}
              onClick={() => void launch()}
            >
              <Play size={14} strokeWidth={2} aria-hidden="true" />
              {launching ? "逐号提交中…" : `开跑二创（已选 ${selectedCount} 个账号）`}
            </button>
          </div>
        </section>

        <section className="remix-lab-overview__table" aria-label="账号进度">
          <div className="remix-lab-overview__table-head">
            <h3>投放账号 <small>已选 {selectedCount} / {visibleAccounts.length}</small></h3>
            <div className="remix-lab-overview__table-actions">
            <button type="button" className="header-button remix-lab-icon-btn"
              aria-expanded={hiddenManagerOpen} aria-controls="overview-hidden-accounts"
              onClick={() => setHiddenManagerOpen((open) => !open)}>
              <EyeOff size={13} aria-hidden="true" />
              管理隐藏账号（{hiddenAccounts.length}）
            </button>
            <button
              type="button"
              className="header-button remix-lab-icon-btn"
              onClick={() => void loadProgress()}
            >
              <RefreshCw size={13} strokeWidth={2} />
              刷新
            </button>
            </div>
          </div>
          {hiddenManagerOpen ? (
            <div id="overview-hidden-accounts" className="remix-lab-overview__hidden" role="region" aria-label="隐藏账号管理">
              <p className="remix-lab-muted">隐藏仅影响本浏览器的账号总览，不会停用账号或中断已有任务。恢复显示后可重新勾选开跑。</p>
              {hiddenAccounts.length ? <ul>{hiddenAccounts.map((account) => (
                <li key={account.id}>
                  <span>{account.name}</span>
                  <button type="button" className="header-button remix-lab-icon-btn" aria-label={`显示账号 ${account.name}`}
                    disabled={launching} onClick={() => showAccount(account.id)}>
                    <Eye size={13} aria-hidden="true" />恢复显示
                  </button>
                </li>
              ))}</ul> : <span className="remix-lab-muted">没有隐藏的账号。</span>}
            </div>
          ) : null}
          <table>
            <thead>
              <tr>
                <th className="remix-lab-overview__check">
                  <input
                    type="checkbox"
                    aria-label="全选账号"
                    checked={allSelected}
                    disabled={launching || visibleAccounts.length === 0}
                    onChange={toggleAll}
                  />
                </th>
                <th>账号</th>
                <th>最新实验</th>
                <th>二创进度</th>
                <th>混剪进度</th>
                <th>更新时间</th>
                <th>账号管理</th>
              </tr>
            </thead>
            <tbody>
              {visibleAccounts.length === 0 ? (
                <tr>
                  <td colSpan={7} className="remix-lab-overview__empty">
                    {accounts.length === 0 ? "还没有账号。回创作台右上角「新增账号」先建一个。" : "账号已全部隐藏，点击「管理隐藏账号」恢复显示。"}
                  </td>
                </tr>
              ) : (
                visibleAccounts.map((account) => {
                  const summary = latestByAccount.get(account.id);
                  const detail = summary ? details[summary.id] : undefined;
                  const launchResult = launchResults[account.id];
                  const statusLabel = summary
                    ? (EXPERIMENT_STATUS[summary.status] ?? summary.status)
                    : "还没跑过";
                  const produce = produceLabel(detail);
                  const runs = runsLabel(detail);
                  const history = allByAccount.get(account.id) ?? [];
                  const open = expandedAccount === account.id;
                  return (
                    <Fragment key={account.id}>
                    <tr className={selected.has(account.id) ? "is-selected" : undefined}>
                      <td className="remix-lab-overview__check">
                        <input
                          type="checkbox"
                          aria-label={`选择账号 ${account.name}`}
                          checked={selected.has(account.id)}
                          disabled={launching}
                          onChange={() => toggleAccount(account.id)}
                        />
                      </td>
                      <td>
                        {editingID === account.id ? (
                          <form onSubmit={(event) => { event.preventDefault(); void rename(account); }}>
                            <input aria-label="新账号名称" value={newName} autoFocus disabled={renaming}
                              onChange={(event) => setNewName(event.target.value)} />
                            <button type="submit" className="header-button" disabled={renaming || !newName.trim()}>{renaming ? "保存中…" : "保存名称"}</button>
                            <button type="button" className="header-button" disabled={renaming} onClick={() => setEditingID("")}>取消改名</button>
                            {renameError ? <p role="alert" className="remix-lab-overview__error">{renameError}</p> : null}
                          </form>
                        ) : null}
                        <button
                          type="button"
                          className="remix-lab-overview__account"
                          title="打开这个账号的最新实验"
                          onClick={() => onOpenAccount(account.id, summary?.id)}
                        >
                          {account.name}
                        </button>
                        {launchResult && !launchResult.ok ? (
                          <p className="remix-lab-overview__error">{launchResult.detail}</p>
                        ) : null}
                        {history.length > 1 ? (
                          <button
                            type="button"
                            className="remix-lab-overview__history-toggle"
                            onClick={() => setExpandedAccount(open ? "" : account.id)}
                          >
                            {open ? "收起" : `共 ${history.length} 次实验`}
                          </button>
                        ) : null}
                      </td>
                      <td className="remix-lab-overview__title">{summary?.title || "—"}</td>
                      <td>
                        <span
                          className={`remix-lab-chip remix-lab-overview__chip--${summary?.status ?? "none"}`}
                        >
                          {statusLabel}
                        </span>
                        {runs ? <small>{runs}</small> : null}
                      </td>
                      <td>
                        {produce ? <span className="remix-lab-chip">{produce}</span> : <span>—</span>}
                      </td>
                      <td>
                        <small>{summary?.updated_at ? formatTime(summary.updated_at) : ""}</small>
                      </td>
                      <td>
                        <div className="remix-lab-overview__account-actions">
                        <button type="button" className="header-button" aria-label={`改名账号 ${account.name}`}
                          disabled={launching || renaming || Boolean(deactivating)}
                          onClick={() => { setEditingID(account.id); setNewName(account.name); setRenameError(""); }}>改名</button>
                        <button type="button" className="header-button remix-lab-icon-btn"
                          aria-label={`隐藏账号 ${account.name}`} title="在账号总览中隐藏，可随时恢复显示"
                          disabled={launching || deactivating === account.id} onClick={() => hideAccount(account.id)}>
                          <EyeOff size={13} aria-hidden="true" />隐藏
                        </button>
                        <button
                          type="button"
                          className="header-button remix-lab-icon-btn remix-lab-overview__deactivate"
                          aria-label={`删除账号 ${account.name}`}
                          title="从可选账号中移除，历史项目和文案保留"
                          disabled={launching || renaming || Boolean(deactivating)}
                          onClick={() => void deactivate(account)}
                        >
                          <Trash2 size={13} strokeWidth={2} />
                          {deactivating === account.id ? "删除中…" : "删除账号"}
                        </button>
                        </div>
                      </td>
                    </tr>
                    {open ? (
                      <tr className="remix-lab-overview__history">
                        <td></td>
                        <td colSpan={5}>
                          <ul>
                            {history.map((item) => (
                              <li key={item.id}>
                                <button type="button" className="remix-lab-overview__account" onClick={() => onOpenAccount(account.id, item.id)}>
                                  {item.title || item.id}
                                </button>
                                <span className={`remix-lab-chip remix-lab-overview__chip--${item.status}`}>
                                  {EXPERIMENT_STATUS[item.status] ?? item.status}
                                </span>
                                <small>{item.updated_at ? formatTime(item.updated_at) : ""}</small>
                              </li>
                            ))}
                          </ul>
                        </td>
                      </tr>
                    ) : null}
                    </Fragment>
                  );
                })
              )}
            </tbody>
          </table>
          <p className="remix-lab-muted">
            表格每 4 秒自动刷新。点账号名查看流程步骤、修改定稿或确认混剪；隐藏设置仅在本浏览器保存。
          </p>
        </section>
      </div>
    </div>
  );
}
