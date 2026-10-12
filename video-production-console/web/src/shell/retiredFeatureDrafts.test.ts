// @vitest-environment jsdom
import { afterEach, expect, test } from "vitest";
import { clearRetiredFeatureDrafts } from "./retiredFeatureDrafts";

afterEach(() => sessionStorage.clear());

test("clears all retired AI drafts and preserves copy and image drafts", () => {
  sessionStorage.setItem("console:ai-short-draft:first", "old draft");
  sessionStorage.setItem("console:remix-draft", "copy draft");
  sessionStorage.setItem("console:ai-short-draft:second", "old draft");
  sessionStorage.setItem("console:image-draft", "image draft");
  clearRetiredFeatureDrafts(sessionStorage);
  expect(sessionStorage.getItem("console:ai-short-draft:first")).toBeNull();
  expect(sessionStorage.getItem("console:ai-short-draft:second")).toBeNull();
  expect(sessionStorage.getItem("console:remix-draft")).toBe("copy draft");
  expect(sessionStorage.getItem("console:image-draft")).toBe("image draft");
});
