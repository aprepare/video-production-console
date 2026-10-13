import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, Pencil, Plus, Sparkles, Trash2, X } from "lucide-react";
import {
  applyRemixLabStyle,
  deleteRemixLabStyle,
  emptyBrief,
  fetchRemixLabStyles,
  fetchRemixLabWorkflowPrompts,
  saveRemixLabStyle,
  type RemixLabApi,
  type RemixLabBrief,
  type RemixLabStyle,
  type RemixLabWorkflowPrompts,
} from "./api";
import "./style-picker.css";

/** 行业预设只填「通常怎么写」，真实卖点、价格、地址必须由用户自己填。 */
export const briefPresets: Array<{ id: string; label: string; style: string; brief: Partial<RemixLabBrief> }> = [
  { id: "seafood", label: "海鲜餐饮", style: "local_store", brief: { industry: "海鲜餐饮 / 渔港小馆", audience: "本地上班族、周末带家人朋友聚餐的人、来旅游想吃本地海鲜的游客", offer: "到店堂食 / 团购套餐", conversion: "点左下角定位或团购链接，到店报暗号", persona: "老板本人，说话实在" } },
  { id: "tea", label: "茶叶", style: "product_seller", brief: { industry: "茶叶（绿茶 / 红茶）", audience: "爱喝茶的中年人、给长辈和客户送礼的人", offer: "茶叶产品 / 礼盒", conversion: "点小黄车或橱窗下单", persona: "茶园主 / 制茶人，懂行但不卖弄", banned: "治病\n降三高\n减肥\n排毒\n养胃\n抗癌" } },
  { id: "snack", label: "小吃早餐", style: "local_store", brief: { industry: "街边小吃 / 早餐", audience: "附近上班族、学生、早起买早饭的居民", offer: "到店购买", conversion: "收藏这条，路过来尝尝", persona: "摊主本人，热情直接" } },
  { id: "store", label: "门店服务", style: "local_store", brief: { industry: "本地门店服务（理发、美甲、洗车、维修等）", audience: "附近三公里的居民", offer: "到店服务 / 预约", conversion: "私信预约或点团购", persona: "店长" } },
  { id: "knowledge", label: "知识分享", style: "knowledge", brief: { industry: "行业知识科普", audience: "想弄懂这件事的普通人", offer: "关注账号，持续分享", conversion: "关注看下一期，评论区提问", persona: "懂行的从业者" } },
];

const platformLabels: Record<RemixLabBrief["platform"], string> = { "": "不限平台", douyin: "抖音", xhs: "小红书", channels: "视频号" };

type Props = {
  api: RemixLabApi;
  accountID?: string;
  ownerLabel: string;
  onClose: () => void;
  onApplied: (result: RemixLabWorkflowPrompts) => void;
  onMessage: (message: string) => void;
};

/** 选风格 + 填简报：风格决定怎么写，简报决定写什么、给谁看、往哪转化。 */
export function StylePicker({ api, accountID, ownerLabel, onClose, onApplied, onMessage }: Props) {
  const [styles, setStyles] = useState<RemixLabStyle[]>([]);
  const [currentStyle, setCurrentStyle] = useState("");
  const [picked, setPicked] = useState("");
  const [brief, setBrief] = useState<RemixLabBrief>(emptyBrief);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<RemixLabStyle | null>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [list, prompts] = await Promise.all([fetchRemixLabStyles(api), fetchRemixLabWorkflowPrompts(api, accountID)]);
        if (cancelled) return;
        setStyles(list);
        const style = prompts.workflow.style || "finance_course";
        setCurrentStyle(style);
        setPicked(style);
        setBrief({ ...emptyBrief, ...(prompts.workflow.brief ?? {}) });
      } catch (loadError) {
        if (!cancelled) setError(loadError instanceof Error ? loadError.message : "风格模板读取失败。");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [api, accountID]);

  const selected = styles.find((style) => style.id === picked);
  const grouped = useMemo(() => {
    const groups = new Map<string, RemixLabStyle[]>();
    for (const style of styles) {
      const key = style.builtin ? style.category || "通用" : "我的风格";
      groups.set(key, [...(groups.get(key) ?? []), style]);
    }
    return [...groups.entries()];
  }, [styles]);

  const patch = (next: Partial<RemixLabBrief>) => setBrief((current) => ({ ...current, ...next }));
  const fillPreset = (presetID: string) => {
    const preset = briefPresets.find((item) => item.id === presetID);
    if (!preset) return;
    // Presets only fill blanks so a half-written brief is never overwritten.
    setBrief((current) => {
      const next = { ...current };
      for (const [key, value] of Object.entries(preset.brief) as Array<[keyof RemixLabBrief, string]>) {
        if (!String(current[key] ?? "").trim()) (next as Record<string, unknown>)[key] = value;
      }
      return next;
    });
    if (styles.some((style) => style.id === preset.style)) setPicked(preset.style);
  };

  const apply = async () => {
    if (!picked || busy) return;
    if (selected?.finance === false && currentStyle === "finance_course" && !window.confirm(`把「${ownerLabel}」从财经卖课切换到「${selected.name}」？写手、策划和审稿提示词会换成新风格，之前改过的提示词不会保留。`)) return;
    setBusy(true);
    setError("");
    try {
      const result = await applyRemixLabStyle(api, picked, brief, accountID);
      onApplied(result);
      onMessage(`已应用「${selected?.name ?? picked}」，下一次二创开始生效。`);
    } catch (applyError) {
      setError(applyError instanceof Error ? applyError.message : "应用风格失败。");
    } finally {
      setBusy(false);
    }
  };

  const removeStyle = async (style: RemixLabStyle) => {
    if (!window.confirm(`删除自定义风格「${style.name}」？`)) return;
    try {
      await deleteRemixLabStyle(api, style.id);
      setStyles((current) => current.filter((item) => item.id !== style.id));
      if (picked === style.id) setPicked(currentStyle);
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "删除失败。");
    }
  };

  const startEdit = (style: RemixLabStyle | null) => {
    setEditing(style
      ? { ...style, id: style.builtin ? "" : style.id, name: style.builtin ? `${style.name}（我的版本）` : style.name, builtin: false }
      : { id: "", name: "", category: "我的风格", description: "", best_for: [], planner: "", writer: "", writer_user: "{{BRIEF}}\n{{NOTES}}\n# 同行原文\n{{SOURCE}}", reviewer: "", rules: "", finance: false, builtin: false });
  };

  return createPortal(
    <div className="modal-backdrop style-picker__backdrop" onClick={busy ? undefined : onClose}>
      <div
        ref={dialogRef}
        className="preview-modal style-picker"
        role="dialog"
        aria-modal="true"
        aria-labelledby="style-picker-title"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => { if (event.key === "Escape" && !busy) onClose(); }}
      >
        <header className="style-picker__head">
          <div>
            <span className="style-picker__eyebrow">{ownerLabel} · 下一次二创生效</span>
            <h2 id="style-picker-title"><Sparkles size={18} aria-hidden="true" /> 二创风格与内容简报</h2>
          </div>
          <button type="button" className="close" aria-label="关闭" disabled={busy} onClick={onClose}><X size={20} aria-hidden="true" /></button>
        </header>

        {loading ? <p className="style-picker__state" role="status">正在读取…</p> : null}
        {error ? <p className="style-picker__error" role="alert">{error}</p> : null}

        {!loading ? <div className="style-picker__body">
          <section className="style-picker__styles" aria-label="写作风格">
            <h3>1. 选写法</h3>
            {grouped.map(([group, items]) => <div key={group} className="style-picker__group">
              <h4>{group}</h4>
              <ul>
                {items.map((style) => <li key={style.id}>
                  <button type="button" className="style-card" aria-pressed={picked === style.id} onClick={() => setPicked(style.id)}>
                    <strong>{style.name}{currentStyle === style.id ? <span className="style-card__current">使用中</span> : null}</strong>
                    <span>{style.description}</span>
                    {style.best_for?.length ? <span className="style-card__tags">{style.best_for.slice(0, 4).map((tag) => <em key={tag}>{tag}</em>)}</span> : null}
                  </button>
                  <span className="style-card__tools">
                    <button type="button" aria-label={`${style.builtin ? "复制" : "编辑"}「${style.name}」`} title={style.builtin ? "复制一份来改" : "编辑"} onClick={() => startEdit(style)}>{style.builtin ? <Copy size={13} aria-hidden="true" /> : <Pencil size={13} aria-hidden="true" />}</button>
                    {!style.builtin ? <button type="button" aria-label={`删除「${style.name}」`} title="删除" onClick={() => void removeStyle(style)}><Trash2 size={13} aria-hidden="true" /></button> : null}
                  </span>
                </li>)}
              </ul>
            </div>)}
            <button type="button" className="header-button style-picker__new" onClick={() => startEdit(null)}><Plus size={14} aria-hidden="true" />自定义风格</button>
          </section>

          <section className="style-picker__brief" aria-label="内容简报">
            <h3>2. 填简报<small>写手只用这里的事实，没填的不会编</small></h3>
            <div className="style-picker__presets" role="group" aria-label="行业预设">
              <span>快速填：</span>
              {briefPresets.map((preset) => <button key={preset.id} type="button" onClick={() => fillPreset(preset.id)}>{preset.label}</button>)}
            </div>
            {selected?.finance ? <p className="style-picker__hint">财经卖课风格自带课程资料和转化话术，简报只作补充。</p> : null}
            <div className="style-picker__fields">
              <label>行业 / 品类<input value={brief.industry} onChange={(event) => patch({ industry: event.target.value })} placeholder="例：海鲜餐饮、日照绿茶、早餐摊" /></label>
              <label>卖什么<input value={brief.offer} onChange={(event) => patch({ offer: event.target.value })} placeholder="例：到店堂食、99 元双人套餐、礼盒装" /></label>
              <label className="wide">给谁看<input value={brief.audience} onChange={(event) => patch({ audience: event.target.value })} placeholder="例：本地上班族、周末带孩子出来吃饭的家长" /></label>
              <label className="wide">真实卖点与事实<textarea rows={5} value={brief.selling_points} onChange={(event) => patch({ selling_points: event.target.value })} placeholder={"一行一条，只写真的：\n每天凌晨码头直采，当天卖完\n招牌：辣炒蛤蜊、清蒸梭子蟹\n人均 60 左右，99 元双人套餐含 4 个菜\n地址：岚山渔港路 12 号，营业 10:00–22:00"} /></label>
              <label>转化动作<input value={brief.conversion} onChange={(event) => patch({ conversion: event.target.value })} placeholder="例：点团购链接 / 点小黄车 / 私信预约" /></label>
              <label>出镜人设<input value={brief.persona} onChange={(event) => patch({ persona: event.target.value })} placeholder="例：老板本人、探店博主、店员" /></label>
              <label>发布平台<select value={brief.platform} onChange={(event) => patch({ platform: event.target.value as RemixLabBrief["platform"] })}>{(Object.keys(platformLabels) as Array<RemixLabBrief["platform"]>).map((key) => <option key={key} value={key}>{platformLabels[key]}</option>)}</select></label>
              <label>目标时长<select value={brief.duration_s} onChange={(event) => patch({ duration_s: Number(event.target.value) })}><option value={0}>跟随原文</option>{[15, 30, 45, 60, 90, 120, 180].map((value) => <option key={value} value={value}>{value} 秒（约 {Math.round(value * 240 / 60)} 字）</option>)}</select></label>
              <label className="wide">禁用词 / 不能说的<textarea rows={2} value={brief.banned} onChange={(event) => patch({ banned: event.target.value })} placeholder="一行一个，例：最便宜、治病、加微信" /></label>
              <label className="wide">其他要求<textarea rows={2} value={brief.notes} onChange={(event) => patch({ notes: event.target.value })} placeholder="例：结尾提一下周末有活动；不要提竞争对手" /></label>
            </div>
          </section>
        </div> : null}

        <footer className="style-picker__foot">
          <span className="style-picker__summary">{selected ? <>写法：<strong>{selected.name}</strong>{brief.industry ? <> · {brief.industry}</> : null}</> : "先选一个写法"}</span>
          <button type="button" className="header-button" disabled={busy} onClick={onClose}>取消</button>
          <button type="button" className="primary-button" disabled={!picked || busy || loading} onClick={() => void apply()}><Check size={14} aria-hidden="true" />{busy ? "正在应用…" : "应用到当前账号"}</button>
        </footer>

        {editing ? <StyleEditor
          style={editing}
          onCancel={() => setEditing(null)}
          onSave={async (draft) => {
            try {
              const saved = await saveRemixLabStyle(api, draft);
              setStyles((current) => [...current.filter((item) => item.id !== saved.id), saved]);
              setPicked(saved.id);
              setEditing(null);
            } catch (saveError) {
              setError(saveError instanceof Error ? saveError.message : "风格模板保存失败。");
            }
          }}
        /> : null}
      </div>
    </div>,
    document.body,
  );
}

function StyleEditor({ style, onCancel, onSave }: { style: RemixLabStyle; onCancel: () => void; onSave: (style: RemixLabStyle) => Promise<void> }) {
  const [draft, setDraft] = useState(style);
  const [saving, setSaving] = useState(false);
  const set = (key: keyof RemixLabStyle) => (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setDraft((current) => ({ ...current, [key]: event.target.value }));
  return <div className="style-editor" role="group" aria-label="编辑风格模板">
    <header>
      <h3>{style.id ? "编辑风格" : "新建风格"}</h3>
      <p>写手提示词决定怎么写；user 模板里 {"{{SOURCE}}"} 是原文、{"{{BRIEF}}"} 是简报、{"{{NOTES}}"} 是修改要求。输出格式由系统自动追加，不用写。</p>
    </header>
    <div className="style-editor__fields">
      <label>名称<input value={draft.name} onChange={set("name")} /></label>
      <label>一句话说明<input value={draft.description} onChange={set("description")} placeholder="适合什么内容" /></label>
      <label className="wide">写手提示词<textarea rows={10} value={draft.writer} onChange={set("writer")} /></label>
      <label className="wide">写手 user 模板<textarea rows={4} value={draft.writer_user} onChange={set("writer_user")} /></label>
      <details className="wide">
        <summary>高级：策划、审稿与公共规则</summary>
        <label>策划提示词（留空=不要策划节点）<textarea rows={6} value={draft.planner} onChange={set("planner")} /></label>
        <label>审稿提示词（留空=保留当前审稿）<textarea rows={6} value={draft.reviewer} onChange={set("reviewer")} /></label>
        <label>公共编审规则（留空=用通用底线）<textarea rows={6} value={draft.rules} onChange={set("rules")} /></label>
      </details>
    </div>
    <footer>
      <button type="button" className="header-button" disabled={saving} onClick={onCancel}>取消</button>
      <button type="button" className="primary-button" disabled={saving || !draft.name.trim() || !draft.writer.trim()} onClick={async () => { setSaving(true); await onSave(draft); setSaving(false); }}>{saving ? "正在保存…" : "保存风格"}</button>
    </footer>
  </div>;
}
