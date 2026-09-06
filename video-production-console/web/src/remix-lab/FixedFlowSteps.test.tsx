// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { FixedFlowSteps } from "./FixedFlowSteps";

test("fixed steps show their real status and select without executing work", () => {
  const select = vi.fn();
  render(<FixedFlowSteps selectedID="source" onSelect={select} steps={[
    { id: "source", title: "原文输入", group: "create", status: "ok" },
    { id: "writer", title: "生成文案", group: "create", status: "running" },
    { id: "produce-gate", title: "确认二创", group: "produce", status: "waiting_confirm" },
  ]} />);
  expect(screen.getByRole("navigation", { name: "流程步骤" })).toBeTruthy();
  expect(screen.getByRole("button", { name: /原文输入/ }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByText("进行中")).toBeTruthy();
  expect(screen.getByText("待确认")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /生成文案/ }));
  expect(select).toHaveBeenCalledExactlyOnceWith("writer");
  expect(screen.queryByRole("button", { name: /zoom|放大|缩小/i })).toBeNull();
});
