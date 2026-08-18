import { ArrowRight } from "lucide-react";
import type { ProductionModeDefinition } from "./catalog";
import "./mode-home.css";

type ModeHomeProps = {
  modes: readonly ProductionModeDefinition[];
  onNavigate: (href: string) => void;
};

export function ModeHome({ modes, onNavigate }: ModeHomeProps) {
  return (
    <main className="mode-home">
      <div className="mode-home-intro">
        <span className="mode-home-eyebrow">视频生产控制台</span>
        <h1>选择制作方式</h1>
      </div>
      <div className="mode-home-grid">
        {modes.map((mode) => {
          const Icon = mode.icon;
          return (
            <article
              className={`mode-choice mode-choice--${mode.accent}`}
              key={mode.id}
            >
              <div className="mode-choice-icon" aria-hidden="true">
                <Icon size={28} strokeWidth={1.8} />
              </div>
              <div className="mode-choice-copy">
                <h2>{mode.title}</h2>
                <p>{mode.description}</p>
              </div>
              <button type="button" onClick={() => onNavigate(mode.href)}>
                进入{mode.title}
                <ArrowRight size={18} aria-hidden="true" />
              </button>
            </article>
          );
        })}
      </div>
    </main>
  );
}
