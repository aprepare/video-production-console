import type { Asset } from "../types";

export const assetLabels: Record<string, string> = {
  source_script: "爆款原文",
  topic_card: "正式选题卡",
  continuous_script: "连续文案",
  narration: "配音",
  spoken_script: "配音断句",
  subtitle_srt: "SRT 字幕",
  account_background: "账号固定背景图",
  audio: "配音",
  subtitle: "SRT 字幕",
  mix_draft: "混剪草稿",
  final_video: "成片",
};

export type AssetPreview = { asset: Asset; text?: string };
