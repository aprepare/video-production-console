import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { RemixLabDefaults, RemixLabRerunModel, RemixLabWorkflow } from "./api";
import { ReferenceModelFields } from "./ReferenceModelFields";
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
  ]).filter(({node})=>Boolean(node)).map(({label,node}) => ({label, id:node?.id, value: {
    model: node?.config.model || (label === "审稿" ? defaults?.remix_model : fallbackWriter.model) || "",
    reasoning_effort: node?.config.reasoning_effort || (label === "审稿" ? defaults?.remix_reasoning_effort : fallbackWriter.reasoning_effort) || "",
    service_tier: node?.config.service_tier || (label === "写手" ? fallbackWriter.service_tier : "default"),
  } as RemixLabRerunModel})));
  const [references, setReferences] = useState(() => workflow.nodes.filter(isReferenceNode).map(node => ({
    model: node.config.model || fallbackWriter.model,
    reasoning_effort: node.config.reasoning_effort || fallbackWriter.reasoning_effort,
    service_tier: node.config.service_tier || "default",
  } as RemixLabRerunModel)));
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
      await onSave(replaceReferenceModels(updated,references));
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
      <div className="modal-head"><div><span className="muted">{ownerLabel} · 从下一次二创开始生效</span><h2 id="role-models-title">模型</h2></div>
        <button type="button" className="close" aria-label="关闭模型配置" disabled={busy} onClick={onClose}><X size={20} aria-hidden="true" /></button></div>
      <p className="remix-lab-muted">二创分两步：写手把对标原文改写成你的文案，审稿再检查一遍，有问题直接改。两步可以用不同的模型。历史稿件不受影响。</p>
      {error ? <p role="alert">{error}</p> : null}
      <div className="remix-lab-slots remix-lab-slots--main">
        {roles.map((role,index) => role.label === "二创策划" ? null : <fieldset key={role.label} disabled={busy || !role.id} className="remix-lab-slot"><legend>{role.label}</legend>
          <p className="remix-lab-role-hint">{role.label === "写手" ? "负责改写：照着对标的结构，写成你的新文案。" : "负责把关：检查事实和规则，不合格的地方直接改好。"}</p>
          {role.id ? <div className="remix-lab-slot__fields"><RoleModelFields id={`role-${role.id}`} label={role.label} value={role.value}
            onChange={value => setRoles(current => current.map((item,i) => i===index ? {...item,value} : item))} /></div>
            : <p className="remix-lab-muted">当前账号没有开启这一步。</p>}
        </fieldset>)}
      </div>
      <details className="remix-lab-advanced-models">
        <summary>高级设置（策划、参考模型、旧版连接）</summary>
        <div className="remix-lab-slots">
          {roles.map((role,index) => role.label !== "二创策划" ? null : <fieldset key={role.label} disabled={busy || !role.id} className="remix-lab-slot"><legend>{role.label}</legend>
            <p className="remix-lab-role-hint">写稿前先想好钩子和角度。不填就用写手的模型。</p>
            {role.id ? <div className="remix-lab-slot__fields"><RoleModelFields id={`role-${role.id}`} label={role.label} value={role.value}
              onChange={value => setRoles(current => current.map((item,i) => i===index ? {...item,value} : item))} /></div> : null}
          </fieldset>)}
        </div>
        <ReferenceModelFields values={references} onChange={setReferences} fallback={fallbackWriter} disabled={busy} />
        {onConnections ? <button type="button" className="header-button" disabled={busy} onClick={onConnections}>连接与旧版预设</button> : null}
      </details>
      <div className="remix-lab-modal__actions">
        <button type="button" className="remix-lab-start" disabled={busy || references.some(value => !value.model.trim())} onClick={() => void save()}>{busy ? "正在保存…" : "保存并完成"}</button>
      </div>
    </div>
  </div>, document.body);
}
