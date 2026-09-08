import {useEffect,useState} from "react";
import type {AiShort} from "./api";

// 09-08 实测（grok-4.6-fast，同一块 218 字）：low 49 秒，high 319 秒，拆出来的镜头数一样。
// 拆分镜是并行的，总时长 ≈ 最慢那一块，所以强度直接决定等多久。
const REASONING_HINT: Record<string, string> = {
 "": "默认 low：一块约 1 分钟，整篇一两分钟。",
 none: "不思考，最快，画面描述会粗一些。", minimal: "接近 low。", low: "一块约 1 分钟，整篇一两分钟。",
 medium: "一块约 2～3 分钟。", high: "一块约 5 分钟，整篇 5～9 分钟；画面描述和 low 差别不大，不推荐。", xhigh: "比 high 更慢，不推荐。",
};
export function ReasoningSelect({value,onChange,disabled}:{value:string;onChange:(v:string)=>void;disabled?:boolean}) {
 const slow = value === "high" || value === "xhigh";
 return <label>分镜思考强度<select aria-label="分镜思考强度" value={value} disabled={disabled} onChange={e=>onChange(e.target.value)}>
  <option value="">默认（low，推荐）</option>{["none","minimal","low","medium","high","xhigh"].map(v=><option key={v} value={v}>{v}</option>)}
 </select><small className={slow ? "ai-shorts__dirty" : undefined}>{REASONING_HINT[value] ?? REASONING_HINT[""]} 只影响拆分镜，不影响生图。</small></label>;
}
export function AssemblyStatus({short}:{short:AiShort}) {
 const [clock,setClock]=useState(Date.now());
 useEffect(()=>{if(short.status!=="assembling")return;const timer=window.setInterval(()=>setClock(Date.now()),1000);return()=>window.clearInterval(timer)},[short.status]);
 const p=short.assembly_progress;
 const running=short.status==="assembling",done=short.status==="assembled",failed=short.status==="failed"&&!!p&&p.stage<4;
 if(!running&&!done&&!failed)return null;
 const elapsed=p?Math.max(0,Math.floor((clock-Date.parse(p.started_at))/1000)):0;
 const stage=done?4:Math.max(0,Math.min(3,p?.stage??0));
 return <section className="ai-shorts__assembly-status" role="status" aria-live="polite">
  <strong>{done?"草稿已成功导入剪映":failed?"草稿导出失败，可重试":p?.message||"正在准备配音与草稿…"}</strong>
  <progress aria-label="草稿导出阶段进度" max={4} value={stage}/>
  <div className="ai-shorts__assembly-steps">{["配音","时间对齐","组装草稿","导入剪映"].map((name,i)=><span key={name} aria-current={running&&i===stage?"step":undefined}>{i<stage?"已完成 · ":i===stage&&running?"进行中 · ":""}{name}</span>)}</div>
  <small>{done?short.draft_name:failed?short.error:`阶段进度 ${stage}/4${Number.isFinite(elapsed)?` · 已用时 ${Math.floor(elapsed/60)}分${elapsed%60}秒`:""}，上游未返回阶段内百分比，完成后会自动更新。`}</small>
 </section>;
}
