// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConsoleNavigation } from "./ConsoleNavigation";

afterEach(cleanup);

test("navigation marks the active workspace and preserves modified link clicks", () => {
  const navigate = vi.fn();
  render(<ConsoleNavigation active="image" theme="light" onNavigate={navigate} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} />);
  const image = screen.getByRole("link", { name: "图文制作" });
  expect(image.getAttribute("aria-current")).toBe("page");
  const remix = screen.getByRole("link", { name: "文案与混剪" });
  expect(remix.getAttribute("href")).toBe("/");
  fireEvent.click(remix);
  expect(navigate).toHaveBeenCalledExactlyOnceWith("/");
  fireEvent.click(remix, { ctrlKey: true });
  expect(navigate).toHaveBeenCalledTimes(1);
});

test("only copy and image workspaces remain in navigation", () => {
  render(<ConsoleNavigation active="remix" theme="light" onNavigate={vi.fn()} onThemeChange={vi.fn()} onOpenSettings={vi.fn()} onLogout={vi.fn()} />);
  expect(screen.getAllByRole("link").map(link => link.getAttribute("href"))).toEqual(["/", "/image-projects"]);
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
