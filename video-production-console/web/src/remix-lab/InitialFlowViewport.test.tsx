// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { InitialFlowViewport } from "./InitialFlowViewport";
const flow = vi.hoisted(() => ({ width: 1200, height: 600, fitView: vi.fn(), getNodes: vi.fn(() => [
  { id: "source", position: { x: 0, y: 200 } },
  { id: "hook", position: { x: 300, y: 0 } },
  { id: "writer", position: { x: 600, y: 200 } },
  { id: "review", position: { x: 1200, y: 200 } },
  { id: "produce-gate", position: { x: 0, y: 700 } },
]) }));
vi.mock("@xyflow/react", () => ({
  useNodesInitialized: () => true,
  useStore: (select: (state: { width: number; height: number }) => unknown) => select(flow),
  useReactFlow: () => flow,
}));
afterEach(() => { cleanup(); vi.useRealTimers(); flow.fitView.mockClear(); flow.width = 1200; });
test("initial view fits the input side without centering the distant full graph", () => {
  vi.useFakeTimers();
  render(<InitialFlowViewport token="design" />);
  vi.runAllTimers();
  const options = flow.fitView.mock.calls[0][0];
  expect(options.nodes.map((node: { id: string }) => node.id)).toEqual(["source", "hook", "writer"]);
  expect(options.minZoom).toBeLessThan(0.72);
});
test("resizing a narrow canvas refocuses the input alone", () => {
  vi.useFakeTimers();
  const view = render(<InitialFlowViewport token="design" />);
  vi.runAllTimers();
  flow.width = 390;
  view.rerender(<InitialFlowViewport token="design" />);
  vi.runAllTimers();
  expect(flow.fitView.mock.lastCall?.[0].nodes.map((node: { id: string }) => node.id)).toEqual(["source"]);
});
