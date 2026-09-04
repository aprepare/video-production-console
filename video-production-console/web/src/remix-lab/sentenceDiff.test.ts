import { expect, test } from "vitest";
import { chunkDiff, diffSentences, diffStats, listItemChanged, splitSentences } from "./sentenceDiff";

test("splitSentences keeps punctuation and newlines so the text round-trips", () => {
  const text = "第一句。第二句！\n\n第三句？没标点的尾巴";
  const parts = splitSentences(text);
  expect(parts).toEqual(["第一句。", "第二句！", "\n", "\n", "第三句？", "没标点的尾巴"]);
  expect(parts.join("")).toBe(text);
});

test("diffSentences marks only the replaced sentence", () => {
  const before = "开头砸钩子。中间讲道理。结尾催上车。";
  const after = "开头砸钩子。中间换了个说法讲道理。结尾催上车。";
  const ops = diffSentences(before, after);
  expect(ops).toEqual([
    { type: "same", text: "开头砸钩子。" },
    { type: "del", text: "中间讲道理。" },
    { type: "add", text: "中间换了个说法讲道理。" },
    { type: "same", text: "结尾催上车。" },
  ]);
  expect(diffStats(ops)).toEqual({ deleted: 1, added: 1, unchanged: 2 });
});

test("diffSentences ignores surrounding whitespace differences", () => {
  const ops = diffSentences("一句。 二句。", "一句。二句。");
  expect(ops.every((op) => op.type === "same")).toBe(true);
});

test("chunkDiff collapses long unchanged runs but keeps one sentence of context each side", () => {
  const before = "一。二。三。四。五。六。七。";
  const after = "一。二。三。四。五。六。改过的七。";
  const chunks = chunkDiff(diffSentences(before, after), 1);
  expect(chunks).toHaveLength(2);
  const context = chunks[0];
  expect(context.kind).toBe("context");
  if (context.kind === "context") {
    expect(context.collapsible).toBe(true);
    expect(context.head.map((op) => op.text)).toEqual(["一。"]);
    expect(context.hidden.map((op) => op.text)).toEqual(["二。", "三。", "四。", "五。"]);
    expect(context.tail.map((op) => op.text)).toEqual(["六。"]);
  }
  expect(chunks[1].kind).toBe("change");
});

test("chunkDiff keeps short unchanged runs inline", () => {
  const chunks = chunkDiff(diffSentences("一。二。", "一。改二。"), 1);
  expect(chunks[0]).toMatchObject({ kind: "context", collapsible: false });
});

test("listItemChanged compares trimmed items", () => {
  expect(listItemChanged("板标题甲 ", ["板标题甲", "副标题"])).toBe(false);
  expect(listItemChanged("新板标题", ["板标题甲", "副标题"])).toBe(true);
});
