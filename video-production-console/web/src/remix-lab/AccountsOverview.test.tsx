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
  const props = { api, accounts, onClose: vi.fn(), onOpenAccount: vi.fn(), onAccountRenamed: vi.fn(), onAccountDeactivated: vi.fn(), onMessage: vi.fn() };
  return { props, page: render(<AccountsOverview {...props} />) };
}

test("renaming sends a trimmed name and updates the parent account list", async () => {
  const { props } = setup();
  props.api.mockImplementation(async (_path, init) => init?.method === "PATCH" ? json({ id: "a", name: "新账号" }) : json([]));
  fireEvent.click(screen.getByRole("button", { name: "改名账号 账号甲" }));
  fireEvent.change(screen.getByRole("textbox", { name: "新账号名称" }), { target: { value: " 新账号 " } });
  fireEvent.click(screen.getByRole("button", { name: "保存名称" }));
  await waitFor(() => expect(props.onAccountRenamed).toHaveBeenCalledWith({ id: "a", name: "新账号" }));
  expect(props.api).toHaveBeenCalledWith("/api/accounts/a", expect.objectContaining({ method: "PATCH", body: JSON.stringify({ name: "新账号" }) }));
});

test("rename failure keeps the editor and does not change the parent account", async () => {
  const { props } = setup();
  props.api.mockImplementation(async (_path, init) => init?.method === "PATCH" ? new Response("{}", { status: 409 }) : json([]));
  fireEvent.click(screen.getByRole("button", { name: "改名账号 账号甲" }));
  fireEvent.change(screen.getByRole("textbox", { name: "新账号名称" }), { target: { value: "账号乙" } });
  fireEvent.click(screen.getByRole("button", { name: "保存名称" }));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("同名账号"));
  expect(props.onAccountRenamed).not.toHaveBeenCalled();
  expect(screen.getByRole("textbox", { name: "新账号名称" })).toBeTruthy();
});

test("delete requires confirmation and removes the account only after success", async () => {
  const { props } = setup();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(screen.getByRole("button", { name: "删除账号 账号甲" }));
  expect(props.api.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(0);
  confirm.mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: "删除账号 账号甲" }));
  await waitFor(() => expect(props.onAccountDeactivated).toHaveBeenCalledWith("a"));
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining("历史项目和文案保留"));
});

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

test("batch reviewer selection is sent independently of writer models", async () => {
  const { props } = setup();
  fireEvent.click(screen.getByRole("checkbox", { name: "选择账号 账号甲" }));
  fireEvent.change(screen.getByRole("textbox", { name: "批量二创原文" }), { target: { value: "本次原文" } });
  fireEvent.change(screen.getByLabelText("审稿方式"), { target: { value: "custom" } });
  fireEvent.change(screen.getByRole("combobox", { name: "批量审稿模型" }), { target: { value: "review-model" } });
  fireEvent.change(screen.getByLabelText("审稿思考强度"), { target: { value: "high" } });
  fireEvent.click(screen.getByRole("button", { name: "开跑二创（已选 1 个账号）" }));
  await waitFor(() => expect(props.api.mock.calls.some(([p]) => p === "/api/remix-lab/workflow/run")).toBe(true));
  const call = props.api.mock.calls.find(([p]) => p === "/api/remix-lab/workflow/run")!;
  expect(JSON.parse(String(call[1]?.body))).toMatchObject({ models: [], reviewer: { model: "review-model", reasoning_effort: "high" } });
});

test.each(["invalid JSON", "null", '{"a":true}', "42", '[1,null,{}]'])("invalid hidden account storage %s does not prevent opening", (stored) => {
  window.localStorage.setItem(storageKey, stored);
  setup();
  expect(screen.getByRole("dialog", { name: "账号总览" })).toBeTruthy();
  expect(screen.getByRole("checkbox", { name: "选择账号 账号甲" })).toBeTruthy();
  expect(screen.getByRole("checkbox", { name: "选择账号 账号乙" })).toBeTruthy();
});
