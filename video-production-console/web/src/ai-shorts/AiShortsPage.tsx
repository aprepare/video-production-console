import { useEffect, useMemo, useRef, useState } from "react";
import { Clapperboard, Film, LogOut, Plus, RefreshCw, Settings, Trash2, Wand2 } from "lucide-react";
import type { Account, Theme } from "../types";
import {
  assembleShort,
  assetURL,
  createShort,
  deleteShort,
  generateShort,
  fetchMeta,
  getShort,
  listShorts,
  regenerateCharacter,
  regenerateShot,
  shotReady,
  storyboardShort,
  updateShort,
  updateShot,
  NARRATOR,
  type AiShort,
  type AiShortMode,
  type AiShot,
  type AiStylePreset,
  type Api,
} from "./api";
import { ModelCombo } from "../ModelSelect";
import "./ai-shorts.css";

// AI 短片分镜板：左栏故事与角色，中栏分镜卡片，右栏成片。
// 页面只做展示和触发，长任务在后端跑，这里每 3 秒轮询一次进度。

type Props = {
  api: Api;
  shortID?: string;
  onNavigate: (href: string) => void;
  theme?: Theme;
  onThemeChange?: (theme: Theme) => void;
  onOpenSettings?: () => void;
  onLogout?: () => void;
};

const STATUS_LABEL: Record<string, string> = {
  draft: "待拆分镜",
  storyboard: "分镜就位",
  generating: "生成中",
  ready: "素材齐了",
  assembling: "组装中",
  assembled: "已进剪映",
  failed: "失败",
};

const SHOT_STATUS: Record<string, string> = {
  pending: "待生成",
  running: "生成中…",
  done: "完成",
  failed: "失败",
};

type ShotDraft = { scene: string; motion: string; narration: string; speaker: string; style_key: string; subject: string; hero: boolean };

const MODE_LABEL: Record<AiShortMode, string> = { fable: "寓言动画", explainer: "财经解说" };

const DEFAULT_STORY = "族谱上写着，我们祖上是凤凰，是被遗忘的飞禽。若不敢跳，便永世为鸡。自愿入局怨不得别人。我专程前来求取熬汤之法。只要他们笃信崖上标语，我们便有吃不完的肉，喝不尽的汤，哈哈哈。前辈全从这里跃下，化作凤凰乘风飞走。我们纵身一跃，定然也会成功！多好的食材啊，还自带奋斗的佐料。他们以为自己在飞跃，其实是在排队入局。";

const emptyShotDraft = (): ShotDraft => ({ scene: "", motion: "", narration: "", speaker: NARRATOR, style_key: "", subject: "", hero: false });
const draftOf = (shot: AiShot): ShotDraft => ({
  scene: shot.scene, motion: shot.motion, narration: shot.narration, speaker: shot.speaker || NARRATOR,
  style_key: shot.style_key || "", subject: shot.subject || "", hero: !!shot.hero,
});

export function AiShortsPage({ api, shortID, onNavigate, theme, onThemeChange, onOpenSettings, onLogout }: Props) {
  const [shorts, setShorts] = useState<AiShort[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [current, setCurrent] = useState<AiShort | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");
  // 新建表单
  const [newMode, setNewMode] = useState<AiShortMode>("explainer");
  const [newStory, setNewStory] = useState("");
  const [newHeadline, setNewHeadline] = useState("");
  const [newAccount, setNewAccount] = useState("");
  const [newTextModel, setNewTextModel] = useState("");
  const [newSegmentModel, setNewSegmentModel] = useState("");
  const [newStyle, setNewStyle] = useState("documentary");
  const [styles, setStyles] = useState<AiStylePreset[]>([]);
  const [defaultTextModel, setDefaultTextModel] = useState("");
  // 编辑中的文案（当前短片）
  const [storyDraft, setStoryDraft] = useState("");
  const [headlineDraft, setHeadlineDraft] = useState("");
  const [styleDraft, setStyleDraft] = useState("");
  const [textModelDraft, setTextModelDraft] = useState("");
  const [segmentModelDraft, setSegmentModelDraft] = useState("");
  const [editingShot, setEditingShot] = useState<number | null>(null);
  const [shotDraft, setShotDraft] = useState<ShotDraft>(emptyShotDraft);
  const pollRef = useRef<number | undefined>(undefined);

  const reloadList = async () => {
    try {
      setShorts(await listShorts(api));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "列表读取失败。");
    }
  };

  useEffect(() => {
    void reloadList();
    void api("/api/accounts").then(async (res) => {
      if (res.ok) setAccounts(((await res.json()) as Account[]).filter((a) => a.status !== "inactive"));
    }).catch(() => undefined);
    void fetchMeta(api).then((meta) => { setStyles(meta.styles); setDefaultTextModel(meta.defaultTextModel); }).catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  const load = async (id: string) => {
    try {
      const short = await getShort(api, id);
      setCurrent(short);
      setStoryDraft(short.story);
      setHeadlineDraft(short.headline);
      setStyleDraft(short.style);
      setTextModelDraft(short.text_model ?? "");
      setSegmentModelDraft(short.segment_model ?? "");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "短片读取失败。");
    }
  };

  useEffect(() => {
    if (!shortID) {
      setCurrent(null);
      return;
    }
    void load(shortID);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shortID]);

  // 生成中 / 组装中持续轮询，其余状态停。
  const active = current?.status === "generating" || current?.status === "assembling";
  useEffect(() => {
    if (!current || !active) return;
    pollRef.current = window.setInterval(async () => {
      try {
        const fresh = await getShort(api, current.id);
        setCurrent(fresh);
        if (fresh.status !== "generating" && fresh.status !== "assembling") void reloadList();
      } catch {
        // 下一轮再试
      }
    }, 3000);
    return () => window.clearInterval(pollRef.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.id, active]);

  useEffect(() => {
    if (!message) return;
    const timer = window.setTimeout(() => setMessage(""), 5000);
    return () => window.clearTimeout(timer);
  }, [message]);

  const run = async (label: string, action: () => Promise<void>) => {
    setBusy(label);
    try {
      await action();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : `${label}失败。`);
    } finally {
      setBusy("");
    }
  };

  const create = () => run("创建", async () => {
    const short = await createShort(api, {
      mode: newMode, story: newStory, headline: newHeadline, account_id: newAccount,
      text_model: newTextModel, segment_model: newMode === "explainer" ? newSegmentModel : undefined,
      style: newMode === "explainer" ? newStyle : undefined,
    });
    await reloadList();
    onNavigate(`/ai-shorts/${short.id}`);
  });

  const textInput = () => ({ story: storyDraft, headline: headlineDraft, style: styleDraft, text_model: textModelDraft, segment_model: segmentModelDraft });

  const saveText = () => current && run("保存", async () => {
    setCurrent(await updateShort(api, current.id, textInput()));
    setMessage("已保存。改了旁白记得重新拆分镜。");
  });

  const storyboard = () => current && run("拆分镜", async () => {
    await updateShort(api, current.id, textInput());
    setCurrent(await storyboardShort(api, current.id));
    setMessage("分镜拆好了，看一眼画面描述，没问题就点「生成全部」。");
  });

  const generate = () => current && run("生成", async () => {
    await generateShort(api, current.id);
    setCurrent({ ...current, status: "generating" });
  });

  const assemble = () => current && run("组装", async () => {
    await assembleShort(api, current.id);
    setCurrent({ ...current, status: "assembling" });
  });

  const regen = (index: number, stage: "image" | "video") => current && run("重生", async () => {
    await regenerateShot(api, current.id, index, stage);
    // 后端已经按「分镜」独立加锁；这里也只把当前卡标成运行中，
    // 其他卡继续可点，用户可以连续提交多镜（全局生图并发上限 20）。
    setCurrent({
      ...current,
      status: "generating",
      shots: current.shots.map((shot) => shot.index === index
        ? {
            ...shot,
            image_status: stage === "image" ? "running" : shot.image_status,
            video_status: stage === "video" ? "running" : "pending",
            error: undefined,
          }
        : shot),
    });
  });

  const regenCharacter = (index: number) => current && run("重画", async () => {
    await regenerateCharacter(api, current.id, index);
    setCurrent({ ...current, status: "generating" });
  });

  const saveShot = (index: number) => current && run("保存分镜", async () => {
    const { scene, motion, narration, speaker, subject } = shotDraft;
    const input = isExplainer
      ? { scene, narration, subject }
      : { scene, motion, narration, speaker };
    setCurrent(await updateShot(api, current.id, index, input));
    setEditingShot(null);
  });

  const remove = (id: string) => run("删除", async () => {
    await deleteShort(api, id);
    await reloadList();
    if (current?.id === id) onNavigate("/ai-shorts");
  });

  const accountName = (id?: string) => accounts.find((a) => a.id === id)?.name ?? "";
  const isExplainer = current?.mode === "explainer";
  const doneShots = useMemo(() => (current ? current.shots.filter((s) => shotReady(current, s)).length : 0), [current]);

  return (
    <div className="ai-shorts">
      {message ? <div className="ai-shorts__toast" role="status">{message}</div> : null}
      <header className="ai-shorts__header">
        <div className="ai-shorts__brand">
          <span className="ai-shorts__mark"><Clapperboard size={18} strokeWidth={2} /></span>
          <div>
            <span className="eyebrow">视频生产控制台</span>
            <h1>AI 短片</h1>
            <p>文案 → 细分镜 → 每镜一张统一风格图片 → 整篇配音 → 逐字对齐 → 9:16 竖版内嵌 16:9 画面 + 账号背景 + BGM/音效 → 剪映草稿</p>
          </div>
        </div>
        <div className="ai-shorts__actions">
          {theme && onThemeChange ? (
            <label className="theme-control">
              主题
              <select aria-label="选择界面主题" value={theme} onChange={(e) => onThemeChange(e.target.value as Theme)}>
                <option value="light">日间</option>
                <option value="dark">夜间</option>
              </select>
            </label>
          ) : null}
          <button type="button" className="header-button" onClick={() => onNavigate("/")}>文案创作台</button>
          {onOpenSettings ? <button type="button" className="header-button" onClick={() => void onOpenSettings()}><Settings size={15} /> 设置</button> : null}
          {onLogout ? <button type="button" className="header-button" onClick={() => void onLogout()}><LogOut size={15} /> 退出</button> : null}
        </div>
      </header>

      <div className="ai-shorts__body">
        {/* 左栏：列表 + 新建 / 当前短片的文案与角色 */}
        <aside className="ai-shorts__side">
          {!current ? (
            <section className="ai-shorts__card">
              <h2>新建短片</h2>
              <div className="ai-shorts__modes" role="radiogroup" aria-label="短片类型">
                {(Object.keys(MODE_LABEL) as AiShortMode[]).map((mode) => (
                  <button
                    key={mode}
                    type="button"
                    role="radio"
                    aria-checked={newMode === mode}
                    className={`ai-shorts__mode${newMode === mode ? " is-active" : ""}`}
                    onClick={() => {
                      setNewMode(mode);
                      if (mode === "fable" && !newStory) setNewStory(DEFAULT_STORY);
                    }}
                  >
                    <strong>{MODE_LABEL[mode]}</strong>
                    <small>{mode === "explainer" ? "全片选一套统一画风，每 2～5 秒换一张图，推拉平移；全程旁白" : "动物角色 + 每镜图生视频，角色在视频里开口说台词"}</small>
                  </button>
                ))}
              </div>
              <label>
                账号（决定配音音色和草稿命名）
                <select value={newAccount} onChange={(e) => setNewAccount(e.target.value)}>
                  <option value="">不指定</option>
                  {accounts.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
                </select>
              </label>
              <label>
                拆分镜模型（可直接输入任意模型名，留空用默认）
                <ModelCombo id="ai-shorts-new-model" aria-label="拆分镜模型" value={newTextModel} onChange={setNewTextModel} placeholder={`默认：${defaultTextModel || "设置里的 Grok 模型"}`} />
              </label>
              {newMode === "explainer" ? (
                <label>
                  分大段模型（先按话题把整篇切成几段，再并行拆镜；留空按段落机械切）
                  <ModelCombo id="ai-shorts-new-segment-model" aria-label="分大段模型" value={newSegmentModel} onChange={setNewSegmentModel} placeholder="如 gemini-3.8-flash-high；留空不用模型" />
                </label>
              ) : null}
              {newMode === "explainer" ? (
                <label>
                  全片统一画风
                  <select value={newStyle} onChange={(e) => setNewStyle(e.target.value)}>
                    {styles.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
                  </select>
                </label>
              ) : null}
              {newMode === "fable" ? (
                <label>
                  顶部金句（可空，拆分镜时会自动补）
                  <input value={newHeadline} onChange={(e) => setNewHeadline(e.target.value)} />
                </label>
              ) : null}
              <label>
                {newMode === "explainer" ? "口播文案（贴整篇，最多 6000 字）" : "旁白文案（100～300 字）"}
                <textarea rows={newMode === "explainer" ? 14 : 9} value={newStory} onChange={(e) => setNewStory(e.target.value)} placeholder={newMode === "explainer" ? "把二创定稿或口播稿整篇贴进来" : ""} />
              </label>
              <button type="button" className="remix-lab-start" disabled={!!busy || newStory.trim().length < 20} onClick={create}>
                <Plus size={14} /> 建短片
              </button>
            </section>
          ) : (
            <>
              <section className="ai-shorts__card">
                <div className="ai-shorts__card-head">
                  <h2>{isExplainer ? "文案" : "故事"}</h2>
                  <span className={`ai-shorts__status ai-shorts__status--${current.status}`}>{STATUS_LABEL[current.status] ?? current.status}</span>
                </div>
                <p className="ai-shorts__muted">
                  {MODE_LABEL[current.mode ?? "fable"]}
                  {current.account_id ? ` · 账号：${accountName(current.account_id) || current.account_id}` : ""}
                </p>
                {!isExplainer ? (
                  <label>
                    顶部金句
                    <input value={headlineDraft} onChange={(e) => setHeadlineDraft(e.target.value)} />
                  </label>
                ) : null}
                <label>
                  {isExplainer ? "口播文案" : "旁白文案"}
                  <textarea rows={isExplainer ? 14 : 8} value={storyDraft} onChange={(e) => setStoryDraft(e.target.value)} />
                </label>
                <label>
                  拆分镜模型（可直接输入任意模型名；改了要重新拆分镜）
                  <ModelCombo id="ai-shorts-edit-model" aria-label="拆分镜模型" value={textModelDraft} onChange={setTextModelDraft} placeholder={`默认：${defaultTextModel || "设置里的 Grok 模型"}`} />
                </label>
                {isExplainer ? (
                  <label>
                    分大段模型（先按话题切大段再并行拆镜；留空按段落机械切）
                    <ModelCombo id="ai-shorts-edit-segment-model" aria-label="分大段模型" value={segmentModelDraft} onChange={setSegmentModelDraft} placeholder="如 gemini-3.8-flash-high" />
                  </label>
                ) : null}
                {isExplainer ? (
                  <label>
                    全片统一画风（更改后旧图会清空，需重新生成）
                    <select value={styleDraft || "documentary"} onChange={(e) => setStyleDraft(e.target.value)}>
                      {styles.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
                    </select>
                  </label>
                ) : (
                  <details>
                    <summary>画风前缀（全片统一）</summary>
                    <textarea rows={3} value={styleDraft} onChange={(e) => setStyleDraft(e.target.value)} />
                  </details>
                )}
                <div className="ai-shorts__row">
                  <button type="button" className="header-button" disabled={!!busy || active} onClick={saveText}>保存文案</button>
                  <button type="button" className="remix-lab-start" disabled={!!busy || active} onClick={storyboard}>
                    <Wand2 size={14} /> {busy === "拆分镜" ? "拆分镜中…" : current.shots.length ? "重新拆分镜" : "拆分镜"}
                  </button>
                </div>
                {current.error ? <p className={current.status === "failed" ? "ai-shorts__error" : "ai-shorts__muted"}>{current.error}</p> : null}
              </section>

              {current.characters.length ? (
                <section className="ai-shorts__card">
                  <h2>角色设定（全片共用外形，标签是开口时的嗓音）</h2>
                  <div className="ai-shorts__characters">
                    {current.characters.map((c, i) => (
                      <div key={c.name} className="ai-shorts__character">
                        {c.image_path ? (
                          <img src={assetURL(current.id, c.image_path)} alt={c.name} />
                        ) : (
                          <div className="ai-shorts__placeholder">{SHOT_STATUS[c.status] ?? c.status}</div>
                        )}
                        <strong>{c.name}{c.voice ? <em className="ai-shorts__voice-tag">{c.voice}</em> : null}</strong>
                        <small>{c.description}</small>
                        {c.error ? <small className="ai-shorts__error">{c.error}</small> : null}
                        <button type="button" className="header-button" disabled={!!busy || active} onClick={() => regenCharacter(i)}>
                          <RefreshCw size={12} /> 重画
                        </button>
                      </div>
                    ))}
                  </div>
                </section>
              ) : null}

              <button type="button" className="header-button ai-shorts__back" onClick={() => onNavigate("/ai-shorts")}>← 返回列表</button>
            </>
          )}

          <section className="ai-shorts__card">
            <h2>短片列表</h2>
            {shorts.length === 0 ? <p className="ai-shorts__muted">还没有短片。</p> : null}
            <ul className="ai-shorts__list">
              {shorts.map((item) => (
                <li key={item.id} className={item.id === current?.id ? "is-selected" : undefined}>
                  <button type="button" onClick={() => onNavigate(`/ai-shorts/${item.id}`)}>
                    <strong>{item.headline || item.title}</strong>
                    <span>
                      {accountName(item.account_id) ? <em>{accountName(item.account_id)}</em> : null}
                      <em>{STATUS_LABEL[item.status] ?? item.status}</em>
                      <em>{item.shots.length} 镜</em>
                    </span>
                  </button>
                  <button type="button" className="ai-shorts__delete" aria-label="删除短片" onClick={() => remove(item.id)}>
                    <Trash2 size={13} />
                  </button>
                </li>
              ))}
            </ul>
          </section>
        </aside>

        {/* 中栏：分镜卡片 */}
        <main className="ai-shorts__main">
          {!current ? (
            <div className="ai-shorts__empty">
              <Film size={40} strokeWidth={1.5} />
              <p>左边贴文案建短片，或从列表打开一条。</p>
            </div>
          ) : current.shots.length === 0 ? (
            <div className="ai-shorts__empty">
              <Wand2 size={40} strokeWidth={1.5} />
              <p>{isExplainer ? "点左边「拆分镜」，AI 会按 8～22 字细切，每镜给出一个直接对应文案的画面主体和画面描述。" : "点左边「拆分镜」，AI 会把旁白切成 6～12 镜并给出角色表。"}</p>
            </div>
          ) : (
            <>
              <div className="ai-shorts__toolbar">
                <span>
                  {current.shots.length} 镜 · 已出画面 {doneShots}/{current.shots.length}
                </span>
                <div className="ai-shorts__row">
                  <button type="button" className="remix-lab-start" disabled={!!busy || active} onClick={generate}>
                    {active && current.status === "generating" ? "生成中…" : doneShots === 0 ? (isExplainer ? "生成全部图片" : "生成全部") : "续跑未完成的"}
                  </button>
                  <button
                    type="button"
                    className="remix-lab-start"
                    disabled={!!busy || active || doneShots !== current.shots.length}
                    onClick={assemble}
                    title={doneShots !== current.shots.length ? "所有镜头出完才能组装" : "配音 + 剪映草稿"}
                  >
                    {current.status === "assembling" ? "组装中…" : "配音并进剪映"}
                  </button>
                </div>
              </div>
              <ol className="ai-shorts__shots">
                {current.shots.map((shot) => (
                  <ShotCard
                    key={shot.index}
                    shortID={current.id}
                    shot={shot}
                    explainer={isExplainer}
                    ready={shotReady(current, shot)}
                    styles={styles}
                    speakers={[NARRATOR, ...current.characters.map((c) => c.name)]}
                    disabled={
                      !!busy ||
                      current.status === "assembling" ||
                      shot.image_status === "running" ||
                      shot.video_status === "running"
                    }
                    editing={editingShot === shot.index}
                    draft={shotDraft}
                    onDraft={setShotDraft}
                    onEdit={() => { setEditingShot(shot.index); setShotDraft(draftOf(shot)); }}
                    onCancel={() => setEditingShot(null)}
                    onSave={() => saveShot(shot.index)}
                    onRegen={(stage) => regen(shot.index, stage)}
                  />
                ))}
              </ol>
            </>
          )}
        </main>

        {/* 右栏：成片 */}
        <aside className="ai-shorts__result">
          <section className="ai-shorts__card">
            <h2>成片</h2>
            {!current ? <p className="ai-shorts__muted">—</p> : current.status === "assembled" ? (
              <>
                <p><strong>{current.draft_name}</strong></p>
                <p className="ai-shorts__muted">时长 {current.duration_s?.toFixed(1)} 秒 · 草稿已进剪映，打开剪映即可看到。</p>
                {current.narration_path ? (
                  <audio controls src={assetURL(current.id, current.narration_path)} className="ai-shorts__audio" />
                ) : null}
                <p className="ai-shorts__muted">路径：{current.draft_path}</p>
                <p className="ai-shorts__muted">简介里记得带课程引流；这条视频的目的是拉权重和涨粉。</p>
              </>
            ) : current.status === "assembling" ? (
              <p className="ai-shorts__muted">{current.error || "配音 → 对齐分镜 → 生成草稿…"}</p>
            ) : (
              <p className="ai-shorts__muted">
                {isExplainer
                  ? "所有图片出完后点「配音并进剪映」。整篇文案一次配音（生图时已在后台预跑），逐字对齐后每张图落在自己那句话上；9:16 画布、账号背景框、BGM 与音效沿用混剪那套；字幕 ≤10 字压在画面底边。"
                  : "所有镜头出完后点「配音并进剪映」。角色台词是视频模型生成时让角色自己说的，直接用视频声轨；旁白句用账号音色配音，画面裁到这句话的长度；顶部大字用金句。"}
              </p>
            )}
          </section>
        </aside>
      </div>
    </div>
  );
}

function ShotCard({
  shortID, shot, explainer, ready, styles, speakers, disabled, editing, draft, onDraft, onEdit, onCancel, onSave, onRegen,
}: {
  shortID: string;
  shot: AiShot;
  explainer: boolean;
  ready: boolean;
  styles: AiStylePreset[];
  speakers: string[];
  disabled: boolean;
  editing: boolean;
  draft: ShotDraft;
  onDraft: (d: ShotDraft) => void;
  onEdit: () => void;
  onCancel: () => void;
  onSave: () => void;
  onRegen: (stage: "image" | "video") => void;
}) {
  const imgURL = assetURL(shortID, shot.image_path);
  const vidURL = assetURL(shortID, shot.video_path);
  const speaker = shot.speaker || NARRATOR;
  const spoken = !explainer && speaker !== NARRATOR;
  const needsVideo = !explainer;
  const styleName = styles.find((s) => s.key === shot.style_key)?.name ?? shot.style_key ?? "";
  // 图是按旧提示词出的：描述/规则改过之后没重生。
  const stale = !!shot.image_path && shot.image_status === "done" && !!shot.image_prompt && shot.image_prompt_used !== undefined && shot.image_prompt_used !== shot.image_prompt;
  const stateClass = ready ? "done" : needsVideo ? shot.video_status : shot.image_status;
  return (
    <li className={`ai-shorts__shot ai-shorts__shot--${stateClass}`}>
      <div className="ai-shorts__shot-media">
        {!explainer && shot.video_status === "done" && vidURL ? (
          <video src={vidURL} controls muted={!spoken} loop playsInline poster={imgURL || undefined} />
        ) : imgURL ? (
          <img src={imgURL} alt={`第 ${shot.index + 1} 镜画面`} />
        ) : (
          <div className="ai-shorts__placeholder">
            {shot.image_status === "running" ? "生图中…" : shot.video_status === "running" ? "生视频中…" : "待生成"}
          </div>
        )}
        <span className="ai-shorts__shot-no">{shot.index + 1}</span>
        {stale ? <span className="ai-shorts__shot-stale" title="描述或规则改过，这张图还是按旧提示词生成的">图是旧的，点重生图</span> : null}
        <span className="ai-shorts__shot-state">
          {explainer ? (
            <>
              {styleName ? `${styleName} · ` : ""}
              图片 {SHOT_STATUS[shot.image_status] ?? shot.image_status} · 推拉
            </>
          ) : (
            <>
              图 {SHOT_STATUS[shot.image_status] ?? shot.image_status} · 视频 {SHOT_STATUS[shot.video_status] ?? shot.video_status} · {shot.seconds}s
              {" · "}{spoken ? "角色开口说" : "旁白配音"}
            </>
          )}
          {shot.end_s ? ` · ${(shot.start_s ?? 0).toFixed(1)}–${shot.end_s.toFixed(1)}s` : ""}
        </span>
      </div>
      <div className="ai-shorts__shot-body">
        {editing ? (
          <>
            {explainer ? (
              <div>
                <label>
                  画面主体
                  <input value={draft.subject} placeholder="一眼能对上这句话的东西" onChange={(e) => onDraft({ ...draft, subject: e.target.value })} />
                </label>
              </div>
            ) : (
              <label>
                谁说
                <select value={draft.speaker} onChange={(e) => onDraft({ ...draft, speaker: e.target.value })}>
                  {speakers.map((name) => <option key={name} value={name}>{name}</option>)}
                </select>
              </label>
            )}
            <label>{explainer ? "旁白" : "台词"}<textarea rows={2} value={draft.narration} onChange={(e) => onDraft({ ...draft, narration: e.target.value })} /></label>
            <label>画面<textarea rows={3} value={draft.scene} onChange={(e) => onDraft({ ...draft, scene: e.target.value })} /></label>
            {!explainer ? <label>镜头/动作<textarea rows={2} value={draft.motion} onChange={(e) => onDraft({ ...draft, motion: e.target.value })} /></label> : null}
            <p className="ai-shorts__muted">
              {explainer ? "改了主体或画面后点「重生图」。全片画风在左侧文案卡统一选择。" : "选角色名 = 这句由角色在视频里开口说（重生视频才生效）；选「旁白」= 用账号音色配音。"}
            </p>
            <div className="ai-shorts__row">
              <button type="button" className="header-button" onClick={onCancel}>取消</button>
              <button type="button" className="remix-lab-start" onClick={onSave}>保存</button>
            </div>
          </>
        ) : (
          <>
            <p className="ai-shorts__narration">
              {explainer ? null : <span className={`ai-shorts__speaker${spoken ? "" : " is-narrator"}`}>{speaker}</span>}
              「{shot.narration}」
            </p>
            {explainer && shot.subject ? <p className="ai-shorts__subject">主体：{shot.subject}</p> : null}
            <p className="ai-shorts__scene">{shot.scene}</p>
            {!explainer ? (
              <p className="ai-shorts__motion">镜头：{shot.motion}{shot.characters?.length ? ` · 出场：${shot.characters.join("、")}` : ""}</p>
            ) : shot.motion ? (
              <p className="ai-shorts__motion">动作：{shot.motion}</p>
            ) : null}
            <div className="ai-shorts__prompts">
              {shot.image_prompt ? (
                <details className="ai-shorts__prompt">
                  <summary>生图提示词</summary>
                  <p>{shot.image_prompt}</p>
                </details>
              ) : null}
              {needsVideo && shot.video_prompt ? (
                <details className="ai-shorts__prompt">
                  <summary>生视频提示词</summary>
                  <p>{shot.video_prompt}</p>
                </details>
              ) : null}
            </div>
            {shot.error ? <p className="ai-shorts__error">{shot.error}</p> : null}
            <div className="ai-shorts__row">
              <button type="button" className="header-button" disabled={disabled} onClick={onEdit}>改描述</button>
              {explainer ? (
                <button type="button" className="header-button" disabled={disabled} onClick={() => onRegen("image")}>重生图</button>
              ) : (
                <>
                  <button type="button" className="header-button" disabled={disabled} onClick={() => onRegen("image")}>重生图+视频</button>
                  <button type="button" className="header-button" disabled={disabled || !shot.image_path} onClick={() => onRegen("video")}>只重生视频</button>
                </>
              )}
            </div>
          </>
        )}
      </div>
    </li>
  );
}
