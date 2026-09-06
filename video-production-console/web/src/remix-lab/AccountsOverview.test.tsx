// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AccountsOverview } from "./AccountsOverview";

const storageKey = "remix-lab:overview-hidden-accounts";
const accounts = [{ id: "a", name: "账号甲" }, { id: "b", name: "账号乙" }];
const json = (value: unknown) => new Response(JSON.stringify(value));

afterEach(() => { cleanup(); window.localStorage.clear(); vi.restoreAllMocks(); });

function setup() {
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/workflow/run" && init?.method === "POST") {
      return json({ id: "experiment-new", runs: [] });
    }
    return json([]);
  });
  const props = { api, accounts, onClose: vi.fn(), onOpenAccount: vi.fn(), onAccountDeactivated: vi.fn(), onMessage: vi.fn() };
  return { props, page: render(<AccountsOverview {...props} />) };
}

test("hiding deselects an account, persists across mounting, and can be restored without deactivation", async () => {
  const { props, page } = setup();
  fireEvent.click(screen.getByRole("checkbox", { name: "选择账号 账号甲" }));
  fireEvent.click(screen.getByRole("button", { name: "隐藏账号 账号甲" }));
  expect(screen.queryByRole("checkbox", { name: "选择账号 账号甲" })).toBeNull();
  expect(screen.getByRole("button", { name: "开跑二创（已选 0 个账号）" })).toBeTruthy();
  await waitFor(() => expect(JSON.parse(window.localStorage.getItem(storageKey) ?? "null")).toEqual(["a"]));

  page.unmount();
  render(<AccountsOverview {...props} />);
  expect(screen.queryByRole("checkbox", { name: "选择账号 账号甲" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "管理隐藏账号（1）" }));
  fireEvent.click(screen.getByRole("button", { name: "显示账号 账号甲" }));
  expect((screen.getByRole("checkbox", { name: "选择账号 账号甲" }) as HTMLInputElement).checked).toBe(false);
  await waitFor(() => expect(JSON.parse(window.localStorage.getItem(storageKey) ?? "null")).toEqual([]));
  expect(props.onAccountDeactivated).not.toHaveBeenCalled();
  expect(props.api.mock.calls.filter(([, init]) => init?.method === "DELETE")).toEqual([]);
});

test("select all and batch launch only include visible accounts after hiding a selected account", async () => {
  const { props } = setup();
  fireEvent.click(screen.getByRole("checkbox", { name: "全选账号" }));
  fireEvent.click(screen.getByRole("button", { name: "隐藏账号 账号甲" }));
  expect((screen.getByRole("checkbox", { name: "全选账号" }) as HTMLInputElement).checked).toBe(true);
  fireEvent.click(screen.getByRole("checkbox", { name: "全选账号" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "全选账号" }));
  fireEvent.change(screen.getByRole("textbox", { name: "批量二创原文" }), { target: { value: "测试原文" } });
  fireEvent.click(screen.getByRole("button", { name: "开跑二创（已选 1 个账号）" }));
  await waitFor(() => expect(props.onMessage).toHaveBeenCalledWith("已为 1 个账号开跑二创，进度看下方表格。"));
  const launches = props.api.mock.calls.filter(([path]) => path === "/api/remix-lab/workflow/run");
  expect(launches).toHaveLength(1);
  expect(JSON.parse(String(launches[0][1]?.body))).toMatchObject({ account_id: "b", source: "测试原文" });
});

test.each(["invalid JSON", "null", '{"a":true}', "42", '[1,null,{}]'])("invalid hidden account storage %s does not prevent opening", (stored) => {
  window.localStorage.setItem(storageKey, stored);
  setup();
  expect(screen.getByRole("dialog", { name: "账号总览" })).toBeTruthy();
  expect(screen.getByRole("checkbox", { name: "选择账号 账号甲" })).toBeTruthy();
  expect(screen.getByRole("checkbox", { name: "选择账号 账号乙" })).toBeTruthy();
});
