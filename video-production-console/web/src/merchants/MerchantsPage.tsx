import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { ArrowLeft, FilePlus2, FolderOpen, Plus, RefreshCw, Search, Store, Trash2, Upload, X } from "lucide-react";
import "../shell/studio-page.css";
import "./merchants.css";
import { AigcStudio } from "./AigcStudio";

export type BusinessApi = (path: string, init?: RequestInit) => Promise<Response>;

export type MerchantStatus = "prospect" | "trial" | "active" | "paused" | "churned";
export type Merchant = {
  id: string;
  name: string;
  category: string;
  contact: string;
  phone: string;
  address: string;
  selling_points: string;
  notes: string;
  account_id: string;
  status: MerchantStatus;
  created_at: string;
  updated_at: string;
};

export type MaterialKind = "video" | "image" | "audio" | "document" | "other";
export type LicenseSource = "merchant_shot" | "team_shot" | "licensed_stock" | "ai_generated" | "unknown";
export type Material = {
  id: string;
  merchant_id: string;
  kind: MaterialKind;
  filename: string;
  size: number;
  description: string;
  license_source: LicenseSource;
  license_scope: string;
  license_expires_at: string;
  parent_material_id: string;
  created_at: string;
};

export type OrderPackage = "trial" | "monthly" | "premium" | "custom";
export type OrderStatus = "draft" | "signed" | "producing" | "delivered" | "closed" | "cancelled";
export type Order = {
  id: string;
  merchant_id: string;
  title: string;
  package: OrderPackage;
  price_cents: number;
  paid_cents: number;
  video_quota: number;
  max_duration_s: number;
  revision_rounds: number;
  due_date: string;
  scope_notes: string;
  status: OrderStatus;
  renewal_of: string;
  created_at: string;
};

export type CostKind = "labor" | "api" | "material" | "revision" | "other";
export type OrderCost = {
  id: string;
  order_id: string;
  project_id: string;
  kind: CostKind;
  minutes: number;
  amount_cents: number;
  note: string;
  occurred_at: string;
};
export type OrderSummary = {
  price_cents: number;
  paid_cents: number;
  cost_cents: number;
  labor_minutes: number;
  revision_count: number;
  project_count: number;
  delivered_project_count: number;
  contribution_cents: number;
};
export type OrderProject = {
  id: string;
  title: string;
  stage: string;
  account_id: string;
  updated_at: string;
  delivered_at?: string | null;
};
export type OrderDetail = { order: Order; summary: OrderSummary; costs: OrderCost[]; projects: OrderProject[] };
export type BusinessOverview = {
  merchants_by_status: Partial<Record<MerchantStatus, number>>;
  orders_by_status: Partial<Record<OrderStatus, number>>;
  price_cents: number;
  paid_cents: number;
  cost_cents: number;
  contribution_cents: number;
  renewal_count: number;
};

export const merchantStatusLabels: Record<MerchantStatus, string> = { prospect: "意向", trial: "试单", active: "合作中", paused: "暂停", churned: "流失" };
const merchantStatusTone: Record<MerchantStatus, string> = { prospect: "draft", trial: "working", active: "ready", paused: "draft", churned: "attention" };
export const orderStatusLabels: Record<OrderStatus, string> = { draft: "草拟", signed: "已签约", producing: "制作中", delivered: "已交付", closed: "已结单", cancelled: "已取消" };
const orderStatusTone: Record<OrderStatus, string> = { draft: "draft", signed: "working", producing: "working", delivered: "ready", closed: "ready", cancelled: "attention" };
export const packageLabels: Record<OrderPackage, string> = { trial: "体验包", monthly: "月费包", premium: "精品定制", custom: "自定义" };
const packagePresets: Record<OrderPackage, Partial<Order>> = {
  trial: { title: "体验包 · 10 条", price_cents: 9900, video_quota: 10, max_duration_s: 60, revision_rounds: 1 },
  monthly: { title: "月费内容包", price_cents: 80000, video_quota: 20, max_duration_s: 60, revision_rounds: 2 },
  premium: { title: "精品定制", price_cents: 12000, video_quota: 1, max_duration_s: 90, revision_rounds: 2 },
  custom: { title: "", price_cents: 0, video_quota: 0, max_duration_s: 0, revision_rounds: 0 },
};
const licenseLabels: Record<LicenseSource, string> = { merchant_shot: "商户实拍", team_shot: "团队实拍", licensed_stock: "授权图库", ai_generated: "AI 生成", unknown: "来源不明" };
const kindLabels: Record<MaterialKind, string> = { video: "视频", image: "图片", audio: "音频", document: "文档", other: "其他" };
const costLabels: Record<CostKind, string> = { labor: "人工", api: "接口费用", material: "素材", revision: "返工", other: "其他" };

export function yuan(cents: number) {
  const value = (cents || 0) / 100;
  return `¥${value.toLocaleString("zh-CN", { minimumFractionDigits: value % 1 ? 2 : 0, maximumFractionDigits: 2 })}`;
}
export function toCents(text: string) {
  const value = Number.parseFloat(text.replace(/[,，¥\s]/g, ""));
  return Number.isFinite(value) ? Math.round(value * 100) : 0;
}
function fileSize(bytes: number) {
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} GB`;
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`;
  if (bytes >= 1 << 10) return `${Math.round(bytes / (1 << 10))} KB`;
  return `${bytes} B`;
}
function shortDate(value?: string | null) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString("zh-CN", { month: "numeric", day: "numeric" });
}
async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}
async function json<T>(api: BusinessApi, path: string, init: RequestInit | undefined, fallback: string): Promise<T> {
  const response = await api(path, init);
  if (!response.ok) throw new Error(await readError(response, fallback));
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
const jsonBody = (method: string, body: unknown): RequestInit => ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

type Accounts = Array<{ id: string; name: string }>;
type Projects = Array<{ id: string; title: string; account_id: string; stage: string }>;

type Props = {
  api: BusinessApi;
  accounts: Accounts;
  projects: Projects;
  initialMerchantID?: string;
  onNavigate: (href: string) => void;
};

export function MerchantsPage({ api, accounts, projects, initialMerchantID, onNavigate }: Props) {
  const [merchants, setMerchants] = useState<Merchant[]>([]);
  const [overview, setOverview] = useState<BusinessOverview | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error" | "unavailable">("loading");
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<MerchantStatus | "all">("all");
  const [creating, setCreating] = useState(false);
  const [notice, setNotice] = useState<{ tone: "ok" | "error"; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [list, summary] = await Promise.all([
        json<Merchant[]>(api, "/api/merchants", undefined, "商户列表读取失败。"),
        json<BusinessOverview>(api, "/api/business/overview", undefined, "经营数据读取失败。").catch(() => null),
      ]);
      setMerchants(list);
      setOverview(summary);
      setState("ready");
    } catch (error) {
      setState(error instanceof Error && /404|not found/i.test(error.message) ? "unavailable" : "error");
    }
  }, [api]);
  useEffect(() => { void load(); }, [load]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return merchants.filter((merchant) => (statusFilter === "all" || merchant.status === statusFilter)
      && (!needle || [merchant.name, merchant.category, merchant.contact].some((value) => value.toLocaleLowerCase().includes(needle))));
  }, [merchants, query, statusFilter]);

  if (initialMerchantID) {
    return <MerchantDetail key={initialMerchantID} api={api} merchantID={initialMerchantID} accounts={accounts} projects={projects}
      onBack={() => { onNavigate("/merchants"); void load(); }} />;
  }

  return <main className="studio-page merchants-page" id="studio-content" tabIndex={-1}>
    <header className="studio-page__head">
      <div>
        <p className="studio-kicker">创作工作室 / 商户与订单</p>
        <h1>商户与订单</h1>
        <p>记录商户卖点和授权素材，按订单约定交付范围，逐单记下工时、接口费用和返工，算清每单贡献。</p>
      </div>
      <div className="studio-row">
        <button type="button" className="studio-button studio-button--quiet" aria-label="刷新" onClick={() => void load()}><RefreshCw size={15} aria-hidden="true" /></button>
        <button type="button" className="studio-button studio-button--primary" onClick={() => setCreating(true)}><Plus size={15} aria-hidden="true" />新增商户</button>
      </div>
    </header>

    {notice ? <div className="studio-notice" data-tone={notice.tone} role={notice.tone === "error" ? "alert" : "status"}>{notice.text}</div> : null}
    {state === "unavailable" ? <div className="studio-notice" data-tone="error" role="alert">当前控制台版本还没有商户接口，请更新控制台后再试。</div> : null}

    {overview ? <section className="studio-stat-grid merchants-overview" aria-label="经营总览">
      <div className="studio-stat"><span>合作中商户</span><strong>{overview.merchants_by_status.active ?? 0}</strong><small>试单 {overview.merchants_by_status.trial ?? 0} · 意向 {overview.merchants_by_status.prospect ?? 0}</small></div>
      <div className="studio-stat"><span>合同额</span><strong>{yuan(overview.price_cents)}</strong><small>已收 {yuan(overview.paid_cents)}</small></div>
      <div className="studio-stat"><span>已记成本</span><strong>{yuan(overview.cost_cents)}</strong><small>人工、接口、素材、返工</small></div>
      <div className="studio-stat" data-tone={overview.contribution_cents < 0 ? "negative" : undefined}><span>贡献（已收 − 成本）</span><strong>{yuan(overview.contribution_cents)}</strong><small>未扣获客、固定支出与税费</small></div>
      <div className="studio-stat"><span>续费订单</span><strong>{overview.renewal_count}</strong><small>制作中 {(overview.orders_by_status.producing ?? 0) + (overview.orders_by_status.signed ?? 0)} · 已交付 {overview.orders_by_status.delivered ?? 0}</small></div>
    </section> : null}

    <section className="studio-panel" aria-labelledby="merchant-list-title">
      <header>
        <div><h2 id="merchant-list-title">商户 <span className="merchants-count">{merchants.length}</span></h2></div>
        <div className="studio-row merchants-filters">
          <label className="studio-search"><Search size={16} aria-hidden="true" /><input type="search" aria-label="搜索商户" placeholder="名称、品类、联系人" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
          <select aria-label="按状态筛选商户" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as MerchantStatus | "all")}>
            <option value="all">全部状态</option>
            {(Object.keys(merchantStatusLabels) as MerchantStatus[]).map((status) => <option key={status} value={status}>{merchantStatusLabels[status]}</option>)}
          </select>
        </div>
      </header>
      {state === "loading" ? <p className="studio-empty" role="status">正在读取…</p> : null}
      {state === "error" ? <div className="studio-notice" data-tone="error" role="alert">商户列表读取失败。<button type="button" className="studio-button studio-button--quiet" onClick={() => void load()}>重试</button></div> : null}
      {state === "ready" && filtered.length === 0 ? <div className="studio-empty"><Store size={28} strokeWidth={1.5} aria-hidden="true" /><strong>{merchants.length ? "没有匹配的商户" : "还没有商户"}</strong><span>{merchants.length ? "调整搜索或状态筛选。" : "先登记一家试点商户：名称、品类和他家真实的卖点。"}</span></div> : null}
      <div className="merchant-grid">
        {filtered.map((merchant) => <button type="button" key={merchant.id} className="merchant-card" onClick={() => onNavigate(`/merchants/${merchant.id}`)}>
          <span className="merchant-card__top"><strong>{merchant.name}</strong><span className="studio-status" data-status={merchantStatusTone[merchant.status]}>{merchantStatusLabels[merchant.status]}</span></span>
          <span className="merchant-card__meta">{[merchant.category, merchant.contact].filter(Boolean).join(" · ") || "未填品类和联系人"}</span>
          {merchant.selling_points ? <span className="merchant-card__points">{merchant.selling_points}</span> : null}
        </button>)}
      </div>
    </section>

    {creating ? <MerchantDialog title="新增商户" accounts={accounts} onClose={() => setCreating(false)} onSubmit={async (draft) => {
      const created = await json<Merchant>(api, "/api/merchants", jsonBody("POST", draft), "商户创建失败。");
      setCreating(false);
      setNotice({ tone: "ok", text: `已新增商户「${created.name}」。` });
      onNavigate(`/merchants/${created.id}`);
    }} /> : null}
  </main>;
}

type MerchantDraft = Pick<Merchant, "name" | "category" | "contact" | "phone" | "address" | "selling_points" | "notes" | "account_id" | "status">;
const emptyMerchant: MerchantDraft = { name: "", category: "", contact: "", phone: "", address: "", selling_points: "", notes: "", account_id: "", status: "prospect" };

function Dialog({ title, onClose, children, footer }: { title: string; onClose: () => void; children: ReactNode; footer: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("input, textarea, select")?.focus();
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); previous?.focus?.(); };
  }, [onClose]);
  return <div className="studio-dialog-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="studio-dialog" role="dialog" aria-modal="true" aria-label={title} ref={ref}>
      <header><h2>{title}</h2><button type="button" className="studio-button studio-button--quiet" aria-label="关闭" onClick={onClose}><X size={16} aria-hidden="true" /></button></header>
      {children}
      <footer>{footer}</footer>
    </div>
  </div>;
}

function MerchantDialog({ title, initial = emptyMerchant, accounts, onClose, onSubmit }: { title: string; initial?: MerchantDraft; accounts: Accounts; onClose: () => void; onSubmit: (draft: MerchantDraft) => Promise<void> }) {
  const [draft, setDraft] = useState<MerchantDraft>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const set = (key: keyof MerchantDraft) => (event: { target: { value: string } }) => setDraft((current) => ({ ...current, [key]: event.target.value }));
  const submit = async () => {
    if (!draft.name.trim()) { setError("请填写商户名称。"); return; }
    setBusy(true); setError("");
    try { await onSubmit({ ...draft, name: draft.name.trim() }); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "保存失败。"); setBusy(false); }
  };
  return <Dialog title={title} onClose={onClose} footer={<>
    <button type="button" className="studio-button" onClick={onClose}>取消</button>
    <button type="button" className="studio-button studio-button--primary" disabled={busy} onClick={() => void submit()}>{busy ? "正在保存…" : "保存"}</button>
  </>}>
    {error ? <p className="studio-notice" data-tone="error" role="alert">{error}</p> : null}
    <div className="studio-fields">
      <label className="studio-field">商户名称<input value={draft.name} onChange={set("name")} placeholder="例：岚山老王海鲜档口" /></label>
      <label className="studio-field">品类<input value={draft.category} onChange={set("category")} placeholder="茶叶 / 海鲜 / 餐饮" /></label>
      <label className="studio-field">状态<select value={draft.status} onChange={set("status")}>{(Object.keys(merchantStatusLabels) as MerchantStatus[]).map((status) => <option key={status} value={status}>{merchantStatusLabels[status]}</option>)}</select></label>
      <label className="studio-field">联系人<input value={draft.contact} onChange={set("contact")} /></label>
      <label className="studio-field">电话<input value={draft.phone} onChange={set("phone")} inputMode="tel" /></label>
      <label className="studio-field">发布账号<select value={draft.account_id} onChange={set("account_id")}><option value="">不绑定</option>{accounts.map((account) => <option key={account.id} value={account.id}>{account.name}</option>)}</select></label>
      <label className="studio-field wide">地址<input value={draft.address} onChange={set("address")} /></label>
      <label className="studio-field wide">卖点（真实、可核验）<small>写文案时会用到：产地、做法、价格带、季节、和别家的不同。</small><textarea rows={4} value={draft.selling_points} onChange={set("selling_points")} placeholder="例：每天凌晨四点码头直采，现场称重不缺斤两；咕嘟蟹子 38 元一斤…" /></label>
      <label className="studio-field wide">备注<textarea rows={2} value={draft.notes} onChange={set("notes")} /></label>
    </div>
  </Dialog>;
}

function MerchantDetail({ api, merchantID, accounts, projects, onBack }: { api: BusinessApi; merchantID: string; accounts: Accounts; projects: Projects; onBack: () => void }) {
  const [merchant, setMerchant] = useState<Merchant | null>(null);
  const [materials, setMaterials] = useState<Material[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [tab, setTab] = useState<"orders" | "materials" | "aigc">("orders");
  const [editing, setEditing] = useState(false);
  const [orderDialog, setOrderDialog] = useState(false);
  const [openOrderID, setOpenOrderID] = useState("");
  const [notice, setNotice] = useState<{ tone: "ok" | "error"; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [detail, mats, ords] = await Promise.all([
        json<Merchant>(api, `/api/merchants/${merchantID}`, undefined, "商户读取失败。"),
        json<Material[]>(api, `/api/merchants/${merchantID}/materials`, undefined, "素材读取失败。"),
        json<Order[]>(api, `/api/orders?merchant_id=${merchantID}`, undefined, "订单读取失败。"),
      ]);
      setMerchant(detail); setMaterials(mats); setOrders(ords); setState("ready");
    } catch {
      setState("error");
    }
  }, [api, merchantID]);
  useEffect(() => { void load(); }, [load]);
  const reloadMaterials = useCallback(async () => {
    try { setMaterials(await json<Material[]>(api, `/api/merchants/${merchantID}/materials`, undefined, "素材读取失败。")); } catch { /* keep the current list */ }
  }, [api, merchantID]);

  if (state === "loading") return <main className="studio-page" id="studio-content" tabIndex={-1}><p className="studio-empty" role="status">正在读取商户…</p></main>;
  if (state === "error" || !merchant) return <main className="studio-page" id="studio-content" tabIndex={-1}>
    <div className="studio-notice" data-tone="error" role="alert">商户读取失败或已被删除。<button type="button" className="studio-button studio-button--quiet" onClick={onBack}>返回商户列表</button></div>
  </main>;

  const accountName = accounts.find((account) => account.id === merchant.account_id)?.name;
  const remove = async () => {
    if (!window.confirm(`删除商户「${merchant.name}」？它的订单、成本记录和授权素材文件会一起删除。`)) return;
    try { await json(api, `/api/merchants/${merchant.id}`, { method: "DELETE" }, "删除失败。"); onBack(); }
    catch (error) { setNotice({ tone: "error", text: error instanceof Error ? error.message : "删除失败。" }); }
  };

  return <main className="studio-page merchant-detail" id="studio-content" tabIndex={-1}>
    <header className="studio-page__head">
      <div className="merchant-detail__title">
        <button type="button" className="studio-button studio-button--quiet" aria-label="返回商户列表" onClick={onBack}><ArrowLeft size={18} aria-hidden="true" /></button>
        <div>
          <p className="studio-kicker">商户 · {merchant.category || "未填品类"}{accountName ? ` · 发布到 ${accountName}` : ""}</p>
          <h1>{merchant.name} <span className="studio-status" data-status={merchantStatusTone[merchant.status]}>{merchantStatusLabels[merchant.status]}</span></h1>
          <p>{[merchant.contact, merchant.phone, merchant.address].filter(Boolean).join(" · ") || "未填联系方式"}</p>
        </div>
      </div>
      <div className="studio-row">
        <button type="button" className="studio-button" onClick={() => setEditing(true)}>编辑资料</button>
        <button type="button" className="studio-button studio-button--quiet studio-button--danger" aria-label="删除商户" onClick={() => void remove()}><Trash2 size={15} aria-hidden="true" /></button>
      </div>
    </header>
    {notice ? <div className="studio-notice" data-tone={notice.tone} role={notice.tone === "error" ? "alert" : "status"}>{notice.text}</div> : null}

    {merchant.selling_points ? <section className="studio-panel merchant-points"><header><h2>卖点</h2></header><p>{merchant.selling_points}</p></section> : null}

    <section className="studio-panel">
      <header>
        <div className="studio-tabs" role="tablist" aria-label="商户内容">
          <button type="button" role="tab" aria-selected={tab === "orders"} onClick={() => setTab("orders")}>订单 {orders.length}</button>
          <button type="button" role="tab" aria-selected={tab === "materials"} onClick={() => setTab("materials")}>授权素材 {materials.length}</button>
          <button type="button" role="tab" aria-selected={tab === "aigc"} onClick={() => setTab("aigc")}>AIGC 重塑</button>
        </div>
        {tab === "orders" ? <button type="button" className="studio-button studio-button--primary" onClick={() => setOrderDialog(true)}><FilePlus2 size={15} aria-hidden="true" />新建订单</button> : null}
      </header>
      {tab === "orders" ? <OrderList orders={orders} onOpen={setOpenOrderID} /> : null}
      {tab === "materials" ? <MaterialLedger api={api} merchantID={merchant.id} materials={materials} onChange={setMaterials} onNotice={setNotice} /> : null}
      {tab === "aigc" ? <AigcStudio api={api} merchantID={merchant.id} materials={materials} orders={orders} onMaterialsChanged={reloadMaterials} onNotice={setNotice} /> : null}
    </section>

    {editing ? <MerchantDialog title="编辑商户" initial={merchant} accounts={accounts} onClose={() => setEditing(false)} onSubmit={async (draft) => {
      const updated = await json<Merchant>(api, `/api/merchants/${merchant.id}`, jsonBody("PATCH", draft), "保存失败。");
      setMerchant(updated); setEditing(false); setNotice({ tone: "ok", text: "商户资料已保存。" });
    }} /> : null}
    {orderDialog ? <OrderDialog merchantID={merchant.id} orders={orders} onClose={() => setOrderDialog(false)} onSubmit={async (draft) => {
      const created = await json<Order>(api, "/api/orders", jsonBody("POST", draft), "订单创建失败。");
      setOrders((current) => [created, ...current]); setOrderDialog(false); setOpenOrderID(created.id);
    }} /> : null}
    {openOrderID ? <OrderDrawer api={api} orderID={openOrderID} projects={projects} accounts={accounts} onClose={() => { setOpenOrderID(""); void load(); }} /> : null}
  </main>;
}

function OrderList({ orders, onOpen }: { orders: Order[]; onOpen: (id: string) => void }) {
  if (!orders.length) return <div className="studio-empty"><FolderOpen size={28} strokeWidth={1.5} aria-hidden="true" /><strong>还没有订单</strong><span>先按约定建一单：条数、片长、修改轮次和交付日期写清楚。</span></div>;
  return <table className="studio-table">
    <thead><tr><th>订单</th><th>套餐</th><th>状态</th><th className="num">条数</th><th className="num">合同额</th><th className="num">已收</th><th>交付日</th></tr></thead>
    <tbody>{orders.map((order) => <tr key={order.id}>
      <td><button type="button" className="merchant-link" onClick={() => onOpen(order.id)}>{order.title}</button>{order.renewal_of ? <span className="studio-status" data-status="ready">续费</span> : null}</td>
      <td>{packageLabels[order.package]}</td>
      <td><span className="studio-status" data-status={orderStatusTone[order.status]}>{orderStatusLabels[order.status]}</span></td>
      <td className="num">{order.video_quota || "—"}</td>
      <td className="num">{yuan(order.price_cents)}</td>
      <td className="num">{yuan(order.paid_cents)}</td>
      <td>{order.due_date || "—"}</td>
    </tr>)}</tbody>
  </table>;
}

type OrderDraft = Omit<Order, "id" | "created_at">;
function OrderDialog({ merchantID, orders, initial, onClose, onSubmit }: { merchantID: string; orders: Order[]; initial?: Order; onClose: () => void; onSubmit: (draft: OrderDraft) => Promise<void> }) {
  const [draft, setDraft] = useState<OrderDraft>(() => initial ? { ...initial } : {
    merchant_id: merchantID, status: "draft", renewal_of: "", due_date: "", scope_notes: "", paid_cents: 0,
    package: "trial", title: packagePresets.trial.title || "", price_cents: packagePresets.trial.price_cents || 0,
    video_quota: packagePresets.trial.video_quota || 0, max_duration_s: packagePresets.trial.max_duration_s || 0, revision_rounds: packagePresets.trial.revision_rounds || 0,
  });
  const [price, setPrice] = useState(String((draft.price_cents || 0) / 100));
  const [paid, setPaid] = useState(String((draft.paid_cents || 0) / 100));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pickPackage = (pkg: OrderPackage) => {
    const preset = packagePresets[pkg];
    setDraft((current) => ({ ...current, package: pkg, ...(initial ? {} : preset) }));
    if (!initial && preset.price_cents !== undefined) setPrice(String(preset.price_cents / 100));
  };
  const setNum = (key: "video_quota" | "max_duration_s" | "revision_rounds") => (event: { target: { value: string } }) =>
    setDraft((current) => ({ ...current, [key]: Math.max(0, Number.parseInt(event.target.value, 10) || 0) }));
  const submit = async () => {
    if (!draft.title.trim()) { setError("请填写订单名称。"); return; }
    setBusy(true); setError("");
    try { await onSubmit({ ...draft, title: draft.title.trim(), price_cents: toCents(price), paid_cents: toCents(paid) }); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "保存失败。"); setBusy(false); }
  };
  return <Dialog title={initial ? "编辑订单" : "新建订单"} onClose={onClose} footer={<>
    <button type="button" className="studio-button" onClick={onClose}>取消</button>
    <button type="button" className="studio-button studio-button--primary" disabled={busy} onClick={() => void submit()}>{busy ? "正在保存…" : "保存订单"}</button>
  </>}>
    {error ? <p className="studio-notice" data-tone="error" role="alert">{error}</p> : null}
    <div className="studio-fields">
      <label className="studio-field">套餐<select value={draft.package} onChange={(event) => pickPackage(event.target.value as OrderPackage)}>{(Object.keys(packageLabels) as OrderPackage[]).map((pkg) => <option key={pkg} value={pkg}>{packageLabels[pkg]}</option>)}</select></label>
      <label className="studio-field">订单名称<input value={draft.title} onChange={(event) => setDraft((current) => ({ ...current, title: event.target.value }))} /></label>
      <label className="studio-field">状态<select value={draft.status} onChange={(event) => setDraft((current) => ({ ...current, status: event.target.value as OrderStatus }))}>{(Object.keys(orderStatusLabels) as OrderStatus[]).map((status) => <option key={status} value={status}>{orderStatusLabels[status]}</option>)}</select></label>
      <label className="studio-field">合同额（元）<input inputMode="decimal" value={price} onChange={(event) => setPrice(event.target.value)} /></label>
      <label className="studio-field">已收（元）<input inputMode="decimal" value={paid} onChange={(event) => setPaid(event.target.value)} /></label>
      <label className="studio-field">交付日<input type="date" value={draft.due_date} onChange={(event) => setDraft((current) => ({ ...current, due_date: event.target.value }))} /></label>
      <label className="studio-field">约定条数<input type="number" min={0} value={draft.video_quota} onChange={setNum("video_quota")} /></label>
      <label className="studio-field">单条最长（秒）<input type="number" min={0} value={draft.max_duration_s} onChange={setNum("max_duration_s")} /></label>
      <label className="studio-field">修改轮次<input type="number" min={0} value={draft.revision_rounds} onChange={setNum("revision_rounds")} /></label>
      <label className="studio-field">续费自<select value={draft.renewal_of} onChange={(event) => setDraft((current) => ({ ...current, renewal_of: event.target.value }))}><option value="">不是续费</option>{orders.filter((order) => order.id !== initial?.id).map((order) => <option key={order.id} value={order.id}>{order.title}</option>)}</select></label>
      <label className="studio-field wide">服务范围<small>素材来源、是否上门拍摄、AI 镜头是否另计、发布由谁负责。</small><textarea rows={3} value={draft.scope_notes} onChange={(event) => setDraft((current) => ({ ...current, scope_notes: event.target.value }))} /></label>
    </div>
  </Dialog>;
}

function OrderDrawer({ api, orderID, projects, accounts, onClose }: { api: BusinessApi; orderID: string; projects: Projects; accounts: Accounts; onClose: () => void }) {
  const [detail, setDetail] = useState<OrderDetail | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [cost, setCost] = useState<{ kind: CostKind; minutes: string; amount: string; note: string; project_id: string }>({ kind: "labor", minutes: "", amount: "", note: "", project_id: "" });
  const [assign, setAssign] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try { setDetail(await json<OrderDetail>(api, `/api/orders/${orderID}`, undefined, "订单读取失败。")); setError(""); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "订单读取失败。"); }
  }, [api, orderID]);
  useEffect(() => { void load(); }, [load]);

  const run = async (action: () => Promise<unknown>) => {
    setBusy(true);
    try { await action(); await load(); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "操作失败。"); }
    finally { setBusy(false); }
  };
  const addCost = () => run(async () => {
    const minutes = Math.max(0, Number.parseInt(cost.minutes, 10) || 0);
    const amount_cents = toCents(cost.amount);
    if (!minutes && !amount_cents && cost.kind !== "revision") throw new Error("填写工时或金额。");
    await json(api, `/api/orders/${orderID}/costs`, jsonBody("POST", { kind: cost.kind, minutes, amount_cents, note: cost.note.trim(), project_id: cost.project_id }), "成本记录失败。");
    setCost((current) => ({ ...current, minutes: "", amount: "", note: "" }));
  });

  const assigned = new Set(detail?.projects.map((project) => project.id) || []);
  const assignable = projects.filter((project) => !assigned.has(project.id));
  const accountName = (id: string) => accounts.find((account) => account.id === id)?.name || "";

  return <Dialog title={detail ? detail.order.title : "订单"} onClose={onClose} footer={<button type="button" className="studio-button" onClick={onClose}>关闭</button>}>
    {error ? <p className="studio-notice" data-tone="error" role="alert">{error}</p> : null}
    {!detail ? <p className="studio-empty" role="status">正在读取订单…</p> : <div className="order-drawer">
      <div className="studio-row">
        <span className="studio-status" data-status={orderStatusTone[detail.order.status]}>{orderStatusLabels[detail.order.status]}</span>
        <span className="studio-metrics"><span>{packageLabels[detail.order.package]}</span><span>{detail.order.video_quota} 条</span>{detail.order.max_duration_s ? <span>≤{detail.order.max_duration_s}s</span> : null}<span>改 {detail.order.revision_rounds} 轮</span><span>交付 {detail.order.due_date || "未定"}</span></span>
        <span className="grow" />
        <button type="button" className="studio-button studio-button--quiet" onClick={() => setEditing(true)}>编辑</button>
      </div>
      {detail.order.scope_notes ? <p className="order-scope">{detail.order.scope_notes}</p> : null}

      <div className="studio-stat-grid">
        <div className="studio-stat"><span>交付进度</span><strong>{detail.summary.delivered_project_count}/{detail.order.video_quota || detail.summary.project_count}</strong><small>已归单项目 {detail.summary.project_count}</small></div>
        <div className="studio-stat"><span>已收</span><strong>{yuan(detail.summary.paid_cents)}</strong><small>合同 {yuan(detail.order.price_cents)}</small></div>
        <div className="studio-stat"><span>成本</span><strong>{yuan(detail.summary.cost_cents)}</strong><small>人工 {Math.round(detail.summary.labor_minutes / 6) / 10} 小时 · 返工 {detail.summary.revision_count} 次</small></div>
        <div className="studio-stat" data-tone={detail.summary.contribution_cents < 0 ? "negative" : undefined}><span>贡献</span><strong>{yuan(detail.summary.contribution_cents)}</strong><small>已收 − 已记成本</small></div>
      </div>

      <section aria-labelledby="order-projects-title">
        <h3 id="order-projects-title">归单项目</h3>
        {detail.projects.length ? <ul className="studio-list order-projects">{detail.projects.map((project) => <li key={project.id}>
          <a href={`/projects/${project.id}`}>{project.title}</a>
          <span className="studio-metrics"><span>{accountName(project.account_id)}</span><span>{project.stage}</span></span>
          <span className="grow" />
          {project.delivered_at
            ? <span className="studio-status" data-status="ready">已交付 {shortDate(project.delivered_at)}</span>
            : <button type="button" className="studio-button studio-button--quiet" disabled={busy} onClick={() => void run(() => json(api, `/api/orders/${orderID}/projects/${project.id}/deliver`, { method: "POST" }, "标记失败。"))}>标记已交付</button>}
          <button type="button" className="studio-button studio-button--quiet" aria-label={`把 ${project.title} 移出订单`} disabled={busy} onClick={() => void run(() => json(api, `/api/orders/${orderID}/projects/${project.id}`, { method: "DELETE" }, "移出失败。"))}><X size={14} aria-hidden="true" /></button>
        </li>)}</ul> : <p className="studio-metrics">还没有项目归到这单。</p>}
        <div className="studio-row">
          <select className="grow" aria-label="选择要归单的项目" value={assign} onChange={(event) => setAssign(event.target.value)}>
            <option value="">选择一个制作项目归到这单…</option>
            {assignable.map((project) => <option key={project.id} value={project.id}>{project.title}{accountName(project.account_id) ? ` · ${accountName(project.account_id)}` : ""}</option>)}
          </select>
          <button type="button" className="studio-button" disabled={!assign || busy} onClick={() => void run(async () => { await json(api, `/api/orders/${orderID}/projects`, jsonBody("POST", { project_id: assign }), "归单失败。"); setAssign(""); })}>归单</button>
        </div>
      </section>

      <section aria-labelledby="order-costs-title">
        <h3 id="order-costs-title">成本流水</h3>
        <div className="order-cost-form">
          <select aria-label="成本类型" value={cost.kind} onChange={(event) => setCost((current) => ({ ...current, kind: event.target.value as CostKind }))}>{(Object.keys(costLabels) as CostKind[]).map((kind) => <option key={kind} value={kind}>{costLabels[kind]}</option>)}</select>
          <input aria-label="工时（分钟）" inputMode="numeric" placeholder="分钟" value={cost.minutes} onChange={(event) => setCost((current) => ({ ...current, minutes: event.target.value }))} />
          <input aria-label="金额（元）" inputMode="decimal" placeholder="金额 ¥" value={cost.amount} onChange={(event) => setCost((current) => ({ ...current, amount: event.target.value }))} />
          <select aria-label="关联项目" value={cost.project_id} onChange={(event) => setCost((current) => ({ ...current, project_id: event.target.value }))}><option value="">不关联项目</option>{detail.projects.map((project) => <option key={project.id} value={project.id}>{project.title}</option>)}</select>
          <input aria-label="说明" placeholder="说明（例：剪辑质检 / 听悟转写 / 商户要求换开头）" value={cost.note} onChange={(event) => setCost((current) => ({ ...current, note: event.target.value }))} />
          <button type="button" className="studio-button" disabled={busy} onClick={() => void addCost()}><Plus size={14} aria-hidden="true" />记一笔</button>
        </div>
        {detail.costs.length ? <table className="studio-table"><thead><tr><th>日期</th><th>类型</th><th>说明</th><th className="num">工时</th><th className="num">金额</th><th /></tr></thead><tbody>
          {detail.costs.map((item) => <tr key={item.id}>
            <td>{shortDate(item.occurred_at)}</td>
            <td>{costLabels[item.kind]}</td>
            <td>{item.note || "—"}</td>
            <td className="num">{item.minutes ? `${item.minutes} 分` : "—"}</td>
            <td className="num">{item.amount_cents ? yuan(item.amount_cents) : "—"}</td>
            <td><button type="button" className="studio-button studio-button--quiet" aria-label="删除这笔成本" disabled={busy} onClick={() => void run(() => json(api, `/api/order-costs/${item.id}`, { method: "DELETE" }, "删除失败。"))}><Trash2 size={14} aria-hidden="true" /></button></td>
          </tr>)}
        </tbody></table> : <p className="studio-metrics">还没有成本记录。人工工时只记分钟，金额留空也可以。</p>}
      </section>
    </div>}
    {editing && detail ? <OrderDialog merchantID={detail.order.merchant_id} orders={[]} initial={detail.order} onClose={() => setEditing(false)} onSubmit={async (draft) => {
      await json(api, `/api/orders/${orderID}`, jsonBody("PATCH", draft), "保存失败。");
      setEditing(false); await load();
    }} /> : null}
  </Dialog>;
}

function MaterialLedger({ api, merchantID, materials, onChange, onNotice }: { api: BusinessApi; merchantID: string; materials: Material[]; onChange: (materials: Material[]) => void; onNotice: (notice: { tone: "ok" | "error"; text: string }) => void }) {
  const [license, setLicense] = useState<LicenseSource>("merchant_shot");
  const [scope, setScope] = useState("");
  const [expires, setExpires] = useState("");
  const [description, setDescription] = useState("");
  const [uploading, setUploading] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const upload = async (files: FileList | null) => {
    if (!files?.length) return;
    const added: Material[] = [];
    try {
      for (const file of Array.from(files)) {
        setUploading(file.name);
        const form = new FormData();
        form.append("file", file);
        form.append("license_source", license);
        form.append("license_scope", scope.trim());
        form.append("license_expires_at", expires);
        form.append("description", description.trim());
        const response = await api(`/api/merchants/${merchantID}/materials`, { method: "POST", body: form });
        if (!response.ok) throw new Error(await readError(response, `${file.name} 上传失败。`));
        added.push(await response.json());
      }
      onNotice({ tone: "ok", text: `已登记 ${added.length} 个素材。` });
    } catch (error) {
      onNotice({ tone: "error", text: error instanceof Error ? error.message : "上传失败。" });
    } finally {
      if (added.length) onChange([...added, ...materials]);
      setUploading("");
      if (inputRef.current) inputRef.current.value = "";
    }
  };
  const remove = async (material: Material) => {
    if (!window.confirm(`删除素材「${material.filename}」？文件也会删除。`)) return;
    try {
      await json(api, `/api/materials/${material.id}`, { method: "DELETE" }, "删除失败。");
      onChange(materials.filter((item) => item.id !== material.id));
    } catch (error) {
      onNotice({ tone: "error", text: error instanceof Error ? error.message : "删除失败。" });
    }
  };
  const expired = (material: Material) => Boolean(material.license_expires_at) && material.license_expires_at < new Date().toISOString().slice(0, 10);

  return <div className="material-ledger">
    <div className="material-upload">
      <div className="studio-fields">
        <label className="studio-field">授权来源<select value={license} onChange={(event) => setLicense(event.target.value as LicenseSource)}>{(Object.keys(licenseLabels) as LicenseSource[]).map((key) => <option key={key} value={key}>{licenseLabels[key]}</option>)}</select></label>
        <label className="studio-field">授权到期<input type="date" value={expires} onChange={(event) => setExpires(event.target.value)} /></label>
        <label className="studio-field">授权范围<input value={scope} onChange={(event) => setScope(event.target.value)} placeholder="例：仅用于该商户自有账号" /></label>
        <label className="studio-field">素材说明<input value={description} onChange={(event) => setDescription(event.target.value)} placeholder="例：码头直采、现场称重" /></label>
      </div>
      <div className="studio-row">
        <span className="studio-metrics">同一批文件按上面的授权信息登记；之后可以单独修改。</span>
        <span className="grow" />
        <input ref={inputRef} type="file" multiple hidden onChange={(event) => void upload(event.target.files)} />
        <button type="button" className="studio-button studio-button--primary" disabled={Boolean(uploading)} onClick={() => inputRef.current?.click()}><Upload size={15} aria-hidden="true" />{uploading ? `正在上传 ${uploading}…` : "上传授权素材"}</button>
      </div>
    </div>
    {materials.length ? <table className="studio-table">
      <thead><tr><th>文件</th><th>类型</th><th>授权来源</th><th>范围</th><th>到期</th><th className="num">大小</th><th /></tr></thead>
      <tbody>{materials.map((material) => <tr key={material.id}>
        <td><a href={`/api/materials/${material.id}/content`} target="_blank" rel="noreferrer">{material.filename}</a>{material.description ? <div className="studio-metrics">{material.description}</div> : null}</td>
        <td>{kindLabels[material.kind]}</td>
        <td><span className="studio-status" data-status={material.license_source === "unknown" ? "attention" : "ready"}>{licenseLabels[material.license_source]}</span></td>
        <td>{material.license_scope || "—"}</td>
        <td>{material.license_expires_at ? <span className="studio-status" data-status={expired(material) ? "attention" : "draft"}>{material.license_expires_at}{expired(material) ? " 已过期" : ""}</span> : "—"}</td>
        <td className="num">{fileSize(material.size)}</td>
        <td><button type="button" className="studio-button studio-button--quiet" aria-label={`删除 ${material.filename}`} onClick={() => void remove(material)}><Trash2 size={14} aria-hidden="true" /></button></td>
      </tr>)}</tbody>
    </table> : <div className="studio-empty"><strong>还没有授权素材</strong><span>上传商户提供或团队实拍的原片，登记来源和授权范围，交付前可追溯。</span></div>}
  </div>;
}
