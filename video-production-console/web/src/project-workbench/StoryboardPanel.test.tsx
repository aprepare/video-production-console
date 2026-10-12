// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { StoryboardPanel, repeatedSources } from "./StoryboardPanel";

afterEach(cleanup);

const shots = [
  { shot_no: 1, start_s: 0, end_s: 7.2, source_path: "E:\\lib\\a.mp4", match_level: "metaphor", match_reason: "城市高楼呼应换房" },
  { shot_no: 2, start_s: 7.2, end_s: 13, source_path: "E:\\lib\\b.mp4" },
  { shot_no: 3, start_s: 13, end_s: 20, source_path: "E:\\lib\\a.mp4" },
];

test("flags sources used more than once", () => {
  expect([...repeatedSources(shots)]).toEqual(["E:\\lib\\a.mp4"]);
});

test("renders the frame strip with reasons and reuse warnings", async () => {
  const api = vi.fn(async () => new Response(JSON.stringify({ supported: true, duration_s: 20, shots })));
  render(<StoryboardPanel api={api} taskID="t-1" />);
  expect(await screen.findByRole("heading", { name: /分镜预览/ })).toBeTruthy();
  expect(screen.getByText(/3 个镜头 · 0:20 · 1 个素材重复使用/)).toBeTruthy();
  expect(screen.getByText("城市高楼呼应换房")).toBeTruthy();
  expect(screen.getByText("b.mp4")).toBeTruthy();
  expect(screen.getAllByText("重复")).toHaveLength(2);
  expect(screen.getByAltText("镜头 1：a.mp4").getAttribute("src")).toContain("task_id=t-1&shot=1");
  expect(api).toHaveBeenCalledWith("/api/jianying-drafts/storyboard?task_id=t-1");
});

test("explains old plan formats instead of showing an empty strip", async () => {
  const api = vi.fn(async () => new Response(JSON.stringify({ supported: false })));
  render(<StoryboardPanel api={api} taskID="t-2" />);
  expect(await screen.findByText(/旧版计划格式/)).toBeTruthy();
});
