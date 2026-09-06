// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ProductionRail } from "./ProductionRail";
afterEach(cleanup);
test("delivery review is distinct from confirmed publication", () => {
  const { rerender } = render(<ProductionRail currentStage="review" />);
  expect(within(document.querySelector('[aria-current="step"]') as HTMLElement).getByText("待人工确认")).toBeTruthy();
  rerender(<ProductionRail currentStage="published" />);
  expect(within(document.querySelector('[aria-current="step"]') as HTMLElement).getByText("已确认发布")).toBeTruthy();
  expect(screen.queryByText("正在制作")).toBeNull();
});
