import { Bot, Clapperboard, Film, Images, LogOut, Moon, PenLine, Radar, Scissors, Settings2, ShieldCheck, Store, Sun } from "lucide-react";
import type { ReactNode } from "react";
import type { Theme } from "../types";
import "./console-navigation.css";

type Props = {
  active: "remix" | "montage" | "replicate" | "image" | "references" | "merchants" | "setup" | "guide" | "ai-access" | null;
  theme: Theme;
  onNavigate: (href: string) => void;
  onThemeChange: (theme: Theme) => void;
  onOpenSettings: () => void;
  onLogout: () => void;
  /** Extra footer entries such as the task center trigger. */
  footerSlot?: ReactNode;
  /** Number of required setup items that are not ready; shows a dot on 配置体检. */
  setupPending?: number;
  /** The guide has never been opened; shows a dot on 新手引导. */
  guideUnseen?: boolean;
};

const workspaces = [
  { id: "merchants", href: "/merchants", label: "商户与订单", description: "交付、授权与成本", icon: Store },
  { id: "references", href: "/references", label: "对标采集", description: "爆款口播与热评", icon: Radar },
  { id: "remix", href: "/", label: "文案二创", description: "改写爆款文案", icon: PenLine },
  { id: "montage", href: "/projects", label: "混剪", description: "配音字幕剪映草稿", icon: Scissors },
  { id: "replicate", href: "/replicate", label: "AI 复刻", description: "分镜生成与质检", icon: Film },
  { id: "image", href: "/image-projects", label: "图文制作", description: "让内容被看见", icon: Images },
] as const;

export function ConsoleNavigation({ active, theme, onNavigate, onThemeChange, onOpenSettings, onLogout, footerSlot, setupPending = 0 }: Props) {
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
      <a
        href="/ai-access"
        className="console-nav__utility console-nav__setup"
        aria-current={active === "ai-access" ? "page" : undefined}
        aria-label="AI 接入（MCP）"
        title="AI 接入（MCP）"
        onClick={(event) => {
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
          onNavigate("/ai-access");
        }}
      ><Bot size={18} aria-hidden="true" /><span>AI 接入</span></a>
      {footerSlot}
      <a
        href="/setup"
        className="console-nav__utility console-nav__setup"
        aria-current={active === "setup" ? "page" : undefined}
        aria-label={setupPending ? `配置体检，${setupPending} 项未就绪` : "配置体检"}
        title="配置体检"
        onClick={(event) => {
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
          onNavigate("/setup");
        }}
      ><ShieldCheck size={18} aria-hidden="true" /><span>配置体检</span>{setupPending ? <span className="console-nav__dot" aria-hidden="true" /> : null}</a>
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
