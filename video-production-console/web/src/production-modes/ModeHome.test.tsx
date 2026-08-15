// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ModeHome } from "./ModeHome";
import { productionModes } from "./catalog";

afterEach(cleanup);

test("renders the production catalog without a binary mode group", () => {
  render(<ModeHome modes={productionModes} onNavigate={() => undefined} />);
  expect(screen.getByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
  expect(screen.getAllByRole("article")).toHaveLength(productionModes.length);
  expect(screen.getByRole("button", { name: "进入电影混剪" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "进入图文制作" })).toBeTruthy();
  expect(screen.queryByRole("group", { name: "生产模式" })).toBeNull();
});

test("invokes navigation callback", () => {
  const onNavigate = vi.fn();
  render(<ModeHome modes={productionModes} onNavigate={onNavigate} />);
  fireEvent.click(screen.getByRole("button", { name: "进入图文制作" }));
  expect(onNavigate).toHaveBeenCalledWith("/image-projects");
});
