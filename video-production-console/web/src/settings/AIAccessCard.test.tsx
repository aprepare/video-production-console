// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AIAccessCard } from "./AIAccessCard";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const access = (token: string) => ({
  token, endpoint: "http://127.0.0.1:2030/mcp",
  claude: `claude mcp add --transport http jiying-console http://127.0.0.1:2030/mcp --header "Authorization: Bearer ${token}"`,
  codex: "[mcp_servers.jiying-console]", json: "{}",
});

test("the token stays masked until shown and the Claude command copies", async () => {
  const writeText = vi.fn(async () => {});
  Object.assign(navigator, { clipboard: { writeText } });
  const api = vi.fn(async () => new Response(JSON.stringify(access("jy_abcdef1234567890"))));
  render(<AIAccessCard api={api} />);
  await waitFor(() => expect(screen.getByText("http://127.0.0.1:2030/mcp")).toBeTruthy());
  expect(screen.queryByText("jy_abcdef1234567890")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "显示令牌" }));
  expect(screen.getByText("jy_abcdef1234567890")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /复制 Claude Code 命令/ }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(expect.stringContaining("claude mcp add")));
});

test("rotating asks first, then shows the new token", async () => {
  const api = vi.fn(async (path: string) => new Response(JSON.stringify(access(path.endsWith("/rotate") ? "jy_new_token_99999" : "jy_old_token_11111"))));
  render(<AIAccessCard api={api} />);
  await waitFor(() => expect(screen.getByRole("button", { name: /重置令牌/ })).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: /重置令牌/ }));
  expect(api).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "确认重置" }));
  await waitFor(() => expect(screen.getByText(/旧令牌立即失效/)).toBeTruthy());
  expect(api).toHaveBeenCalledWith("/api/mcp/token/rotate", expect.objectContaining({ method: "POST" }));
  fireEvent.click(screen.getByRole("button", { name: "显示令牌" }));
  expect(screen.getByText("jy_new_token_99999")).toBeTruthy();
});
