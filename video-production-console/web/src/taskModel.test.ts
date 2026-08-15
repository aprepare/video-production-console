import { expect, test } from "vitest";
import { inheritedTaskModel } from "./taskModel";

test("remix inherits remix model first", () => {
  expect(
    inheritedTaskModel(
      { remix_model: "cursor-grok-4.6-xhigh-fast", codex_default_model: "gpt-5.6-sol" },
      "remix",
    ),
  ).toBe("cursor-grok-4.6-xhigh-fast");
});

test("montage inherits Codex default and ignores remix model", () => {
  expect(
    inheritedTaskModel(
      { remix_model: "cursor-grok-4.6-xhigh-fast", codex_default_model: "gpt-5.6-sol" },
      "codex",
    ),
  ).toBe("gpt-5.6-sol");
});
