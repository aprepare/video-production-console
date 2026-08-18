// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ModeHome } from "./ModeHome";
import { parseMontageKind, productionModes } from "./catalog";

afterEach(cleanup);

test("renders the production catalog without a binary mode group", () => {
  render(<ModeHome modes={productionModes} onNavigate={() => undefined} />);
  expect(screen.getByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
  expect(screen.getAllByRole("article")).toHaveLength(4);
  expect(screen.getByRole("button", { name: "进入风景混剪" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "进入电影混剪" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "进入图片视频" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "进入图文制作" })).toBeTruthy();
  expect(screen.queryByRole("group", { name: "生产模式" })).toBeNull();
});

test("invokes navigation callback with mode-specific addresses", () => {
  const onNavigate = vi.fn();
  render(<ModeHome modes={productionModes} onNavigate={onNavigate} />);
  fireEvent.click(screen.getByRole("button", { name: "进入风景混剪" }));
  expect(onNavigate).toHaveBeenCalledWith("/projects?mode=scenic");
  fireEvent.click(screen.getByRole("button", { name: "进入电影混剪" }));
  expect(onNavigate).toHaveBeenCalledWith("/projects?mode=movie");
  fireEvent.click(screen.getByRole("button", { name: "进入图片视频" }));
  expect(onNavigate).toHaveBeenCalledWith("/projects?mode=image-video");
  fireEvent.click(screen.getByRole("button", { name: "进入图文制作" }));
  expect(onNavigate).toHaveBeenCalledWith("/image-projects");
});

test("parses montage kind from the query string", () => {
  expect(parseMontageKind("?mode=movie")).toBe("movie");
  expect(parseMontageKind("mode=image-video")).toBe("image-video");
  expect(parseMontageKind("?mode=scenic")).toBe("scenic");
  expect(parseMontageKind("")).toBe("scenic");
});
