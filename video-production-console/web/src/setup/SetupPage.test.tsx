// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SetupPage, type SetupStatus } from "./SetupPage";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const status: SetupStatus = {
  ready: false,
  items: [
    { id: "writer_model", title: "二创写作模型", required: true, status: "ok", detail: "claude-opus 可用", fix: "none" },
    { id: "jianying", title: "剪映", required: true, status: "missing", detail: "还没设置剪映草稿目录。", fix: "jianying_detect" },
    { id: "platform_api", title: "平台接口", required: true, status: "error", detail: "Token 无效", fix: "activation_code" },
    { id: "review_model", title: "审稿模型", required: false, status: "warn", detail: "未单独设置，审稿会用写作模型", fix: "settings:remix" },
  ],
};

function setup(overrides: Record<string, () => Response> = {}) {
  const calls: Array<{ path: string; method?: string; body?: unknown }> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (overrides[path]) return overrides[path]();
    if (path === "/api/setup/status") return new Response(JSON.stringify(status));
    if (path === "/api/jianying-drafts/detect") return new Response(JSON.stringify({
      running: false, configured_root: "", configured_matches: false,
      candidates: [{ draft_root: "D:\\JianyingPro\\AppData\\User Data\\Projects\\com.lveditor.draft", install_dir: "D:\\JianyingPro", version: "11.1.0.14287", verified: true, custom_draft_path: "D:\\JianyingPro Drafts", draft_count: 94, has_root_meta: true, source: "registry" }],
    }));
    if (path === "/api/jianying-drafts/detect/apply") return new Response(JSON.stringify({ jianying_root: "x", restart_required: true, machine_profile_updated: true }));
    if (path === "/api/setup/activate") return new Response(JSON.stringify({ applied_public: ["remix_base_url", "remix_model"], applied_secrets: ["remix_api_key"], skipped: [], issued_to: "王老板", expires_at: "", restart_required: false }));
    if (path === "/api/setup/export-code") return new Response(JSON.stringify({ code: "JYC1.abc", public_keys: ["remix_model"], secret_keys: ["remix_api_key"] }));
    return new Response("{}", { status: 404 });
  });
  const onOpenSettings = vi.fn();
  const onStatusChange = vi.fn();
  render(<SetupPage api={api} onOpenSettings={onOpenSettings} onStatusChange={onStatusChange} />);
  return { api, calls, onOpenSettings, onStatusChange };
}

test("the checklist counts required items and reports them upward", async () => {
  const { onStatusChange } = setup();
  expect(await screen.findByText("还差 2 项必需配置")).toBeTruthy();
  expect(screen.getByText("必需 1/3 项就绪 · 可选 0/1 项")).toBeTruthy();
  expect(onStatusChange).toHaveBeenCalledWith(status);
});

test("剪映 is found automatically and applied with one click", async () => {
  const { calls } = setup();
  await screen.findByText("还差 2 项必需配置");
  fireEvent.click(screen.getByRole("button", { name: "自动检测剪映" }));
  expect(await screen.findByText(/94 个草稿/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "用这个" }));
  await waitFor(() => expect(calls.find((call) => call.path === "/api/jianying-drafts/detect/apply")?.body).toEqual({ draft_root: "D:\\JianyingPro\\AppData\\User Data\\Projects\\com.lveditor.draft" }));
  expect(await screen.findByRole("button", { name: "立即重启" })).toBeTruthy();
});

test("an activation code is applied without echoing any value", async () => {
  const { calls } = setup();
  const box = await screen.findByLabelText("配置码");
  const submit = screen.getByRole("button", { name: "应用配置码" });
  fireEvent.change(box, { target: { value: "not-a-code" } });
  expect(submit.hasAttribute("disabled")).toBe(true);
  fireEvent.change(box, { target: { value: "JYC1.payload" } });
  fireEvent.click(submit);
  expect(await screen.findByText(/写入 2 项设置、1 个密钥/)).toBeTruthy();
  expect(calls.find((call) => call.path === "/api/setup/activate")?.body).toEqual({ code: "JYC1.payload" });
  expect((box as HTMLTextAreaElement).value).toBe("");
});

test("settings-type fixes open the settings dialog", async () => {
  const { onOpenSettings } = setup();
  const row = (await screen.findByText("审稿模型")).closest("li")!;
  fireEvent.click(within(row).getByRole("button", { name: "打开设置" }));
  expect(onOpenSettings).toHaveBeenCalled();
});

test("the vendor tool exports a code for a named customer", async () => {
  const { calls } = setup();
  fireEvent.click(await screen.findByText(/服务商工具/));
  fireEvent.change(screen.getByPlaceholderText(/王老板/), { target: { value: "岚山海鲜" } });
  fireEvent.change(screen.getByDisplayValue("长期有效"), { target: { value: "30" } });
  fireEvent.click(screen.getByRole("button", { name: "生成配置码" }));
  expect(await screen.findByDisplayValue("JYC1.abc")).toBeTruthy();
  expect(calls.find((call) => call.path === "/api/setup/export-code")?.body).toEqual({ issued_to: "岚山海鲜", expires_days: 30, include_secrets: true });
});
