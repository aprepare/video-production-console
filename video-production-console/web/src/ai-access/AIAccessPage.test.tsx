// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AIAccessPage } from "./AIAccessPage";

const access = { token: "secret-token-0123456789", endpoint: "http://127.0.0.1:2030/mcp", claude: "claude mcp add jiying", codex: "[mcp_servers.jiying]", json: "{}" };
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

test("the token stays masked until revealed and setup commands copy", async () => {
  const writeText = vi.fn(async () => undefined);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  const api = vi.fn(async () => json(access));
  render(<AIAccessPage api={api} />);
  expect(await screen.findByText("secret••••••••6789")).toBeTruthy();
  expect(screen.queryByText(access.token)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "显示令牌" }));
  expect(screen.getByText(access.token)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Codex 配置" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(access.codex));
  expect(await screen.findByRole("button", { name: "已复制" })).toBeTruthy();
});

test("rotating the token asks first and then shows the new setup", async () => {
  const rotated = { ...access, token: "rotated-token-9876543210" };
  const api = vi.fn(async (path: string) => json(path.endsWith("/rotate") ? rotated : access));
  render(<AIAccessPage api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: "重置令牌" }));
  expect(api).not.toHaveBeenCalledWith("/api/mcp/token/rotate", expect.anything());
  expect(screen.getByText("旧令牌会立即失效，确定吗？")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "确认重置" }));
  await waitFor(() => expect(api).toHaveBeenCalledWith("/api/mcp/token/rotate", { method: "POST" }));
  expect(await screen.findByText(/令牌已重置/)).toBeTruthy();
  expect(screen.getByText("rotate••••••••3210")).toBeTruthy();
});

test("a failed load explains the problem instead of spinning", async () => {
  const api = vi.fn(async () => json({ message: "MCP 服务没有启动" }, 503));
  render(<AIAccessPage api={api} />);
  expect((await screen.findByRole("alert")).textContent).toBe("MCP 服务没有启动");
  expect(screen.queryByText("正在读取接入信息…")).toBeNull();
});
