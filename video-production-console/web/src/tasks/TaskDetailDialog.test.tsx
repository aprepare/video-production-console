// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { TaskDetailDialog } from "./TaskDetailDialog";
import type { Task } from "../types";

afterEach(cleanup);

test("pending task actions keep the answer and prevent duplicate submissions", () => {
  const task: Task = { id: "task-1", type: "remix", skill_name: "remix", status: "waiting_input", created_at: "2026-09-05", action: "montage.execute", montage: { phase: "registration_failed", can_retry_registration: true }, messages: [{ id: "q", role: "assistant", content: "", question_schema: '["继续吗？"]', created_at: "2026-09-05" }] };
  const onAnswer = vi.fn();
  const onCancelTask = vi.fn();
  const onRetryRegistration = vi.fn();
  const props = { task, projectTitle: "项目", timingNow: 0, directoryManifest: null, directoryManifestStatus: "", canOpenDirectory: false, openingDirectory: false, answerInput: "保留这段回答", onAnswerInputChange: vi.fn(), onClose: vi.fn(), onCancelTask, onRetryRegistration, onOpenDirectory: vi.fn(), onAnswer };
  const { rerender } = render(<TaskDetailDialog {...props} actionPending />);
  expect((screen.getByLabelText("回答任务") as HTMLTextAreaElement).value).toBe("保留这段回答");
  expect((screen.getByRole("button", { name: "发送回答" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.submit(screen.getByLabelText("回答任务").closest("form")!);
  fireEvent.click(screen.getByRole("button", { name: "停止任务" }));
  fireEvent.click(screen.getByRole("button", { name: "只重试剪映登记" }));
  expect(onAnswer).not.toHaveBeenCalled();
  expect(onCancelTask).not.toHaveBeenCalled();
  expect(onRetryRegistration).not.toHaveBeenCalled();
  rerender(<TaskDetailDialog {...props} actionPending={false} actionError="回答失败，请重试" />);
  expect(screen.getByRole("alert").textContent).toContain("回答失败，请重试");
  fireEvent.click(screen.getByRole("button", { name: "发送回答" }));
  expect(onAnswer).toHaveBeenCalledWith(task, "保留这段回答");
});
