import {useEffect,useState} from "react";
import type {AiShort} from "./api";

export function ReasoningSelect({value,onChange,disabled}:{value:string;onChange:(v:string)=>void;disabled?:boolean}) {
 return <label>分镜思考强度<select aria-label="分镜思考强度" value={value} disabled={disabled} onChange={e=>onChange(e.target.value)}>
  <option value="">默认（low）</option>{["none","minimal","low","medium","high","xhigh"].map(v=><option key={v} value={v}>{v}</option>)}
 </select><small>用于分大段与拆分镜，需接口和模型支持。</small></label>;
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
