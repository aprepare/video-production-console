import { Bot, Clapperboard, LogOut, Moon, Settings2, ShieldCheck, Sun } from "lucide-react";
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
  /** Extra toolbar entries such as the task center trigger. */
  footerSlot?: ReactNode;
  /** Number of required setup items that are not ready; shows a dot on 配置体检. */
  setupPending?: number;
  /** The guide has never been opened; kept for callers while the guide entry is hidden. */
  guideUnseen?: boolean;
};

const workspaces = [
  { id: "references", href: "/references", label: "对标采集" },
  { id: "remix", href: "/", label: "文案二创" },
  { id: "montage", href: "/projects", label: "混剪" },
  { id: "replicate", href: "/replicate", label: "AI 复刻" },
  { id: "image", href: "/image-projects", label: "图文制作" },
  { id: "ai-access", href: "/ai-access", label: "MCP 服务" },
] as const;

export function ConsoleNavigation({ active, theme, onNavigate, onThemeChange, onOpenSettings, onLogout, footerSlot, setupPending = 0 }: Props) {
  const go = (href: string) => (event: React.MouseEvent<HTMLAnchorElement>) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    onNavigate(href);
  };
  return <header className="console-nav" aria-label="控制台导航">
    <a className="console-nav__brand" href="/references" onClick={go("/references")} aria-label="极影智造首页">
      <span className="console-nav__mark"><Clapperboard size={20} strokeWidth={1.9} aria-hidden="true" /></span>
      <span className="console-nav__brand-text"><strong>极影智造</strong><small>JIYING.STUDIO</small></span>
    </a>
    <nav className="console-nav__workspaces" aria-label="制作空间">
      {workspaces.map(({ id, href, label }) => <a
        key={id} href={href}
        aria-current={active === id ? "page" : undefined}
        className={`console-nav__link${active === id ? " console-nav__link--active" : ""}`}
        onClick={go(href)}
      >{id === "ai-access" ? <Bot size={15} aria-hidden="true" /> : null}<strong>{label}</strong></a>)}
    </nav>
    <div className="console-nav__footer">
      {footerSlot}
      <a
        href="/setup"
        className="console-nav__utility console-nav__setup"
        aria-current={active === "setup" ? "page" : undefined}
        aria-label={setupPending ? `配置体检，${setupPending} 项未就绪` : "配置体检"}
        title="配置体检"
        onClick={go("/setup")}
      ><ShieldCheck size={18} aria-hidden="true" /><span>配置体检</span>{setupPending ? <span className="console-nav__dot" aria-hidden="true" /> : null}</a>
      <button type="button" className="console-nav__utility" onClick={onOpenSettings} title="设置" aria-label="设置"><Settings2 size={18} aria-hidden="true" /><span>设置</span></button>
      <label className="console-nav__theme" title="选择界面主题">
        {theme === "dark" ? <Moon size={18} aria-hidden="true" /> : <Sun size={18} aria-hidden="true" />}
        <select aria-label="选择界面主题" value={theme} onChange={(event) => onThemeChange(event.target.value as Theme)}>
          <option value="light">日间主题</option><option value="dark">夜间主题</option>
        </select>
      </label>
      <button type="button" className="console-nav__utility console-nav__logout" onClick={onLogout} title="退出" aria-label="退出"><LogOut size={18} aria-hidden="true" /><span>退出</span></button>
    </div>
  </header>;
}
