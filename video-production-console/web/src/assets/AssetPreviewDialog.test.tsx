// @vitest-environment jsdom
/// <reference types="node" />

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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

const sample = JSON.stringify({
  schema_version: 1,
  lines: [
    { line: "整整2万亿", keywords: [{ text: "2万亿", kind: "number" }, { text: "整整", kind: "warning" }] },
    { line: "不是拿去买房了", keywords: [{ text: "买房", kind: "warning" }] },
    { line: "这是铺垫句", keywords: [] },
  ],
});

test("renders caption keywords as highlighted spoken lines instead of raw JSON", () => {
  render(
    <AssetPreviewDialog
      preview={{ asset: asset("caption_keywords"), text: sample }}
      draft={sample}
      onDraftChange={vi.fn()}
      saving={false}
      onClose={vi.fn()}
      onSave={vi.fn()}
      onRevise={vi.fn()}
    />,
  );

  expect(screen.getByText(/共 3 行，已标注 2 行/)).toBeTruthy();
  expect(screen.getAllByText("2万亿").length).toBeGreaterThan(0);
  expect(screen.getAllByText("买房").length).toBeGreaterThan(0);
  expect(screen.queryByText(/schema_version/)).toBeNull();
  expect((screen.getByRole("button", { name: "保存修改" }) as HTMLButtonElement).disabled).toBe(true);
});

test("lets the operator add and remove caption keywords before saving", () => {
  const onDraftChange = vi.fn();
  const onSave = vi.fn();
  const { rerender } = render(
    <AssetPreviewDialog
      preview={{ asset: asset("caption_keywords"), text: sample }}
      draft={sample}
      onDraftChange={onDraftChange}
      saving={false}
      onClose={vi.fn()}
      onSave={onSave}
      onRevise={vi.fn()}
    />,
  );

  fireEvent.click(screen.getByRole("button", { name: "取消标注买房" }));
  expect(onDraftChange).toHaveBeenCalled();
  const afterRemove = onDraftChange.mock.calls.at(-1)?.[0] as string;
  expect(afterRemove).toContain("\"keywords\": []");
  expect(afterRemove).not.toMatch(/"text": "买房"/);

  rerender(
    <AssetPreviewDialog
      preview={{ asset: asset("caption_keywords"), text: sample }}
      draft={afterRemove}
      onDraftChange={onDraftChange}
      saving={false}
      onClose={vi.fn()}
      onSave={onSave}
      onRevise={vi.fn()}
    />,
  );
  fireEvent.change(screen.getByLabelText("第 3 行新增关键词"), { target: { value: "铺垫" } });
  fireEvent.click(screen.getAllByRole("button", { name: "添加" }).at(-1)!);
  const afterAdd = onDraftChange.mock.calls.at(-1)?.[0] as string;
  expect(afterAdd).toContain("\"text\": \"铺垫\"");
  fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
  expect(onSave).toHaveBeenCalled();
});
