// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { PublishPackage, cleanCoverLine, shortTitleProblem } from "./PublishPackage";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const publishing = {
  short_titles: ["手里有存款的人这两年别乱动", "银行柜台悄悄撤掉了一样东西", "钱的新去处，看懂不吃亏"],
  descriptions: ["认知#楼市#认知提升#经济趋势", "#认知提升#趋势分析"],
  cover_titles: ["钱的新去处", "看懂这一步不吃亏"],
};

test("short titles follow the video-account rule: no punctuation, at most sixteen characters", () => {
  expect(shortTitleProblem("手里有存款的人这两年别乱动")).toBe("");
  expect(shortTitleProblem("钱的新去处，看懂不吃亏")).toBe("有标点或空格");
  expect(shortTitleProblem("一二三四五六七八九十一二三四五六七")).toBe("17 字，超了");
  expect(cleanCoverLine("钱的 新去处！")).toBe("钱的新去处");
});

test("each line copies on click and a bad short title is flagged", () => {
  const onCopy = vi.fn();
  render(<PublishPackage api={vi.fn()} runID="run-1" publishing={publishing} onCopy={onCopy} onMessage={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: /银行柜台悄悄撤掉了一样东西/ }));
  expect(onCopy).toHaveBeenCalledWith("银行柜台悄悄撤掉了一样东西", "短标题");
  fireEvent.click(screen.getByRole("button", { name: /认知#楼市/ }));
  expect(onCopy).toHaveBeenCalledWith("认知#楼市#认知提升#经济趋势", "简介");
  expect(screen.getByText("有标点或空格")).toBeTruthy();
});

test("the cover lines prefill from the package and generate a cover for this run", async () => {
  const api = vi.fn(async () => new Response(JSON.stringify({ path: "C:/Users/me/Videos/封面图/20261014_认知漫步_钱的新去处.png", url: "/api/remix-lab/runs/run-1/cover", width: 1080, height: 1920, account: "认知漫步" })));
  const onMessage = vi.fn();
  render(<PublishPackage api={api} runID="run-1" publishing={publishing} onCopy={vi.fn()} onMessage={onMessage} />);
  expect((screen.getByLabelText("第一行") as HTMLInputElement).value).toBe("钱的新去处");
  fireEvent.change(screen.getByLabelText("第二行"), { target: { value: "看懂 这一步！" } });
  fireEvent.click(screen.getByRole("button", { name: /生成封面/ }));
  await waitFor(() => expect(screen.getByText(/已保存：/)).toBeTruthy());
  expect(api).toHaveBeenCalledWith("/api/remix-lab/runs/run-1/cover", expect.objectContaining({ method: "POST", body: JSON.stringify({ line1: "钱的新去处", line2: "看懂这一步" }) }));
  expect(screen.getByRole("img").getAttribute("src")).toContain("/api/remix-lab/runs/run-1/cover");
});

test("empty cover lines are refused before calling the server", () => {
  const api = vi.fn();
  const onMessage = vi.fn();
  render(<PublishPackage api={api} runID="run-1" publishing={{ short_titles: [], descriptions: [] }} onCopy={vi.fn()} onMessage={onMessage} />);
  fireEvent.click(screen.getByRole("button", { name: /生成封面/ }));
  expect(api).not.toHaveBeenCalled();
  expect(onMessage).toHaveBeenCalledWith(expect.stringContaining("两行大字都要填"));
});
