// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AigcStudio } from "./AigcStudio";
import type { Material } from "./MerchantsPage";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const image = (id: string, extra: Partial<Material> = {}): Material => ({
  id, merchant_id: "m1", kind: "image", filename: `${id}.jpg`, size: 10, description: "", license_source: "merchant_shot",
  license_scope: "", license_expires_at: "", parent_material_id: "", created_at: "2026-10-12T00:00:00Z", ...extra,
});

function setup(jobs: unknown[] = []) {
  const calls: Array<{ path: string; body?: Record<string, unknown> }> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (path.startsWith("/api/aigc/jobs?")) return new Response(JSON.stringify(jobs));
    return new Response(JSON.stringify({ id: "job-1" }), { status: 202 });
  });
  render(<AigcStudio api={api} merchantID="m1" materials={[image("f1"), image("r1", { license_source: "ai_generated", parent_material_id: "f1" })]} orders={[]} onMaterialsChanged={vi.fn()} onNotice={vi.fn()} />);
  return calls;
}

test("preparing image-to-video only drafts shots; nothing is submitted", async () => {
  const calls = setup();
  fireEvent.click(await screen.findByLabelText("选择 r1.jpg"));
  fireEvent.change(screen.getByLabelText("镜头动作"), { target: { value: "镜头缓慢推近" } });
  fireEvent.click(screen.getByRole("button", { name: "准备 1 个镜头" }));
  await waitFor(() => expect(calls.some((call) => call.path === "/api/aigc/image-to-video")).toBe(true));
  const prepared = calls.find((call) => call.path === "/api/aigc/image-to-video")!;
  expect(prepared.body).toMatchObject({ merchant_id: "m1", material_ids: ["r1"], duration_s: 5, aspect: "9:16" });
  expect(calls.some((call) => call.path.endsWith("/confirm"))).toBe(false);
});

test("paid submission requires an explicit confirm and is skipped when declined", async () => {
  const job = { id: "j1", merchant_id: "m1", order_id: "", kind: "image_to_video", status: "awaiting_confirm", input_material_ids: ["r1"], output_material_ids: [], params_json: '{"estimated_shots":1}', error: "", created_at: "2026-10-12T00:00:00Z", updated_at: "2026-10-12T00:00:00Z" };
  const calls = setup([job]);
  const button = await screen.findByRole("button", { name: "确认提交（会产生费用）" });
  const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
  fireEvent.click(button);
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining("会产生费用"));
  expect(calls.some((call) => call.path === "/api/aigc/jobs/j1/confirm")).toBe(false);
  fireEvent.click(button);
  await waitFor(() => expect(calls.some((call) => call.path === "/api/aigc/jobs/j1/confirm")).toBe(true));
});

test("AI-generated images are labelled and restyle carries the preserve note", async () => {
  const calls = setup();
  expect(await screen.findByText("AI 生成")).toBeTruthy();
  fireEvent.click(screen.getByLabelText("选择 f1.jpg"));
  fireEvent.change(screen.getByLabelText("想要的环境与氛围"), { target: { value: "清晨渔港" } });
  fireEvent.change(screen.getByLabelText(/必须保持不变/), { target: { value: "红色绑绳" } });
  fireEvent.click(screen.getByRole("button", { name: "重绘 1 张" }));
  await waitFor(() => expect(calls.find((call) => call.path === "/api/aigc/restyle")?.body).toMatchObject({ material_ids: ["f1"], prompt: "清晨渔港", preserve: "红色绑绳" }));
});
