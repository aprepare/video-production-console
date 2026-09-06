import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { RemixLabDefaults, RemixLabRerunModel, RemixLabWorkflow } from "./api";
import { RoleModelFields } from "./RoleModelFields";
import { isReferenceNode, replaceReferenceModels } from "./reference-workflow";

export function RoleModelsDialog({ workflow, defaults, ownerLabel, onSave, onClose, onConnections }: {
  workflow: RemixLabWorkflow; defaults?: RemixLabDefaults | null; ownerLabel: string;
  onSave: (workflow: RemixLabWorkflow) => Promise<void>; onClose: () => void; onConnections?: () => void;
}) {
  const writer = workflow.nodes.find(node => node.type === "writer");
  const preset = defaults?.presets[0];
  const fallbackWriter = {
    model: writer?.config.model || preset?.model || defaults?.remix_model || "",
    reasoning_effort: writer?.config.reasoning_effort || preset?.reasoning_effort || defaults?.remix_reasoning_effort || "",
    service_tier: writer?.config.service_tier || preset?.service_tier || "default",
  };
  const [roles, setRoles] = useState(() => ([
    {label:"二创策划", node:workflow.nodes.find(node => node.id === "hook" && node.type === "agent" && !isReferenceNode(node))},
    {label:"写手", node:writer},
    {label:"审稿", node:workflow.nodes.find(node => node.type === "reviewer")},
  ]).filter(({label,node})=>label!=="二创策划"||node).map(({label,node}) => ({label, id:node?.id, value: {
    model: node?.config.model || (label === "审稿" ? defaults?.remix_model : fallbackWriter.model) || "",
    reasoning_effort: node?.config.reasoning_effort || (label === "审稿" ? defaults?.remix_reasoning_effort : fallbackWriter.reasoning_effort) || "",
    service_tier: node?.config.service_tier || (label === "写手" ? fallbackWriter.service_tier : "default"),
  } as RemixLabRerunModel})));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const lock = useRef(false);
  const dialog = useRef<HTMLDivElement>(null);
  useEffect(() => { dialog.current?.querySelector<HTMLInputElement>("input")?.focus(); }, []);
  async function save() {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError("");
    try {
      const choices = new Map(roles.filter(role => role.id).map(role => [role.id, role.value]));
      const updated = {...workflow, nodes:workflow.nodes.map(node => {
        const choice = choices.get(node.id);
        return choice ? {...node, config:{...node.config, ...choice, model:choice.model.trim()}} : node;
      })};
      await onSave(replaceReferenceModels(updated,[]));
      onClose();
    } catch (reason) { setError(reason instanceof Error ? reason.message : "模型配置保存失败。"); }
    finally { lock.current = false; setBusy(false); }
  }
  return createPortal(<div className="modal-backdrop" onClick={() => { if (!busy) onClose(); }}>
    <div ref={dialog} className="preview-modal remix-lab-modal remix-lab-modal--wide" role="dialog" aria-modal="true" aria-labelledby="role-models-title"
      onClick={event => event.stopPropagation()} onKeyDown={event => {
        if(event.key === "Escape" && !busy) onClose();
        if(event.key === "Tab") {
          const items = dialog.current?.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled), select:not(:disabled)");
          if(!items?.length) return;
          const first=items[0], last=items[items.length-1];
          if(event.shiftKey && document.activeElement===first) { event.preventDefault(); last.focus(); }
          if(!event.shiftKey && document.activeElement===last) { event.preventDefault(); first.focus(); }
        }
      }}>
      <div className="modal-head"><div><span className="muted">{ownerLabel} · 下一轮使用</span><h2 id="role-models-title">模型配置</h2></div>
        <button type="button" className="close" aria-label="关闭模型配置" disabled={busy} onClick={onClose}>×</button></div>
      <p className="remix-lab-muted">参考模型先独立出稿，写手借鉴后完整重写，审稿负责定稿检查。保存后用于此账号的新建和重新生成，历史稿件保留。</p>
      {error ? <p role="alert">{error}</p> : null}
      <div className="remix-lab-slots">
        {roles.map((role,index) => <fieldset key={role.label} disabled={busy || !role.id} className="remix-lab-slot"><legend>{role.label}</legend>
          {role.id ? <div className="remix-lab-slot__fields"><RoleModelFields id={`role-${role.id}`} label={role.label} value={role.value}
            onChange={value => setRoles(current => current.map((item,i) => i===index ? {...item,value} : item))} /></div>
            : <p className="remix-lab-muted">当前工作流未启用此环节。</p>}
        </fieldset>)}
      </div>
      <div className="remix-lab-modal__actions">
        {onConnections ? <button type="button" className="header-button" disabled={busy} onClick={onConnections}>连接与旧版预设</button> : null}
        <button type="button" className="remix-lab-start" disabled={busy || roles.some(role => role.id && !role.value.model.trim())} onClick={() => void save()}>{busy ? "正在保存…" : "保存并完成"}</button>
      </div>
    </div>
  </div>, document.body);
}
