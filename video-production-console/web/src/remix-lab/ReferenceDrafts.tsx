import { useEffect, useState } from "react";
import { fetchRemixLabRunStages, type RemixLabApi, type RemixLabReferenceDraft } from "./api";

export function ReferenceDrafts({ drafts, error="", onMessage }: {
  drafts:RemixLabReferenceDraft[]; error?:string; onMessage:(text:string)=>void;
}) {
  const [selectedID,setSelectedID]=useState("");
  const [tab,setTab]=useState<"copy"|"publishing"|"analysis">("copy");
  const [open,setOpen]=useState(true);
  if(!drafts.length&&!error) return null;
  const selected=drafts.find(draft=>draft.id===selectedID)??[...drafts].reverse().find(draft=>draft.status==="completed")??drafts.at(-1);
  const versions=new Map<string,number>();
  const labels=new Map<string,string>();
  for(const draft of drafts) {
    const version=(versions.get(draft.node_id)??0)+1; versions.set(draft.node_id,version);
    labels.set(draft.id,`${draft.title} · ${draft.model} · 第${version}版${draft.status==="failed"?" · 未完成":""}`);
  }
  async function copy(text:string,label:string) {
    try { await navigator.clipboard.writeText(text);onMessage(`已复制${label}。`); }
    catch { onMessage("复制失败，可以选中文字后复制。"); }
  }
  const list=(values:string[]|undefined,label:string)=><ul className="remix-reference-drafts__list">{(values??[]).map((value,index)=><li key={index}>
    <span>{value}</span><button type="button" className="header-button" aria-label={`复制${label}${index+1}`} onClick={()=>void copy(value,label)}>复制</button>
  </li>)}</ul>;
  return <section className="remix-reference-drafts" aria-label="多模型参考稿">
    <div className="remix-reference-drafts__head"><div><h3>多模型参考稿</h3><span className="remix-lab-muted">已保留 {drafts.length} 个版本 · 修改定稿时可随时查阅</span></div>
      <button type="button" className="header-button" aria-expanded={open} onClick={()=>setOpen(value=>!value)}>{open?"收起参考稿":"展开参考稿"}</button></div>
    {error?<p role="alert">{error}</p>:null}
    {open&&selected?<>
      <label className="remix-reference-drafts__select"><span>模型与版本</span><select aria-label="选择参考稿版本" value={selected.id} onChange={event=>setSelectedID(event.target.value)}>
        {drafts.map(draft=><option key={draft.id} value={draft.id}>{labels.get(draft.id)}</option>)}
      </select></label>
      <p className="remix-lab-muted">{new Date(selected.created_at).toLocaleString()} · {selected.model}{selected.reasoning_effort?` · ${selected.reasoning_effort}`:""}{selected.service_tier==="priority"?" · Fast":""}</p>
      {selected.status==="failed"?<div role="status"><p className="remix-lab-run__error">{selected.error||"此模型未生成完整参考稿。"}</p>
        <details><summary>查看保留的原始输出</summary><pre className="remix-reference-drafts__raw">{selected.raw||"服务未返回正文。"}</pre></details></div>:<>
        <nav className="remix-reference-drafts__tabs" aria-label="参考稿内容">
          {([["copy","完整文案"],["publishing","标题与视频描述"],["analysis","拆解说明"]] as const).map(([key,label])=><button key={key} type="button" className="header-button" aria-pressed={tab===key} onClick={()=>setTab(key)}>{label}</button>)}
        </nav>
        {tab==="copy"?<><textarea aria-label="参考文案" className="remix-reference-drafts__copy" readOnly value={selected.copy.continuous_script} rows={10}/>
          <button type="button" className="header-button" onClick={()=>void copy(selected.copy.continuous_script,"参考全文")}>复制参考全文</button></>:null}
        {tab==="publishing"?<div className="remix-reference-drafts__publishing"><section><h4>标题候选</h4>{list(selected.copy.titles,"标题")}</section><section><h4>视频描述</h4>{list(selected.copy.descriptions,"视频描述")}</section></div>:null}
        {tab==="analysis"?<div className="remix-reference-drafts__analysis">{([["opening","开头"],["middle","中段"],["ending","课尾"]] as const).map(([key,label])=><section key={key}><h4>{label}</h4><p>{selected.copy.analysis?.[key]||"本模型未提供此项说明。"}</p></section>)}</div>:null}
      </>}
    </>:null}
  </section>;
}

export function RunReferenceDrafts({api,runID,onMessage}:{api:RemixLabApi;runID:string;onMessage:(text:string)=>void}) {
  const [data,setData]=useState<{runID:string;drafts:RemixLabReferenceDraft[];error?:string}>({runID:"",drafts:[]});
  const [reload,setReload]=useState(0);
  useEffect(()=>{
    let cancelled=false;
    void fetchRemixLabRunStages(api,runID).then(view=>{
      if(!cancelled) setData({runID,drafts:view.reference_drafts??[],error:view.reference_error});
    }).catch(reason=>{
      if(!cancelled) setData({runID,drafts:[],error:reason instanceof Error?reason.message:"参考稿读取失败。"});
    });
    return ()=>{cancelled=true;};
  },[api,runID,reload]);
  if(data.runID!==runID) return null;
  return <><ReferenceDrafts key={runID} drafts={data.drafts} error={data.error} onMessage={onMessage}/>
    {data.error?<button type="button" className="header-button" onClick={()=>setReload(value=>value+1)}>重新读取参考稿</button>:null}</>;
}
