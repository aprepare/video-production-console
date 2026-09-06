// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { RunFlowPanel } from "./RunFlow";
import type { RemixLabRunStage, RemixLabRunStagesView } from "./api";

afterEach(cleanup);

function fakeAPI(stages: RemixLabRunStage[], status = "completed") {
  const view: RemixLabRunStagesView = { run_id: "run-1", pipeline: "multi_agent", status, stages, edges: [] };
  return vi.fn(async () => new Response(JSON.stringify(view)));
}
const finalStage: RemixLabRunStage = {
  id: "final", kind: "output", title: "二创定稿", status: "ok",
  output: JSON.stringify({ continuous_script: "已经完成的定稿正文", short_titles: ["短标题一"] }),
};

test("fixed steps retain all agents and show the selected stage actual output", async () => {
  const api = fakeAPI([
    finalStage,
    { id: "writer", kind: "agent", title: "写手", status: "ok", output: "写手输出" },
    { id: "planner", kind: "agent", title: "策划", status: "ok", output: "策划实际输出", system_prompt: "策划实际提示词" },
    { id: "source", kind: "input", title: "原文", status: "ok", output: "原文实际输入" },
  ]);
  const { container } = render(<RunFlowPanel api={api} runID="run-1" onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} />);
  await screen.findByText("已经完成的定稿正文");
  fireEvent.click(screen.getByRole("button", { name: /策划/ }));
  expect(screen.getByLabelText("策划详情").textContent).toContain("策划实际输出");
  expect(screen.getByText("策划实际提示词")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /原文/ }));
  expect(screen.getByText("原文实际输入")).toBeTruthy();
  expect(container.querySelector(".react-flow")).toBeNull();
  const buttons = Array.from(container.querySelectorAll(".fixed-flow-workspace button"));
  expect(buttons.findIndex((button) => button.textContent?.includes("原文"))).toBeLessThan(buttons.findIndex((button) => button.textContent?.includes("策划")));
  expect(buttons.findIndex((button) => button.textContent?.includes("策划"))).toBeLessThan(buttons.findIndex((button) => button.textContent?.includes("写手")));
});

test("failed agent is selected and retries its ID with the edited model only once while pending", async () => {
  let resolveRetry!: () => void;
  const retry = vi.fn(() => new Promise<void>((resolve) => { resolveRetry = resolve; }));
  const api = fakeAPI([
    { id: "planner", kind: "agent", title: "策划", status: "failed", model: "old-model", error: "策划请求失败" }, finalStage,
  ], "failed");
  render(<RunFlowPanel api={api} runID="run-1" onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} onRetryNode={retry} />);
  fireEvent.change(await screen.findByLabelText("重试使用的模型"), { target: { value: " new-model " } });
  const button = screen.getByRole("button", { name: /重试此节点并续跑/ });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(retry).toHaveBeenCalledExactlyOnceWith("planner", "new-model");
  expect((button as HTMLButtonElement).disabled).toBe(true);
  resolveRetry();
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
});

test("current running stage is initially selected and a different run resets selection", async () => {
  const api = fakeAPI([{ id: "planner", kind: "agent", title: "策划", status: "running", output: "正在策划" }, finalStage], "running");
  const props = { api, runID: "run-1", onMessage: vi.fn(), onEditAgentPrompts: vi.fn() };
  const { rerender } = render(<RunFlowPanel {...props} />);
  expect(await screen.findByLabelText("策划详情")).toBeTruthy();
  const nextAPI = fakeAPI([finalStage]);
  rerender(<RunFlowPanel {...props} api={nextAPI} runID="run-2" />);
  expect(await screen.findByLabelText("二创定稿详情")).toBeTruthy();
});

test("production gate remains selectable with the real confirmation handler", async () => {
  const confirm = vi.fn();
  const api = fakeAPI([finalStage, { id: "produce-gate", kind: "gate", title: "确认生产", status: "waiting" }]);
  render(<RunFlowPanel api={api} runID="run-1" onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} onConfirmProduce={confirm} />);
  fireEvent.click(await screen.findByRole("button", { name: /确认生产/ }));
  fireEvent.click(screen.getByRole("button", { name: "确认开始混剪" }));
  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
});


test("failed production keeps its dedicated resume handler", async () => {
  const retryProduce = vi.fn();
  const retryNode = vi.fn();
  const api = fakeAPI([finalStage, { id: "produce-narration", kind: "produce", title: "配音", status: "failed", error: "配音失败" }]);
  render(<RunFlowPanel api={api} runID="run-1" onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} onRetryNode={retryNode} onRetryProduce={retryProduce} />);
  fireEvent.click(await screen.findByRole("button", { name: /重试生产并续跑/ }));
  await waitFor(() => expect(retryProduce).toHaveBeenCalledTimes(1));
  expect(retryNode).not.toHaveBeenCalled();
});


test("initial loading failure can be retried without remounting", async () => {
  const goodResponse = fakeAPI([finalStage]);
  const api = vi.fn().mockResolvedValueOnce(new Response("{}", { status: 500 })).mockImplementation(goodResponse);
  const onMessage = vi.fn();
  render(<RunFlowPanel api={api} runID="run-1" onMessage={onMessage} onEditAgentPrompts={vi.fn()} />);
  const retry = await screen.findByRole("button", { name: "重新读取步骤" });
  expect(screen.queryByText("正在读取工作流分解…")).toBeNull();
  fireEvent.click(retry);
  expect(await screen.findByLabelText("二创定稿详情")).toBeTruthy();
  expect(api).toHaveBeenCalledTimes(2);
});


test("live polling refreshes status without stealing selection, then focuses a new failure", async () => {
  vi.useFakeTimers();
  try {
    let failed = false;
    const api = vi.fn(async () => new Response(JSON.stringify({
      run_id: "run-1", pipeline: "multi_agent", status: failed ? "failed" : "running", edges: [],
      stages: [
        { id: "source", kind: "input", title: "原文", status: "ok", output: "已选中的原文" },
        { id: "planner", kind: "agent", title: "策划", status: failed ? "failed" : "running", error: failed ? "新的失败" : "" },
      ],
    })));
    await act(async () => { render(<RunFlowPanel api={api} runID="run-1" live onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} />); });
    expect(screen.getByLabelText("策划详情")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /原文/ }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2500); });
    expect(api).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText("原文详情")).toBeTruthy();
    failed = true;
    await act(async () => { await vi.advanceTimersByTimeAsync(2500); });
    expect(screen.getByLabelText("策划详情")).toBeTruthy();
    expect(screen.getByText("新的失败")).toBeTruthy();
    cleanup();
  } finally {
    vi.useRealTimers();
  }
});
