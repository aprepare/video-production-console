// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { PartnerSettingsPanel } from "./PartnerSettingsPanel";
import type { PartnerSettingsView } from "../partner/types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function partnerSettingsFixture(): PartnerSettingsView {
  return {
    text_models: ["gpt-5.6-sol"],
    reasoning_efforts: ["medium", "high"],
    image_model: "gpt-image-2",
    aura_model: "speech-2.8-hd",
    aura_voice_id: "moss_audio_partner",
    media_root: "D:\\Media",
    jianying_root: "D:\\Jianying",
    machine_profile_path: "D:\\profiles\\machine.json",
    default_image_ratio: "3:4",
    default_image_style: "finance_documentary",
    aura_speed: 1.1,
    aura_volume: 1.2,
  };
}

test("never renders provider URLs or API key fields", async () => {
  render(<PartnerSettingsPanel settings={partnerSettingsFixture()} onSave={vi.fn()} />);

  expect(screen.getByText("gpt-5.6-sol")).toBeTruthy();
  expect(screen.getByText("gpt-image-2")).toBeTruthy();
  expect(screen.queryByText(/Base URL|API Key|接口地址/i)).toBeNull();
  expect(screen.queryByRole("textbox", { name: /模型/ })).toBeNull();
});

test("exposes only path, image presentation, speed, and volume as editable fields", () => {
  render(<PartnerSettingsPanel settings={partnerSettingsFixture()} onSave={vi.fn()} />);

  expect(screen.getByRole("textbox", { name: "媒体素材目录" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "剪映草稿目录" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "混剪机器配置" })).toBeTruthy();
  expect(screen.getByLabelText("默认图片比例")).toBeTruthy();
  expect(screen.getByLabelText("默认图片风格")).toBeTruthy();
  expect(screen.getByLabelText("语速")).toBeTruthy();
  expect(screen.getByLabelText("音量")).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "克隆音色 ID" })).toBeNull();
  expect(screen.queryByLabelText("配音模型")).toBeNull();
});
