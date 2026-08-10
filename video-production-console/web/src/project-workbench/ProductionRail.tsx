import { CircleCheck } from "lucide-react";
import type { CSSProperties } from "react";
import type { ProductionStage } from "./types";
import { productionStages } from "./workflow";

type ProductionRailProps = {
  currentStage: ProductionStage;
};

export function ProductionRail({ currentStage }: ProductionRailProps) {
  const currentIndex = productionStages.findIndex((stage) => stage.id === currentStage);

  return (
    <nav
      className="production-rail"
      aria-label="五阶段生产轨"
      style={{ "--rail-progress": `${Math.max(currentIndex, 0) * 25}%` } as CSSProperties}
    >
      <div className="production-rail__heading">
        <span>PRODUCTION ROUTE</span>
        <strong>{String(currentIndex + 1).padStart(2, "0")} <small>/ 05</small></strong>
      </div>
      <div className="production-rail__stages">
        <div className="production-rail__line" aria-hidden="true"><span /></div>
        {productionStages.map((stage, index) => {
          const state = index < currentIndex ? "complete" : index === currentIndex ? "current" : "upcoming";
          return (
            <div
              className={`production-rail__stage production-rail__stage--${state}`}
              key={stage.id}
              aria-current={state === "current" ? "step" : undefined}
            >
              <span className="production-rail__node" aria-hidden="true">
                {state === "complete" ? <CircleCheck size={17} /> : <span>{index + 1}</span>}
              </span>
              <span className="production-rail__copy">
                <span className="production-rail__label">{stage.label}</span>
                <span className="production-rail__state">
                  {state === "complete" ? "已完成" : state === "current" ? "正在制作" : "待开始"}
                </span>
              </span>
            </div>
          );
        })}
      </div>
    </nav>
  );
}
