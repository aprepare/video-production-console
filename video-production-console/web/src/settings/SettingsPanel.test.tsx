// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SettingsPanel } from "./SettingsPanel";
import type { PublicSettings, Settings } from "../types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const draft: PublicSettings = {
  listen_addr: "127.0.0.1:8080",
  data_root: "C:\\data",
  max_codex_concurrency: 2,
  baokuan_base_url: "",
  baokuan_mcp_executable: "",
  obsidian_vault: "",
  topic_cards_dir: "",
  grok_base_url: "",
  grok_model: "",
  codex_binary_path: "",
  media_index_path: "",
  media_root: "",
  jianying_root: "",
  machine_profile_path: "",
  app_server_enabled: false,
  codex_workspace_roots: [],
  codex_history_limit: 10,
  codex_default_model: "gpt-default",
  codex_default_reasoning_effort: "high",
  volc_speech_speaker_id: "S_volc_speaker",
  volc_speech_resource_id: "seed-icl-2.0",
};

const settings: Settings = {
  public: draft,
  settings_version: 1,
  secrets: {
    grok_api_key: { configured: false, masked: "" },
    pexels_api_key: { configured: false, masked: "" },
    volc_speech_api_key: { configured: true, masked: "********" },
  },
};

function renderPanel(overrides: Partial<Parameters<typeof SettingsPanel>[0]> = {}) {
  const onDraftChange = vi.fn();
  const onSecretDraftChange = vi.fn();
  render(
    <SettingsPanel
      settings={settings}
      draft={draft}
      onDraftChange={onDraftChange}
      secretDraft={{ grok_api_key: "", pexels_api_key: "", volc_speech_api_key: "" }}
      onSecretDraftChange={onSecretDraftChange}
      feedback=""
      onClose={() => {}}
      onSubmit={(event) => event.preventDefault()}
      {...overrides}
    />,
  );
  return { onDraftChange, onSecretDraftChange };
}

test("the Volcengine voice IDs are editable public fields", () => {
  const { onDraftChange } = renderPanel();

  const speaker = screen.getByRole("textbox", { name: "火山音色 ID" });
  const resource = screen.getByRole("textbox", { name: "火山语音资源 ID" });
  expect((speaker as HTMLInputElement).value).toBe("S_volc_speaker");
  expect((resource as HTMLInputElement).value).toBe("seed-icl-2.0");

  fireEvent.change(speaker, { target: { value: "S_other_speaker" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, volc_speech_speaker_id: "S_other_speaker" });
});

test("the Volcengine API key is masked and only sent when a new value is typed", () => {
  const { onSecretDraftChange } = renderPanel();

  const key = screen.getByLabelText(/火山语音 API Key/) as HTMLInputElement;
  expect(key.type).toBe("password");
  expect(key.value).toBe("");
  expect(screen.getAllByText("已配置，输入新值才会替换")).toHaveLength(1);

  fireEvent.change(key, { target: { value: "new-volc-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    grok_api_key: "",
    pexels_api_key: "",
    volc_speech_api_key: "new-volc-key",
  });
});
