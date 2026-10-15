// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ImageTextConnectionSettings } from "./ImageTextConnectionSettings";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {
  status,
  headers: { "Content-Type": "application/json" },
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("saves only the image text connection overlay and preserves an existing key when left blank", async () => {
  const settings = {
    public: {
      image_text_base_url: "https://old.example/v1",
      image_text_model: "gpt-5.6-sol",
      image_text_reasoning_effort: "medium",
    },
    settings_version: 3,
    secrets: { image_text_api_key: { configured: true, masked: "********" } },
  } as any;
  const saved = {
    ...settings,
    public: { ...settings.public, image_text_base_url: "https://new.example/v1", image_text_model: "grok-4.6", image_text_reasoning_effort: "high" },
    settings_version: 4,
    restart_required: true,
  };
  const api = vi.fn(async (_path: string, init?: RequestInit) => {
    expect(JSON.parse(String(init?.body))).toEqual({
      public: {
        image_text_base_url: "https://new.example/v1",
        image_text_model: "grok-4.6",
        image_text_reasoning_effort: "high",
      },
      secrets: {},
    });
    return json(saved);
  });
  const onSaved = vi.fn();

  render(<ImageTextConnectionSettings api={api} settings={settings} onSaved={onSaved} />);
  fireEvent.click(screen.getByText("文本模型连接"));
  fireEvent.change(screen.getByLabelText("图文文本模型地址"), { target: { value: "https://new.example/v1" } });
  fireEvent.change(screen.getByLabelText("图文默认文本模型"), { target: { value: "grok-4.6" } });
  fireEvent.change(screen.getByLabelText("图文文本思考强度"), { target: { value: "high" } });
  fireEvent.click(screen.getByRole("button", { name: "保存文本模型连接" }));

  await waitFor(() => expect(api).toHaveBeenCalledWith("/api/settings", expect.objectContaining({ method: "PUT" })));
  expect(onSaved).toHaveBeenCalledWith(saved);
  expect(await screen.findByText("文本模型连接已保存。重启后生效。")).toBeTruthy();
});

test("sends a replacement API key only when one is entered", async () => {
  const settings = {
    public: { image_text_base_url: "https://text.example/v1", image_text_model: "gpt-5.6-sol", image_text_reasoning_effort: "" },
    settings_version: 1,
    secrets: { image_text_api_key: { configured: false, masked: "" } },
  } as any;
  const api = vi.fn(async (_path: string, init?: RequestInit) => {
    expect(JSON.parse(String(init?.body)).secrets).toEqual({ image_text_api_key: "new-secret" });
    return json(settings);
  });

  render(<ImageTextConnectionSettings api={api} settings={settings} onSaved={() => {}} />);
  fireEvent.click(screen.getByText("文本模型连接"));
  fireEvent.change(screen.getByLabelText("图文文本模型 API Key"), { target: { value: "new-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "保存文本模型连接" }));

  await waitFor(() => expect(api).toHaveBeenCalledTimes(1));
});

test("saved connection that needs restart can be activated from the same panel", async () => {
  const settings = {public:{image_text_model:"gpt-5.6-sol"},secrets:{},restart_required:true} as any;
  const api = vi.fn(async () => json({}, 503));
  render(<ImageTextConnectionSettings api={api} settings={settings} onSaved={() => {}} />);
  fireEvent.click(screen.getByText("文本模型连接"));
  fireEvent.click(screen.getByRole("button",{name:"重启使设置生效"}));
  await waitFor(() => expect(api).toHaveBeenCalledWith("/api/system/restart", {method:"POST"}));
  expect(await screen.findByText("重启请求失败，已保存的设置仍保留。请稍后重试。")).toBeTruthy();
});
