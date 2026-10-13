// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { StylePicker } from "./StylePicker";
import type { RemixLabStyle } from "./api";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

// Style cards are named by their full text; the copy/edit tools carry the name in quotes.
const cardName = (name: string) => new RegExp(`^${name}`);
const card = (name: string) => screen.getByRole("button", { name: cardName(name) });
const findCard = (name: string) => screen.findByRole("button", { name: cardName(name) });

const style = (id: string, name: string, extra: Partial<RemixLabStyle> = {}): RemixLabStyle => ({
  id, name, category: "通用", description: `${name}说明`, best_for: [], planner: "", writer: "写", writer_user: "{{SOURCE}}", reviewer: "", rules: "", finance: false, builtin: true, ...extra,
});
const styles = [
  style("finance_course", "财经卖课", { category: "财经卖课", finance: true }),
  style("universal_remix", "通用爆款二创"),
  style("local_store", "本地门店探店", { category: "本地生活" }),
  style("product_seller", "带货种草", { category: "带货" }),
];

function setup(workflowStyle = "finance_course", brief: Record<string, unknown> = {}) {
  const calls: Array<{ path: string; method?: string; body?: unknown }> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (path === "/api/remix-lab/styles") return new Response(JSON.stringify({ styles }));
    if (path.startsWith("/api/remix-lab/workflow-prompts")) return new Response(JSON.stringify({ workflow: { version: 1, name: "wf", nodes: [], edges: [], style: workflowStyle, brief }, writer_contract: "", reviewer_contract: "" }));
    if (path.startsWith("/api/remix-lab/workflow/style")) return new Response(JSON.stringify({ workflow: { version: 2, name: "wf", nodes: [], edges: [], style: "local_store" }, writer_contract: "", reviewer_contract: "" }));
    return new Response("{}", { status: 404 });
  });
  const onApplied = vi.fn();
  const onMessage = vi.fn();
  render(<StylePicker api={api} accountID="acc-1" ownerLabel="岚山海鲜" onClose={vi.fn()} onApplied={onApplied} onMessage={onMessage} />);
  return { calls, onApplied, onMessage };
}

test("the current style is marked and its saved brief is loaded", async () => {
  setup("local_store", { industry: "海鲜餐饮", selling_points: "凌晨直采" });
  expect(await screen.findByDisplayValue("海鲜餐饮")).toBeTruthy();
  expect(card("本地门店探店").getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByText("使用中")).toBeTruthy();
});

test("an industry preset fills only blanks and picks its suggested style", async () => {
  setup("universal_remix", { audience: "附近居民" });
  await screen.findByDisplayValue("附近居民");
  fireEvent.click(screen.getByRole("button", { name: "海鲜餐饮" }));
  expect(screen.getByDisplayValue("附近居民")).toBeTruthy();
  expect(screen.getByDisplayValue("海鲜餐饮 / 渔港小馆")).toBeTruthy();
  expect(card("本地门店探店").getAttribute("aria-pressed")).toBe("true");
});

test("leaving the finance style asks first, then applies style and brief to the account", async () => {
  const { calls, onApplied } = setup("finance_course");
  fireEvent.click(await findCard("本地门店探店"));
  fireEvent.change(screen.getByPlaceholderText(/海鲜餐饮、日照绿茶/), { target: { value: "海鲜餐饮" } });
  const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
  const apply = screen.getByRole("button", { name: /应用到当前账号/ });
  fireEvent.click(apply);
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining("从财经卖课切换到「本地门店探店」"));
  expect(calls.some((call) => call.path.startsWith("/api/remix-lab/workflow/style"))).toBe(false);
  fireEvent.click(apply);
  await waitFor(() => expect(onApplied).toHaveBeenCalled());
  const sent = calls.find((call) => call.path.startsWith("/api/remix-lab/workflow/style"))!;
  expect(sent.path).toBe("/api/remix-lab/workflow/style?account_id=acc-1");
  expect(sent.body).toMatchObject({ style_id: "local_store", brief: { industry: "海鲜餐饮" } });
});
