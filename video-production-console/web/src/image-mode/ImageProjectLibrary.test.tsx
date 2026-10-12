// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import type { ImageProject } from "../types";
import { ImageProjectLibrary } from "./ImageProjectLibrary";

afterEach(cleanup);
const projects = [
  { id: "one", title: "养老现金流", status: "ready", image_count: 8, ratio: "3:4" },
  { id: "two", title: "家庭现金流", status: "draft", run_status: "running", image_count: 12, ratio: "3:4" },
  { id: "three", title: "资产配置", status: "draft", run_status: "interrupted", image_count: 6, ratio: "1:1" },
] as ImageProject[];

test("title and status filters combine without changing the project list", () => {
  render(<ImageProjectLibrary projects={projects} state="ready" busy={false} onOpen={vi.fn()} onRetry={vi.fn()} />);
  fireEvent.change(screen.getByRole("searchbox", { name: "搜索图文项目" }), { target: { value: "现金流" } });
  expect(screen.getByRole("button", { name: /养老现金流/ })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /资产配置/ })).toBeNull();
  fireEvent.change(screen.getByLabelText("筛选图文项目状态"), { target: { value: "working" } });
  expect(screen.queryByRole("button", { name: /养老现金流/ })).toBeNull();
  expect(screen.getByRole("button", { name: /家庭现金流/ })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
  expect(screen.getByRole("button", { name: /资产配置/ })).toBeTruthy();
  expect(projects).toHaveLength(3);
});

test("interrupted work can be found, opened, and distinguished from no matching result", () => {
  const open = vi.fn();
  render(<ImageProjectLibrary projects={projects} state="ready" busy={false} onOpen={open} onRetry={vi.fn()} />);
  fireEvent.change(screen.getByLabelText("筛选图文项目状态"), { target: { value: "attention" } });
  fireEvent.click(screen.getByRole("button", { name: /资产配置/ }));
  expect(open).toHaveBeenCalledWith(projects[2]);
  fireEvent.change(screen.getByRole("searchbox", { name: "搜索图文项目" }), { target: { value: "不存在" } });
  expect(screen.getByText("没有匹配的项目")).toBeTruthy();
  expect(screen.queryByText("还没有图文项目")).toBeNull();
});

test("failed list loading exposes a retry while preserving already loaded projects", () => {
  const retry = vi.fn();
  render(<ImageProjectLibrary projects={projects} state="error" busy={true} onOpen={vi.fn()} onRetry={retry} />);
  expect(screen.getByRole("alert")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "重试读取项目列表" }));
  expect(retry).toHaveBeenCalledOnce();
  expect((screen.getByRole("button", { name: /养老现金流/ }) as HTMLButtonElement).disabled).toBe(true);
});

test("partial projects without failure counts are shown as needing attention", () => {
  const partial = { id: "partial", title: "部分成功", status: "partial", image_count: 8, ratio: "3:4" } as ImageProject;
  render(<ImageProjectLibrary projects={[partial]} state="ready" busy={false} onOpen={vi.fn()} onRetry={vi.fn()} />);
  fireEvent.change(screen.getByLabelText("筛选图文项目状态"), { target: { value: "attention" } });
  expect(screen.getByRole("button", { name: /部分成功.*需要处理/ })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /部分成功.*待生成/ })).toBeNull();
});
