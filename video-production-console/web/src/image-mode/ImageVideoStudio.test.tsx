// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ImageVideoStudio } from "./ImageVideoStudio";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

test("submits a standalone image-video job from the full script", async () => {
  const onProjectOpen = vi.fn();
  let request: RequestInit | undefined;
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-videos" && !init?.method) return json([]);
    if (path === "/api/accounts") return json([{ id: "acc-1", name: "账号一" }]);
    if (path === "/api/image-videos" && init?.method === "POST") {
      request = init;
      return json({ project_id: "video-1", run_status: "running" }, 202);
    }
    throw new Error(path);
  });
  render(<ImageVideoStudio api={api} onProjectOpen={onProjectOpen} />);
  await screen.findByRole("heading", { name: "口播拆段生图", level: 1 });
  expect(await screen.findByRole("option", { name: "账号一" })).toBeTruthy();
  fireEvent.change(screen.getByLabelText("口播文案"), { target: { value: "完整口播。" } });
  fireEvent.change(screen.getByLabelText("发布账号"), { target: { value: "acc-1" } });
  fireEvent.click(screen.getByRole("button", { name: "开始生成图文视频" }));
  await waitFor(() => expect(onProjectOpen).toHaveBeenCalledWith("video-1"));
  expect(JSON.parse(String(request?.body))).toMatchObject({
    script: "完整口播。",
    account_id: "acc-1",
    output_mode: "image_slideshow",
  });
});

test("lists existing image videos and does not call zip projects", async () => {
  const api = vi.fn(async (path: string) => {
    if (path === "/api/image-videos") return json([{ id: "video-1", title: "家庭现金流", image_count: 12, status: "ready", created_at: "", updated_at: "" }]);
    if (path === "/api/accounts") return json([]);
    throw new Error(path);
  });
  render(<ImageVideoStudio api={api} />);
  expect(await screen.findByRole("button", { name: /家庭现金流/ })).toBeTruthy();
  expect(api.mock.calls.some(([path]) => path === "/api/image-projects")).toBe(false);
});
