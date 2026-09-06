import { Check, ChevronRight, Circle, LoaderCircle } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import "./fixed-flow-steps.css";

export type FixedFlowStep = {
  id: string;
  title: string;
  subtitle?: string;
  status?: string;
  icon?: LucideIcon;
  group: "create" | "produce";
};

const statusLabels: Record<string, string> = {
  ok: "已完成", completed: "已完成", running: "进行中", failed: "失败",
  missing: "待执行", queued: "排队中", waiting: "待确认", waiting_confirm: "待确认",
  skipped: "已跳过", blocked: "待处理", pending: "待执行",
};

export function FixedFlowSteps({ steps, selectedID, onSelect, label = "流程步骤" }: {
  steps: FixedFlowStep[];
  selectedID: string;
  onSelect: (id: string) => void;
  label?: string;
}) {
  return (
    <nav className="fixed-flow-steps" aria-label={label}>
      <div className="fixed-flow-steps__intro">
        <strong>流程步骤</strong>
        <span>点选步骤，查看配置与结果</span>
      </div>
      {(["create", "produce"] as const).map((group, phase) => {
        const items = steps.filter((step) => step.group === group);
        if (!items.length) return null;
        return (
          <section className="fixed-flow-steps__group" key={group} aria-label={group === "create" ? "文案创作步骤" : "混剪制作步骤"}>
            <header><span>0{phase + 1}</span><h3>{group === "create" ? "文案创作" : "混剪制作"}</h3></header>
            <ol>
              {items.map((step, index) => {
                const Icon = step.icon || Circle;
                const done = step.status === "ok" || step.status === "completed";
                return (
                  <li key={step.id}>
                    <button type="button" className="fixed-flow-step" aria-pressed={selectedID === step.id}
                      data-status={step.status || "config"} onClick={() => onSelect(step.id)}>
                      <span className="fixed-flow-step__top"><Icon size={16} aria-hidden="true" /><span>{String(index + 1).padStart(2, "0")}</span></span>
                      <strong>{step.title}</strong>
                      {step.subtitle ? <small title={step.subtitle}>{step.subtitle}</small> : null}
                      <span className="fixed-flow-step__foot">
                        <span>{step.status === "running" ? <LoaderCircle size={12} className="fixed-flow-step__spinner" aria-hidden="true" /> : done ? <Check size={12} aria-hidden="true" /> : null}{statusLabels[step.status || ""] || step.status || "配置"}</span>
                        <ChevronRight size={13} aria-hidden="true" />
                      </span>
                    </button>
                  </li>
                );
              })}
            </ol>
          </section>
        );
      })}
    </nav>
  );
}
