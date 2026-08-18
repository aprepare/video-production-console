import { expect, test } from "vitest";
import { unwrapImportedScript } from "./import-script";

const remixJSON = `{
  "continuous_script": "又一批人要发财了。人民币第三次换锚已经开始。",
  "titles": ["人民币第三次换锚来了", "下一批先富的人在哪", "旧锚退潮钱去哪", "一百七十万亿在找出口", "第三个锚先不说完", "窗口不会一直开着", "看懂资金方向先上车", "别只盯着工资存款"],
  "short_titles": ["第三次换锚来了", "钱会流向哪里", "下一批赢家是谁", "窗口不会等人", "现在就上车吧"],
  "descriptions": ["描述一", "描述二", "描述三"],
  "topics": ["#经济", "#思维认知", "#干货分享"],
  "cta": "关掉干扰，现在就去主页橱窗看《财富觉醒方法论》。"
}`;

test("leaves plain finished scripts unchanged", () => {
  const got = unwrapImportedScript("八月这一波要发财的人");
  expect(got).toEqual({
    ok: true,
    script: "八月这一波要发财的人",
    fromJSON: false,
    hasPublishing: false,
  });
});

test("extracts continuous_script from remix JSON", () => {
  const got = unwrapImportedScript("```json\n" + remixJSON + "\n```");
  expect(got.ok).toBe(true);
  if (!got.ok) return;
  expect(got.script).toBe("又一批人要发财了。人民币第三次换锚已经开始。");
  expect(got.fromJSON).toBe(true);
  expect(got.hasPublishing).toBe(true);
  expect(got.script).not.toContain("titles");
});

test("rejects remix JSON that has no continuous_script", () => {
  const got = unwrapImportedScript(`{"titles":["人民币第三次换锚来了"],"cta":"上车"}`);
  expect(got.ok).toBe(false);
  if (got.ok) return;
  expect(got.message).toContain("continuous_script");
});
