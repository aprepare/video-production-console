// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConsoleNavigation } from "./ConsoleNavigation";

afterEach(cleanup);

test("navigation marks the active workspace and preserves modified link clicks", () => {
  const navigate = vi.fn();
  render(<ConsoleNavigation active="image" theme="light" onNavigate={navigate} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} />);
  const image = screen.getByRole("link", { name: "图文制作" });
  expect(image.getAttribute("aria-current")).toBe("page");
  const remix = screen.getByRole("link", { name: "文案二创" });
  expect(remix.getAttribute("href")).toBe("/");
  fireEvent.click(remix);
  expect(navigate).toHaveBeenCalledExactlyOnceWith("/");
  fireEvent.click(remix, { ctrlKey: true });
  expect(navigate).toHaveBeenCalledTimes(1);
});

test("navigation lists merchant, reference, copy, montage, replicate and image workspaces", () => {
  render(<ConsoleNavigation active="remix" theme="light" onNavigate={vi.fn()} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} />);
  const workspaces = within(screen.getByRole("navigation", { name: "制作空间" }));
  expect(workspaces.getAllByRole("link").map(link => link.getAttribute("href"))).toEqual(["/merchants", "/references", "/", "/projects", "/replicate", "/image-projects"]);
  expect(screen.getByRole("link", { name: "文案二创" }).getAttribute("aria-current")).toBe("page");
  expect(screen.queryByRole("link", { name: "二创文档" })).toBeNull();
  expect(screen.queryByRole("link", { name: "AI短片" })).toBeNull();
});

test("global controls remain available in every workspace", () => {
  const settings = vi.fn();
  const theme = vi.fn();
  const logout = vi.fn();
  render(<ConsoleNavigation active="image" theme="dark" onNavigate={vi.fn()} onThemeChange={theme} onOpenSettings={settings} onLogout={logout} />);
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  fireEvent.change(screen.getByRole("combobox", { name: "选择界面主题" }), { target: { value: "light" } });
  fireEvent.click(screen.getByRole("button", { name: "退出" }));
  expect(settings).toHaveBeenCalledOnce();
  expect(theme).toHaveBeenCalledWith("light");
  expect(logout).toHaveBeenCalledOnce();
});

test("配置体检 shows a dot only while required setup items are pending", () => {
  const navigate = vi.fn();
  const { rerender } = render(<ConsoleNavigation active={null} theme="light" onNavigate={navigate} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} setupPending={2} />);
  const link = screen.getByRole("link", { name: "配置体检，2 项未就绪" });
  fireEvent.click(link);
  expect(navigate).toHaveBeenCalledWith("/setup");
  rerender(<ConsoleNavigation active="setup" theme="light" onNavigate={navigate} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} setupPending={0} />);
  expect(screen.getByRole("link", { name: "配置体检" }).getAttribute("aria-current")).toBe("page");
});

test("新手引导 is hidden from the navigation for now", () => {
  render(<ConsoleNavigation active={null} theme="light" onNavigate={vi.fn()} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} guideUnseen />);
  expect(screen.queryByRole("link", { name: /新手引导/ })).toBeNull();
});
