import { Clapperboard, Film, Images, LogOut, Moon, PenLine, Settings2, Sun } from "lucide-react";
import type { Theme } from "../types";
import "./console-navigation.css";

type Props = {
  active: "remix" | "image" | "ai" | null;
  theme: Theme;
  onNavigate: (href: string) => void;
  onThemeChange: (theme: Theme) => void;
  onOpenSettings: () => void;
  onLogout: () => void;
};

const workspaces = [
  { id: "remix", href: "/", label: "文案与混剪", description: "从灵感到成片", icon: PenLine },
  { id: "image", href: "/image-projects", label: "图文制作", description: "让内容被看见", icon: Images },
  { id: "ai", href: "/ai-shorts", label: "AI短片", description: "把故事变成画面", icon: Film },
] as const;

export function ConsoleNavigation({ active, theme, onNavigate, onThemeChange, onOpenSettings, onLogout }: Props) {
  return <aside className="console-nav" aria-label="控制台导航">
    <div className="console-nav__brand">
      <span className="console-nav__mark"><Clapperboard size={23} strokeWidth={1.8} aria-hidden="true" /></span>
      <span className="console-nav__brand-text"><strong>创作工作室</strong><small>VIDEO CONSOLE</small></span>
    </div>
    <div className="console-nav__section-label">制作空间</div>
    <nav className="console-nav__workspaces" aria-label="制作空间">
      {workspaces.map(({ id, href, label, description, icon: Icon }) => <a
        key={id} href={href} aria-label={label} title={label}
        aria-current={active === id ? "page" : undefined}
        className={`console-nav__link${active === id ? " console-nav__link--active" : ""}`}
        onClick={(event) => {
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
          onNavigate(href);
        }}
      ><Icon size={20} strokeWidth={1.8} aria-hidden="true" /><span><strong>{label}</strong><small>{description}</small></span></a>)}
    </nav>
    <div className="console-nav__footer">
      <button type="button" className="console-nav__utility" onClick={onOpenSettings} title="设置" aria-label="设置"><Settings2 size={18} aria-hidden="true" /><span>设置</span></button>
      <label className="console-nav__theme" title="选择界面主题">
        {theme === "dark" ? <Moon size={18} aria-hidden="true" /> : <Sun size={18} aria-hidden="true" />}
        <select aria-label="选择界面主题" value={theme} onChange={(event) => onThemeChange(event.target.value as Theme)}>
          <option value="light">日间主题</option><option value="dark">夜间主题</option>
        </select>
      </label>
      <button type="button" className="console-nav__utility console-nav__logout" onClick={onLogout} title="退出" aria-label="退出"><LogOut size={18} aria-hidden="true" /><span>退出</span></button>
      <div className="console-nav__footnote">专注内容，有序创作。</div>
    </div>
  </aside>;
}
