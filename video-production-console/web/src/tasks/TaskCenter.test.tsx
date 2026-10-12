// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import type { Task } from "../types";
import { TaskCenter, groupTasks } from "./TaskCenter";

afterEach(cleanup);

const now = Date.parse("2026-10-12T12:00:00Z");
const task = (id: string, status: string, hoursAgo: number, extra: Partial<Task> = {}): Task => ({
  id, status, type: "montage", skill_name: "jianying-montage-draft", action: "montage.execute",
  project_id: "p1", created_at: new Date(now - hoursAgo * 3600_000).toISOString(), ...extra,
} as Task);

test("groups live, recent failures and recent completions", () => {
  const groups = groupTasks([
    task("a", "running", 0.1),
    task("b", "failed", 2, { error_message: "registration_failed" }),
    task("c", "failed", 72),
    task("d", "completed", 1),
    task("e", "interrupted", 1),
  ], now);
  expect(groups.live.map((t) => t.id)).toEqual(["a"]);
  expect(groups.failed.map((t) => t.id)).toEqual(["b", "e"]);
  expect(groups.done.map((t) => t.id)).toEqual(["d"]);
});

test("drawer lists failures with their reason and opens the project", async () => {
  vi.setSystemTime(now);
  const api = vi.fn(async (path: string) => new Response(JSON.stringify(path === "/api/tasks"
    ? [task("b", "failed", 1, { error_message: "剪映登记失败" })]
    : []), { status: 200 }));
  const onOpenProject = vi.fn();
  render(<TaskCenter api={api} projects={[{ id: "p1", title: "岚山海鲜 第3条" }]} onOpenProject={onOpenProject} />);
  const trigger = await screen.findByRole("button", { name: /任务中心，0 个进行中，1 个失败/ });
  fireEvent.click(trigger);
  expect(screen.getByRole("dialog", { name: "任务中心" })).toBeTruthy();
  expect(screen.getByText("剪映登记失败")).toBeTruthy();
  fireEvent.click(screen.getByText("岚山海鲜 第3条", { exact: false }));
  await waitFor(() => expect(onOpenProject).toHaveBeenCalledWith("p1"));
  vi.useRealTimers();
});
