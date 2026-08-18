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
  remix_base_url: "",
  remix_model: "",
  remix_reasoning_effort: "",
  codex_task_project_root: "",
  image_base_url: "http://images.example.test/v1",
  image_model: "gpt-image-2",
  image_text_base_url: "http://text.example.test/v1",
  image_text_model: "planner-test",
  image_text_reasoning_effort: "high",
  max_image_concurrency: 3,
  image_generation_attempts: 2,
  image_stream: false,
  default_image_ratio: "3:4",
  default_image_style: "finance_documentary",
  codex_binary_path: "",
  media_index_path: "",
  media_root: "",
  jianying_root: "",
  machine_profile_path: "",
  app_server_enabled: false,
  codex_workspace_roots: [],
  codex_default_model: "gpt-default",
  codex_default_reasoning_effort: "high",
  volc_speech_speaker_id: "S_volc_speaker",
  volc_speech_resource_id: "seed-icl-2.0",
  tts_provider: "aurastd",
  aurastd_base_url: "https://tts.aurastd.com",
  aurastd_model: "speech-2.8-hd",
  aurastd_voice_id: "moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67",
  aurastd_speed: 1.21,
  aurastd_volume: 1.4,
  aurastd_pitch: 1,
  aurastd_emotion: "",
  aurastd_language_boost: "Chinese",
  aurastd_modify_pitch: 0,
  aurastd_modify_intensity: 5,
  aurastd_modify_timbre: 6,
  aurastd_sound_effects: "",
  media_catalog_path: "C:\\media\\catalog.db",
  ffmpeg_path: "",
  ffprobe_path: "",
  vision_base_url: "https://vision.example.test/v1",
  vision_model: "vision-test",
  embedding_base_url: "",
  embedding_model: "",
  pexels_api_base_url: "https://api.pexels.com",
  pixabay_api_base_url: "https://pixabay.com",
  max_external_results_per_query: 20,
};

const emptySecretDraft = {
  grok_api_key: "",
  remix_api_key: "",
  pexels_api_key: "",
  volc_speech_api_key: "",
  aurastd_tts_api_key: "",
  image_api_key: "",
  image_text_api_key: "",
  vision_api_key: "",
  embedding_api_key: "",
  pixabay_api_key: "",
};

const settings: Settings = {
  public: draft,
  settings_version: 1,
  secrets: {
    grok_api_key: { configured: false, masked: "" },
    remix_api_key: { configured: false, masked: "" },
    pexels_api_key: { configured: false, masked: "" },
    volc_speech_api_key: { configured: true, masked: "********" },
    aurastd_tts_api_key: { configured: false, masked: "" },
    image_api_key: { configured: false, masked: "" },
    image_text_api_key: { configured: false, masked: "" },
    vision_api_key: { configured: false, masked: "" },
    embedding_api_key: { configured: false, masked: "" },
    pixabay_api_key: { configured: false, masked: "" },
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
      secretDraft={{ ...emptySecretDraft }}
      onSecretDraftChange={onSecretDraftChange}
      feedback=""
      onClose={() => {}}
      onSubmit={(event) => event.preventDefault()}
      {...overrides}
    />,
  );
  return { onDraftChange, onSecretDraftChange };
}

function openTab(name: string) {
  fireEvent.click(screen.getByRole("tab", { name }));
}

test("image tab only exposes service addresses and keys", () => {
  renderPanel();
  openTab("图文");
  expect(screen.getByRole("textbox", { name: "生图服务地址" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "图文文本模型地址" })).toBeTruthy();
  expect(screen.getByLabelText("生图 API Key")).toBeTruthy();
  expect(screen.getByLabelText("图文文本模型 API Key")).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "生图模型" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "图文文本模型" })).toBeNull();
  expect(screen.queryByRole("combobox", { name: /同时生成图片数/ })).toBeNull();
  expect(screen.queryByRole("combobox", { name: /图文文本思考强度/ })).toBeNull();
  expect(screen.queryByRole("checkbox", { name: /生图流式保活/ })).toBeNull();
  expect(screen.getByText(/模型和思考强度在进入图文项目后选择或填写/)).toBeTruthy();
});

test("remix model fields are editable independently", () => {
  const { onDraftChange, onSecretDraftChange } = renderPanel();
  fireEvent.change(screen.getByRole("textbox", { name: "二创服务地址" }), {
    target: { value: "http://127.0.0.1:2001/v1" },
  });
  expect(onDraftChange).toHaveBeenCalledWith({
    ...draft,
    remix_base_url: "http://127.0.0.1:2001/v1",
  });
  const remixModel = screen.getByRole("combobox", { name: "二创模型" }) as HTMLSelectElement;
  expect([...remixModel.options].map((option) => option.value)).toEqual([
    "",
    "gpt-5.6-sol",
    "grok-4.6",
    "gpt-5.6-terra",
  ]);
  fireEvent.change(remixModel, {
    target: { value: "gpt-5.6-sol" },
  });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, remix_model: "gpt-5.6-sol" });
  fireEvent.change(screen.getByRole("combobox", { name: "二创思考强度" }), {
    target: { value: "high" },
  });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, remix_reasoning_effort: "high" });
  expect((screen.getByRole("combobox", { name: "二创思考强度" }) as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByLabelText("二创 API 密钥"), {
    target: { value: "new-remix-key" },
  });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    remix_api_key: "new-remix-key",
  });
});

test("the Aura Studio voice settings are the default 配音 fields", () => {
  const { onDraftChange, onSecretDraftChange } = renderPanel();
  openTab("配音");

  const voice = screen.getByRole("textbox", { name: "克隆音色 ID" }) as HTMLInputElement;
  expect(voice.value).toBe("moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67");
  fireEvent.change(voice, { target: { value: "moss_audio_other" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, aurastd_voice_id: "moss_audio_other" });

  const speed = screen.getByLabelText("语速") as HTMLInputElement;
  expect(speed.value).toBe("1.21");
  fireEvent.change(speed, { target: { value: "1.3" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, aurastd_speed: 1.3 });

  fireEvent.change(screen.getByLabelText("Aura Studio API Key"), { target: { value: "new-aura-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    aurastd_tts_api_key: "new-aura-key",
  });
});

test("switching the 配音 provider reveals the Volcengine fallback fields", () => {
  const { onDraftChange } = renderPanel();
  openTab("配音");
  fireEvent.change(screen.getByRole("combobox", { name: "配音接口" }), { target: { value: "volc" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, tts_provider: "volc" });
});

test("the Volcengine voice IDs are editable after switching provider", () => {
  const { onDraftChange } = renderPanel({ draft: { ...draft, tts_provider: "volc" } });
  openTab("配音");

  const speaker = screen.getByRole("textbox", { name: "火山音色 ID" });
  expect((speaker as HTMLInputElement).value).toBe("S_volc_speaker");
  fireEvent.change(speaker, { target: { value: "S_other_speaker" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, tts_provider: "volc", volc_speech_speaker_id: "S_other_speaker" });
});

test("standalone conversation and local history controls are not shown", () => {
  renderPanel();
  openTab("系统");

  expect(screen.queryByText("启用实时 Codex 对话服务")).toBeNull();
  expect(screen.queryByText("本机历史显示数量")).toBeNull();
  expect(screen.getByText("启用任务实时交互服务")).toBeTruthy();
});

test("the Volcengine API key is masked and only sent when a new value is typed", () => {
  const { onSecretDraftChange } = renderPanel({ draft: { ...draft, tts_provider: "volc" } });
  openTab("配音");

  const key = screen.getByLabelText(/火山语音 API Key/) as HTMLInputElement;
  expect(key.type).toBe("password");
  expect(key.value).toBe("");
  expect(screen.getAllByText("已配置，输入新值才会替换")).toHaveLength(1);

  fireEvent.change(key, { target: { value: "new-volc-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    volc_speech_api_key: "new-volc-key",
  });
});

test("image generation settings and secret are editable", () => {
  const { onDraftChange, onSecretDraftChange } = renderPanel();
  openTab("图文");
  const baseURL = screen.getByRole("textbox", { name: "生图服务地址" });
  fireEvent.change(baseURL, { target: { value: "https://images.example.test/v1" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, image_base_url: "https://images.example.test/v1" });
  fireEvent.change(screen.getByLabelText(/生图 API Key/), { target: { value: "new-image-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    image_api_key: "new-image-key",
  });
  fireEvent.change(screen.getByLabelText(/图文文本模型 API Key/), { target: { value: "new-text-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    image_text_api_key: "new-text-key",
  });
  expect(screen.getByText(/当前地址使用 HTTP，密钥会明文传输/)).toBeTruthy();
});

test("the montage tab exposes catalog and FFmpeg paths without analysis keys", () => {
  const { onDraftChange } = renderPanel();
  openTab("混剪");

  const catalogPath = screen.getByRole("textbox", { name: "素材库目录" }) as HTMLInputElement;
  expect(catalogPath.value).toBe("C:\\media\\catalog.db");
  fireEvent.change(catalogPath, { target: { value: "D:\\library\\catalog.db" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, media_catalog_path: "D:\\library\\catalog.db" });

  fireEvent.change(screen.getByRole("textbox", { name: "FFmpeg 路径" }), { target: { value: "C:\\tools\\ffmpeg.exe" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, ffmpeg_path: "C:\\tools\\ffmpeg.exe" });
  fireEvent.change(screen.getByRole("textbox", { name: "FFprobe 路径" }), { target: { value: "C:\\tools\\ffprobe.exe" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, ffprobe_path: "C:\\tools\\ffprobe.exe" });
  expect(screen.queryByRole("textbox", { name: "视觉分析服务地址" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "向量服务地址" })).toBeNull();
});

test("unused retrieval and catalog-builder keys stay off the settings form", () => {
  renderPanel();
  for (const name of ["二创", "图文", "配音", "混剪", "系统"]) {
    openTab(name);
    expect(screen.queryByLabelText("Grok 检索 API 密钥")).toBeNull();
    expect(screen.queryByLabelText("Pexels API 密钥")).toBeNull();
    expect(screen.queryByLabelText("Pixabay API 密钥")).toBeNull();
    expect(screen.queryByLabelText("视觉分析 API Key")).toBeNull();
    expect(screen.queryByLabelText("向量模型 API Key")).toBeNull();
  }
});

test("an old settings response without image_base_url remains editable without an HTTP warning", () => {
  const { image_base_url: _imageBaseURL, image_text_base_url: _textURL, ...legacyDraft } = draft;

  renderPanel({ draft: legacyDraft as PublicSettings });
  openTab("图文");

  expect((screen.getByRole("textbox", { name: "生图服务地址" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByText(/当前地址使用 HTTP，密钥会明文传输/)).toBeNull();
});
