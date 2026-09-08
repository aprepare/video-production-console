import { useState, type CSSProperties, type ReactNode } from "react";
import { videoPlanOf, DEFAULT_CAPTION_COLOR, HEADLINE_FULL_VIDEO, RECOMMENDED_SEGMENT_STYLES, SEGMENT_ROLES, type AiShot, type AiStylePreset, type SegmentStyles, type ShotKeyword, type VideoPlan, type VisualSettings } from "./api";

export const subjectTypes: Record<string, string> = { object: "物件细节", environment: "建筑与环境", person: "人物行为", comparison: "对比关系", metaphor: "意象示意" };
export const cameraMoves: Record<string, string> = { still: "静止", zoom_in: "缓慢推近", zoom_out: "缓慢拉远", pan_left: "向左平移", pan_right: "向右平移" };

// StylePicker：下拉框照旧（可用键盘、测试也靠它），下面铺一排同一测试场景的参考图卡片，点卡片等于选下拉；
// 选中的那套在下面用一行说明"用在什么段落 / 适合谁 / 能不能让人停下"。参考图由 scripts/ai-shorts/render_style_previews.py 生成。
export function StylePicker({ styles, value, onChange, disabled, hint }: { styles: AiStylePreset[]; value: string; onChange: (key: string) => void; disabled?: boolean; hint?: string }) {
  const selected = styles.find((s) => s.key === value);
  return <div className="ai-shorts__style-picker">
    <label>画面风格策略{hint ? `（${hint}）` : ""}
      <select value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
        {styles.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
      </select>
    </label>
    {styles.length ? <div className="ai-shorts__style-grid" role="radiogroup" aria-label="画面风格参考图">
      {styles.map((s) => <button key={s.key} type="button" role="radio" aria-checked={s.key === value} aria-label={s.name} disabled={disabled} title={s.usage} className={`ai-shorts__style-card${s.key === value ? " is-active" : ""}`} onClick={() => onChange(s.key)}>
        {s.preview ? <img src={s.preview} alt="" loading="lazy" onError={(e) => { e.currentTarget.style.visibility = "hidden"; }} /> : <span className="ai-shorts__style-card-blank" />}
        <span>{s.name}</span>
      </button>)}
    </div> : null}
    {selected ? <p className="ai-shorts__muted ai-shorts__style-note"><strong>{selected.name}</strong>：{selected.usage}{selected.note ? <><br />{selected.note}</> : null}</p> : null}
  </div>;
}

// SegmentStyleFields：分段画风。整篇跟项目底色，四个位置各自可换一套：开头几镜 / 讲钱的镜 / 祝福词镜 / 结尾处境镜。
// 角色由后端按旁白打（拆完分镜就能在分镜卡上看到"开头 / 讲钱 / 祝福词 / 结尾处境"标签），这里只决定每个角色用什么画风。
export function SegmentStyleFields({ value, onChange, styles, disabled }: { value: VisualSettings; onChange: (v: VisualSettings) => void; styles: AiStylePreset[]; disabled?: boolean }) {
  const seg = value.segment_styles ?? {};
  const set = (part: Partial<SegmentStyles>) => onChange({ ...value, segment_styles: { ...seg, ...part } });
  const options = styles.filter((s) => s.key !== "finance_editorial");
  const active = SEGMENT_ROLES.some((r) => !!seg[r.key]);
  return <fieldset className="ai-shorts__segment-styles" disabled={disabled}>
    <legend>分段画风（关键位置换画风，其余跟底色）</legend>
    <div className="ai-shorts__row">
      <button type="button" className="header-button" onClick={() => onChange({ ...value, segment_styles: { ...RECOMMENDED_SEGMENT_STYLES } })}>用推荐方案</button>
      <button type="button" className="header-button" disabled={!active && !seg.opening_shots} onClick={() => onChange({ ...value, segment_styles: {} })}>全部跟随底色</button>
      <span className="ai-shorts__muted">推荐：开头年代老照片、讲钱静物、祝福词剪纸、结尾处境油画。</span>
    </div>
    <div className="ai-shorts__visual-grid">
      {SEGMENT_ROLES.map((role) => <label key={role.key} title={role.hint}>{role.label}<select aria-label={`分段画风·${role.label}`} value={seg[role.key] ?? ""} onChange={(e) => set({ [role.key]: e.target.value } as Partial<SegmentStyles>)}>
        <option value="">跟随底色</option>
        {options.map((s) => <option key={s.key} value={s.key}>{s.name}</option>)}
      </select></label>)}
      {seg.opening ? <label>开头算几镜<select aria-label="开头算几镜" value={seg.opening_shots || 1} onChange={(e) => set({ opening_shots: Number(e.target.value) })}>
        {[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n} 镜</option>)}
      </select></label> : null}
    </div>
    <p className="ai-shorts__muted">改了分段画风只影响对应位置的镜头，别的镜头和图不动；变了画风的镜会标"图是旧的"，重生那几张就行。分镜卡上还能单独改某一镜的画风，改过的不再被这里覆盖。</p>
  </fieldset>;
}

export function VisualSettingsFields({ value, onChange, disabled, defaultVideoModel, hideOpening }: { value: VisualSettings; onChange: (v: VisualSettings) => void; disabled?: boolean; defaultVideoModel?: string; hideOpening?: boolean }) {
  const patch = (part: Partial<VisualSettings>) => onChange({ ...value, ...part });
  return <fieldset className="ai-shorts__visual-settings" disabled={disabled}>
    <legend>画面与字幕</legend>
    <label>画面布局<select value={value.layout} onChange={(e) => patch({ layout: e.target.value as VisualSettings["layout"] })}>
      <option value="portrait_full">9:16 竖图全幅</option><option value="portrait_inset">9:16 底板＋16:9 横图</option>
    </select></label>
    <p className="ai-shorts__muted">按文意选择人物行为、物件细节或环境，避免连续重复静物。改布局或画风需重生图片，旧图保留。</p>
    <div className="ai-shorts__visual-grid">
      <label>运动强度<select value={value.motion_strength} onChange={(e) => patch({ motion_strength: e.target.value as VisualSettings["motion_strength"] })}>
        <option value="gentle">轻柔</option><option value="standard">标准</option><option value="none">关闭运动</option>
      </select></label>
      <label>画面切换<select value={value.transition} onChange={(e) => patch({ transition: e.target.value as VisualSettings["transition"] })}>
        <option value="cut">直接切换</option><option value="fade">柔和叠化（画面重叠过渡）</option>
      </select></label>
    </div>
    {!hideOpening && <OpeningVideoFields value={value} onChange={onChange} disabled={disabled} defaultVideoModel={defaultVideoModel} />}
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.caption_enabled} onChange={(e) => patch({ caption_enabled: e.target.checked })} />显示旁白字幕</label>
    <div className="ai-shorts__visual-grid">
      <label>字幕位置<select disabled={!value.caption_enabled} value={value.caption_position} onChange={(e) => patch({ caption_position: e.target.value as VisualSettings["caption_position"] })}>
        <option value="lower">画面下方安全区</option><option value="window">横图窗口下沿</option><option value="middle">画面中央</option>
      </select></label>
      <label>字幕大小<select disabled={!value.caption_enabled} value={value.caption_size} onChange={(e) => patch({ caption_size: Number(e.target.value) })}>
        <option value={9}>紧凑</option><option value={12}>清晰</option><option value={14}>大字</option><option value={16}>特大</option><option value={18}>18 号（推荐，一行一屏）</option><option value={20}>20 号</option>
        {![9,12,14,16,18,20].includes(value.caption_size) ? <option value={value.caption_size}>自定义 {value.caption_size}</option> : null}
      </select></label>
      <label>字幕样式<select disabled={!value.caption_enabled} value={value.caption_style||"outline"} onChange={(e) => patch({ caption_style: e.target.value as VisualSettings["caption_style"] })}>
        <option value="outline">描边（默认）</option><option value="band">半透明底块</option>
      </select></label>
      <label>字幕字色<span className="ai-shorts__color-field"><input aria-label="字幕字色" type="color" disabled={!value.caption_enabled} value={/^#[0-9a-fA-F]{6}$/.test(value.caption_color||"") ? (value.caption_color as string) : DEFAULT_CAPTION_COLOR} onChange={(e) => patch({ caption_color: e.target.value.toUpperCase() })} /><button type="button" className="header-button" disabled={!value.caption_enabled} onClick={() => patch({ caption_color: "#FFFFFF" })}>白</button><button type="button" className="header-button" disabled={!value.caption_enabled} onClick={() => patch({ caption_color: DEFAULT_CAPTION_COLOR })}>黄</button></span></label>
      <label>顶部标题显示<select aria-label="顶部标题显示" value={value.headline_seconds ?? 10} onChange={(e) => patch({ headline_seconds: Number(e.target.value) })}>
        <option value={10}>只在开头 10 秒（推荐）</option><option value={15}>开头 15 秒</option><option value={30}>开头 30 秒</option><option value={HEADLINE_FULL_VIDEO}>贯穿全片</option>
        {![10,15,30,HEADLINE_FULL_VIDEO].includes(value.headline_seconds ?? 10) ? <option value={value.headline_seconds}>自定义 {value.headline_seconds} 秒</option> : null}
      </select></label>
    </div>
    <p className="ai-shorts__muted">字幕按口播稿一行一屏（≤9 字），和顶部标题同色同字号；标题只在开头停留，之后画面保持干净。BGM 固定 -22 dB。</p>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.keywords_enabled} onChange={(e) => patch({ keywords_enabled: e.target.checked })} />字幕内高亮关键词（默认关）</label>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.annotation_enabled} onChange={(e) => patch({ annotation_enabled: e.target.checked })} />显示独立重点标注（画面上方章节式短语，默认关）</label>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.sfx_enabled} onChange={(e) => patch({ sfx_enabled: e.target.checked })} />使用账号音效</label>
    <p className="ai-shorts__muted">运动、字幕和标注可直接重新组装；金额、利率与年份保留完整。字幕和标题在剪映中可继续编辑。</p>
  </fieldset>;
}

const videoPlanHelp: Record<VideoPlan, string> = {
  none: "全片图片＋推拉运动，不产生视频费用。",
  opening: "只把开场前 30 或 60 秒做成 AI 视频，其余镜头用图片。",
  hooks: "三段视频：开场前 60 秒＋中段祝福钩子两镜＋结尾三镜，其余用图片。费用约为全片的 1/3。",
  all: "每一镜都做图生视频，最流畅也最贵，适合短稿。",
  first_n: "只把前 N 镜做成视频，其余用图片。",
};

export function OpeningVideoFields({ value, onChange, disabled, defaultVideoModel }: { value: VisualSettings; onChange: (v: VisualSettings) => void; disabled?: boolean; defaultVideoModel?: string }) {
  const patch = (part: Partial<VisualSettings>) => onChange({ ...value, ...part });
  const plan = videoPlanOf(value);
  const setPlan = (next: VideoPlan) => {
    const part: Partial<VisualSettings> = { video_plan: next };
    if (next === "none") part.opening_video_seconds = 0;
    else if (next === "opening" && !value.opening_video_seconds) part.opening_video_seconds = 60;
    else if (next === "first_n" && !value.video_first_n) part.video_first_n = 3;
    patch(part);
  };
  return <fieldset className="ai-shorts__opening-controls" disabled={disabled}>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={!!value.fast_opening} onChange={(e)=>patch({fast_opening:e.target.checked})} />约前30秒加密分镜（重新拆分镜时生效）</label>
    <div className="ai-shorts__visual-grid">
      <label>AI 视频用法<select aria-label="AI 视频用法" value={plan} onChange={(e)=>setPlan(e.target.value as VideoPlan)}>
        <option value="none">关闭，全片图片</option>
        <option value="hooks">三段式：开场＋中段钩子＋结尾（推荐）</option>
        <option value="opening">只做开场</option>
        <option value="first_n">只做前 N 镜</option>
        <option value="all">全片视频</option>
      </select></label>
      {plan === "opening" ? <label>开场时长<select value={value.opening_video_seconds||60} onChange={(e)=>patch({opening_video_seconds:Number(e.target.value) as 0|30|60})}>
        <option value={30}>前 30 秒</option><option value={60}>前 60 秒</option>
      </select></label> : null}
      {plan === "first_n" ? <label>视频镜数<input aria-label="视频镜数" type="number" min={1} max={60} value={value.video_first_n||3} onChange={(e)=>patch({video_first_n:Math.max(1,Math.min(60,Number(e.target.value)||1))})} /></label> : null}
    </div>
    <p className="ai-shorts__muted">{videoPlanHelp[plan]}</p>
    <label>生视频模型<input aria-label="生视频模型" value={value.video_model||""} placeholder={`默认：${defaultVideoModel||"后台默认视频模型"}`} onChange={(e)=>patch({video_model:e.target.value})} /></label>
    <p className="ai-shorts__muted">视频镜头先按配音定时，再把已有图片生成动态视频；视频单独计费，其余镜头继续用图片。更换视频模型后可单镜重生。原视频声音静音。</p>
  </fieldset>;
}

export function KeywordEditor({ words, narration, onChange }: { words: ShotKeyword[]; narration: string; onChange: (w: ShotKeyword[]) => void }) {
  const set = (index: number, part: Partial<ShotKeyword>) => onChange(words.map((w,i) => i === index ? { ...w, ...part } : w));
  return <div className="ai-shorts__keywords-edit">
    <strong>字幕关键词</strong>
    <p className="ai-shorts__muted">从本镜旁白中选完整词组。数值/概念为金色，风险为浅红；每镜最多3个，也可全部移除。</p>
    {words.map((word, i) => <div key={i} className="ai-shorts__keyword-row">
      <input aria-label={`关键词${i+1}`} value={word.text} placeholder="原文中的词组" onChange={(e) => set(i,{text:e.target.value})} />
      <select aria-label={`关键词${i+1}分类`} value={word.kind} onChange={(e) => set(i,{kind:e.target.value as ShotKeyword["kind"]})}><option value="number">数值</option><option value="concept">概念</option><option value="risk">风险</option></select>
      <button type="button" className="header-button" aria-label={`移除关键词${i+1}`} onClick={() => onChange(words.filter((_,n)=>n!==i))}>移除</button>
    </div>)}
    {words.some(w => w.text && !narration.includes(w.text)) ? <p className="ai-shorts__dirty">请使用旁白中的原词；未对应的词不会进入字幕高亮。</p> : null}
    <button type="button" className="header-button" disabled={words.length>=3} onClick={() => onChange([...words,{text:"",kind:"concept"}])}>添加关键词</button>
  </div>;
}

export function HighlightedText({ text, words = [] }: { text: string; words?: ShotKeyword[] }) {
  const sorted = words.filter(w=>w.text).slice().sort((a,b)=>b.text.length-a.text.length);
  const nodes: ReactNode[] = [];
  let i=0, plain="";
  while(i<text.length) {
    const word=sorted.find(w=>text.startsWith(w.text,i));
    if(word) {
      if(plain) {nodes.push(plain);plain="";}
      nodes.push(<mark key={i} className={`ai-shorts__highlight ai-shorts__highlight--${word.kind}`}>{word.text}</mark>);
      i+=word.text.length;
    } else {plain+=text[i++];}
  }
  if(plain) nodes.push(plain);
  return <>{nodes}</>;
}

export function FramePreview({ shot, imageURL, settings, videoURL }: { shot: AiShot; imageURL: string; settings: VisualSettings; videoURL?: string }) {
  const [moving, setMoving] = useState(false);
  const inset=settings.layout==="portrait_inset";
  const words=settings.keywords_enabled?shot.keywords:[];
  const style={"--caption-factor":settings.caption_size/12, "--caption-color": settings.caption_color || DEFAULT_CAPTION_COLOR, "--motion-time": `${Math.max(3,Math.min(12,shot.seconds||6))}s`} as CSSProperties;
  return <div className="ai-shorts__frame-preview">
    <div className={`ai-shorts__frame ${inset?"is-inset":"is-full"}`} style={style}>
      <div className="ai-shorts__frame-image">
        {videoURL ? <video src={videoURL} poster={imageURL} controls muted playsInline style={{width:"100%",height:"100%",objectFit:"cover"}} /> : imageURL ? <img className={moving && settings.motion_strength!=="none"?`is-moving motion-${shot.camera_move||"zoom_in"} strength-${settings.motion_strength}`:""} src={imageURL} alt={`第 ${shot.index+1} 镜画面`} /> : <div className="ai-shorts__placeholder">{shot.image_status==="running"?"生图中…":"待生成画面"}</div>}
      </div>
      {settings.annotation_enabled && shot.annotation ? <div className="ai-shorts__frame-note">{shot.annotation}</div> : null}
      {settings.caption_enabled ? <div className={`ai-shorts__frame-caption position-${settings.caption_position} style-${settings.caption_style||"outline"}`}><HighlightedText text={shot.caption_lines?.[0] || shot.narration} words={words} /></div> : null}
    </div>
    <div className="ai-shorts__row"><span className="ai-shorts__muted">{inset?"横图底板":"9:16 全幅"} · 字幕位置示意</span>{imageURL&&!videoURL ? <button type="button" className="header-button" onClick={()=>setMoving(!moving)}>{moving?"停止预览":"预览运动"}</button>:null}</div>
  </div>;
}
