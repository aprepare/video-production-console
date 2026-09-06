// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { FastModeButton } from "./FastModeButton";

afterEach(cleanup);

test("explicit off overrides a Fast preset and can return to inheritance", () => {
  const onChange = vi.fn();
  const { rerender } = render(<FastModeButton label="Fast" inheritedValue="priority" onChange={onChange} />);
  expect(screen.getByRole("button", { name: "Fast" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Fast" }));
  expect(onChange).toHaveBeenLastCalledWith("default");
  rerender(<FastModeButton label="Fast" value="default" inheritedValue="priority" onChange={onChange} />);
  expect(screen.getByRole("button", { name: "Fast" }).getAttribute("aria-pressed")).toBe("false");
  fireEvent.click(screen.getByRole("button", { name: "跟随默认档" }));
  expect(onChange).toHaveBeenLastCalledWith("");
});
