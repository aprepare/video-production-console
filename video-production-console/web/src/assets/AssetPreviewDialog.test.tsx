// @vitest-environment jsdom
/// <reference types="node" />

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AssetPreviewDialog } from "./AssetPreviewDialog";
import type { Asset } from "../types";

afterEach(cleanup);

function asset(type: string): Asset {
  return {
    id: `${type}-id`,
    type,
    filename: `${type}.json`,
    mime_type: "application/json",
    size: 128,
    version: 1,
    state: "ready",
    created_at: "2026-08-23T00:00:00Z",
  };
}

test("renders caption keywords as highlighted spoken lines instead of raw JSON", () => {
  const text = JSON.stringify({
    schema_version: 1,
    lines: [
      { line: "整整2万亿", keywords: [{ text: "2万亿", kind: "number" }, { text: "整整", kind: "warning" }] },
      { line: "不是拿去买房了", keywords: [{ text: "买房", kind: "warning" }] },
      { line: "这是铺垫句", keywords: [] },
    ],
  });
  render(
    <AssetPreviewDialog
      preview={{ asset: asset("caption_keywords"), text }}
      draft={text}
      onDraftChange={vi.fn()}
      saving={false}
      onClose={vi.fn()}
      onSave={vi.fn()}
      onRevise={vi.fn()}
    />,
  );

  expect(screen.getByText("共 3 行，已标注 2 行。红底是警示词，金底是数字。")).toBeTruthy();
  expect(screen.getAllByText("2万亿").length).toBeGreaterThan(0);
  expect(screen.getAllByText("买房").length).toBeGreaterThan(0);
  expect(screen.queryByText(/schema_version/)).toBeNull();
});
