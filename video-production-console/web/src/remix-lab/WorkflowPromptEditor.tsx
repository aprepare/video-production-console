import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Download, RotateCw, Save, X } from "lucide-react";
import {
  fetchRemixLabWorkflowPrompts,
  saveRemixLabWorkflowPrompts,
  type RemixLabApi,
  type RemixLabWorkflow,
  type RemixLabWorkflowNode,
  type RemixLabWorkflowPrompts,
} from "./api";
import "./workflow-prompt-editor.css";

type WorkflowPromptEditorProps = {
  api: RemixLabApi;
  accountID?: string;
  ownerLabel: string;
  onClose: () => void;
  onSaved: (result: RemixLabWorkflowPrompts) => void;
};

type PromptKey = "system_prompt" | "user_template";

function editablePromptNodes(workflow: RemixLabWorkflow): RemixLabWorkflowNode[] {
  const rank: Record<string, number> = { writer: 0, reviewer: 1, agent: 2 };
  return workflow.nodes
    .filter((node) =>
      (node.type === "writer" || node.type === "reviewer" || node.type === "agent") &&
      node.config.role !== "reference",
    )
    .sort((left, right) => (rank[left.type] ?? 9) - (rank[right.type] ?? 9));
}

function nodeKind(node: RemixLabWorkflowNode): string {
  if (node.type === "writer") return "写手";
  if (node.type === "reviewer") return "审稿";
  return "前置智能体";
}

function templateVariables(node: RemixLabWorkflowNode): string[] {
  if (node.type === "writer") return ["{{SOURCE}}", "{{NOTES}}"];
  if (node.type === "reviewer") {
    return ["{{source}}", "{{draft}}", "{{annotations}}", "{{facts}}", "{{writing_plan}}"];
  }
  return [];
}

function promptMarkdown(data: RemixLabWorkflowPrompts): string {
  const workflow = data.workflow;
  const sections = editablePromptNodes(workflow).map((node) => [
    `## ${node.title}（${nodeKind(node)}）`,
    "",
    "### System prompt",
    "",
    node.config.system_prompt ?? "",
    "",
    "### User template",
    "",
    node.config.user_template ?? "",
  ].join("\n"));
  return [
    `# ${workflow.name} · 二创提示词`,
    "",
    "## 公共写作规则",
    "",
    workflow.editorial_rules ?? "",
    "",
    ...sections.flatMap((section) => [section, ""]),
    "## 写手输出格式合同（只读）",
    "",
    data.writer_contract,
    "",
    "## 审稿输出格式合同（只读）",
    "",
    data.reviewer_contract,
    "",
  ].join("\n");
}

export function WorkflowPromptEditor({ api, accountID, ownerLabel, onClose, onSaved }: WorkflowPromptEditorProps) {
  const [data, setData] = useState<RemixLabWorkflowPrompts | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      setData(await fetchRemixLabWorkflowPrompts(api, accountID));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "二创提示词读取失败。");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    // 对话框的账号在打开时冻结；切换账号会先关闭并重新打开。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const nodes = useMemo(() => data ? editablePromptNodes(data.workflow) : [], [data]);
  const ready = Boolean(data);
  useEffect(() => {
    if (ready) dialogRef.current?.querySelector<HTMLTextAreaElement>("textarea:not(:disabled)")?.focus();
  }, [ready]);

  const patchWorkflow = (patch: Partial<RemixLabWorkflow>) => {
    setData((current) => current ? { ...current, workflow: { ...current.workflow, ...patch } } : current);
  };

  const patchNodePrompt = (nodeID: string, key: PromptKey, value: string) => {
    if (!data) return;
    patchWorkflow({
      nodes: data.workflow.nodes.map((node) => node.id === nodeID
        ? { ...node, config: { ...node.config, [key]: value } }
        : node),
    });
  };

  const save = async () => {
    if (!data || saving) return;
    setSaving(true);
    setError("");
    try {
      const saved = await saveRemixLabWorkflowPrompts(api, data.workflow, accountID);
      onSaved(saved);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "二创提示词保存失败。");
    } finally {
      setSaving(false);
    }
  };

  const download = () => {
    if (!data) return;
    const blob = new Blob([promptMarkdown(data)], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    const safeName = data.workflow.name.replace(/[\\/:*?"<>|]+/g, "-").trim() || "二创工作流";
    anchor.href = url;
    anchor.download = `${safeName}-提示词.md`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  };

  return createPortal(
    <div className="modal-backdrop workflow-prompt-editor__backdrop" onClick={saving ? undefined : onClose}>
      <div
        ref={dialogRef}
        className="preview-modal workflow-prompt-editor"
        role="dialog"
        aria-modal="true"
        aria-labelledby="workflow-prompt-editor-title"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key === "Escape" && !saving) onClose();
          if (event.key !== "Tab") return;
          const items = dialogRef.current?.querySelectorAll<HTMLElement>(
            "button:not(:disabled), input:not(:disabled), textarea:not(:disabled), summary",
          );
          if (!items?.length) return;
          const first = items[0];
          const last = items[items.length - 1];
          if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus();
          }
          if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus();
          }
        }}
      >
        <header className="workflow-prompt-editor__head">
          <div>
            <span className="workflow-prompt-editor__eyebrow">{ownerLabel} · 后续新跑与重新二创生效</span>
            <h2 id="workflow-prompt-editor-title">编辑二创提示词</h2>
          </div>
          <button type="button" className="close" aria-label="关闭二创提示词" disabled={saving} onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </header>

        {loading ? <p className="workflow-prompt-editor__state">正在读取实际提示词…</p> : null}
        {!loading && !data ? (
          <div className="workflow-prompt-editor__state">
            <p>{error || "没有读取到提示词。"}</p>
            <button type="button" className="header-button remix-lab-icon-btn" onClick={() => void load()}>
              <RotateCw size={15} aria-hidden="true" />
              重新读取
            </button>
          </div>
        ) : null}

        {data ? (
          <>
            <fieldset className="workflow-prompt-editor__scroll" disabled={saving}>
              <section className="workflow-prompt-editor__rules">
                <div className="workflow-prompt-editor__section-head">
                  <div>
                    <h3>公共写作规则</h3>
                    <p>统一应用于写手与审稿。留空会明确停用公共规则。</p>
                  </div>
                </div>
                <textarea
                  aria-label="公共写作规则"
                  rows={7}
                  value={data.workflow.editorial_rules ?? ""}
                  placeholder="留空表示不使用公共写作规则"
                  onChange={(event) => patchWorkflow({ editorial_rules: event.target.value })}
                />
              </section>

              {nodes.map((node) => (
                <section className="workflow-prompt-editor__node" key={node.id}>
                  <div className="workflow-prompt-editor__section-head">
                    <div>
                      <span>{nodeKind(node)}</span>
                      <h3>{node.title}</h3>
                    </div>
                    {templateVariables(node).length ? (
                      <div className="workflow-prompt-editor__variables" aria-label={`${node.title}可用变量`}>
                        {templateVariables(node).map((variable) => <code key={variable}>{variable}</code>)}
                      </div>
                    ) : null}
                  </div>
                  <label>
                    System prompt
                    <textarea
                      rows={9}
                      value={node.config.system_prompt ?? ""}
                      onChange={(event) => patchNodePrompt(node.id, "system_prompt", event.target.value)}
                    />
                  </label>
                  <label>
                    User template
                    <textarea
                      rows={9}
                      value={node.config.user_template ?? ""}
                      onChange={(event) => patchNodePrompt(node.id, "user_template", event.target.value)}
                    />
                  </label>
                </section>
              ))}

              <details className="workflow-prompt-editor__contracts">
                <summary>输出格式合同（只读）</summary>
                <p>这些内容由系统维护，用来保证写手和审稿结果可被正确解析。</p>
                <label>
                  写手输出格式
                  <textarea readOnly rows={8} value={data.writer_contract} />
                </label>
                <label>
                  审稿输出格式
                  <textarea readOnly rows={8} value={data.reviewer_contract} />
                </label>
              </details>
            </fieldset>

            <footer className="workflow-prompt-editor__footer">
              <span role="alert">{error}</span>
              <button type="button" className="header-button remix-lab-icon-btn" disabled={saving} onClick={download}>
                <Download size={15} aria-hidden="true" />
                下载全部 Markdown
              </button>
              <button
                type="button"
                className="remix-lab-start remix-lab-icon-btn"
                disabled={saving}
                aria-busy={saving}
                onClick={() => void save()}
              >
                <Save size={15} aria-hidden="true" />
                {saving ? "正在保存…" : "保存提示词"}
              </button>
            </footer>
          </>
        ) : null}
      </div>
    </div>,
    document.body,
  );
}
