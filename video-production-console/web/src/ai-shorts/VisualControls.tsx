import { useState, type CSSProperties, type ReactNode } from "react";
import type { AiShot, ShotKeyword, VisualSettings } from "./api";

export const subjectTypes: Record<string, string> = { object: "物件细节", environment: "建筑与环境", person: "人物行为", comparison: "对比关系", metaphor: "意象示意" };
export const cameraMoves: Record<string, string> = { still: "静止", zoom_in: "缓慢推近", zoom_out: "缓慢拉远", pan_left: "向左平移", pan_right: "向右平移" };

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
        <option value={9}>紧凑</option><option value={12}>清晰（推荐）</option><option value={14}>大字</option><option value={16}>特大</option>
        {![9,12,14,16].includes(value.caption_size) ? <option value={value.caption_size}>自定义 {value.caption_size}</option> : null}
      </select></label>
    </div>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.keywords_enabled} onChange={(e) => patch({ keywords_enabled: e.target.checked })} />字幕内高亮关键词</label>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.annotation_enabled} onChange={(e) => patch({ annotation_enabled: e.target.checked })} />显示独立重点标注</label>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={value.sfx_enabled} onChange={(e) => patch({ sfx_enabled: e.target.checked })} />使用账号音效</label>
    <p className="ai-shorts__muted">运动、字幕和标注可直接重新组装；金额、利率与年份保留完整。字幕和重点标注在剪映中可继续编辑。</p>
  </fieldset>;
}

export function OpeningVideoFields({ value, onChange, disabled, defaultVideoModel }: { value: VisualSettings; onChange: (v: VisualSettings) => void; disabled?: boolean; defaultVideoModel?: string }) {
  const patch = (part: Partial<VisualSettings>) => onChange({ ...value, ...part });
  return <fieldset className="ai-shorts__opening-controls" disabled={disabled}>
    <label className="ai-shorts__toggle"><input type="checkbox" checked={!!value.fast_opening} onChange={(e)=>patch({fast_opening:e.target.checked})} />约前30秒加密分镜（重新拆分镜时生效）</label>
    <label>开场 AI 视频<select value={value.opening_video_seconds||0} onChange={(e)=>patch({opening_video_seconds:Number(e.target.value) as 0|30|60})}>
      <option value={0}>关闭，全片图片</option><option value={30}>前30秒使用AI视频</option><option value={60}>前60秒使用AI视频</option>
    </select></label>
    <label>生视频模型<input aria-label="生视频模型" value={value.video_model||""} placeholder={`默认：${defaultVideoModel||"后台默认视频模型"}`} onChange={(e)=>patch({video_model:e.target.value})} /></label>
    <p className="ai-shorts__muted">开场视频先按配音定时，再将已有图片生成动态视频；视频单独计费，其余镜头继续用图片。更换视频模型后可单镜重生。原视频声音静音。</p>
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
  const style={"--caption-factor":settings.caption_size/12, "--motion-time": `${Math.max(3,Math.min(12,shot.seconds||6))}s`} as CSSProperties;
  return <div className="ai-shorts__frame-preview">
    <div className={`ai-shorts__frame ${inset?"is-inset":"is-full"}`} style={style}>
      <div className="ai-shorts__frame-image">
        {videoURL ? <video src={videoURL} poster={imageURL} controls muted playsInline style={{width:"100%",height:"100%",objectFit:"cover"}} /> : imageURL ? <img className={moving && settings.motion_strength!=="none"?`is-moving motion-${shot.camera_move||"zoom_in"} strength-${settings.motion_strength}`:""} src={imageURL} alt={`第 ${shot.index+1} 镜画面`} /> : <div className="ai-shorts__placeholder">{shot.image_status==="running"?"生图中…":"待生成画面"}</div>}
      </div>
      {settings.annotation_enabled && shot.annotation ? <div className="ai-shorts__frame-note">{shot.annotation}</div> : null}
      {settings.caption_enabled ? <div className={`ai-shorts__frame-caption position-${settings.caption_position}`}><HighlightedText text={shot.caption_lines?.[0] || shot.narration} words={words} /></div> : null}
    </div>
    <div className="ai-shorts__row"><span className="ai-shorts__muted">{inset?"横图底板":"9:16 全幅"} · 字幕位置示意</span>{imageURL&&!videoURL ? <button type="button" className="header-button" onClick={()=>setMoving(!moving)}>{moving?"停止预览":"预览运动"}</button>:null}</div>
  </div>;
}
