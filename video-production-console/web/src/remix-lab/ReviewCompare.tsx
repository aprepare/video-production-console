import { useMemo, useState } from "react";
import { Check } from "lucide-react";
import type { RemixLabPackageInput, RemixLabReviewRecord } from "./api";
import { chunkDiff, diffSentences, diffStats, listItemChanged, type DiffOp } from "./sentenceDiff";

/** 审稿前后可对照、可逐项采用的字段（cta 不上屏）。 */
export type CompareField = "continuous_script" | "short_titles" | "descriptions" | "topics" | "titles";

const FIELD_META: Array<{ field: CompareField; label: string; optional?: boolean }> = [
  { field: "continuous_script", label: "正文" },
  { field: "short_titles", label: "短标题" },
  { field: "descriptions", label: "视频描述" },
  { field: "topics", label: "话题" },
  { field: "titles", label: "长标题", optional: true },
];

function toStringList(value: unknown): string[] {
  return Array.isArray(value) ? value.map((item) => String(item)) : [];
}

function cleanList(items: string[]): string[] {
  return items.map((item) => item.trim()).filter(Boolean);
}

/** 把写手 JSON / 审稿快照的任意残缺形状补成完整发布包；没有正文视为无效。 */
export function normalizeDraft(raw: unknown): RemixLabPackageInput | null {
  if (!raw || typeof raw !== "object") return null;
  const source = raw as Partial<Record<keyof RemixLabPackageInput, unknown>>;
  const script = typeof source.continuous_script === "string" ? source.continuous_script.trim() : "";
  if (!script) return null;
  return {
    continuous_script: script,
    titles: toStringList(source.titles),
    short_titles: toStringList(source.short_titles),
    descriptions: toStringList(source.descriptions),
    topics: toStringList(source.topics),
    cta: typeof source.cta === "string" ? source.cta : "",
  };
}

export function parseDraftJSON(json: string | undefined): RemixLabPackageInput | null {
  const raw = (json ?? "").trim();
  if (!raw) return null;
  try {
    return normalizeDraft(JSON.parse(raw));
  } catch {
    return null;
  }
}

export type ReviewVersions = {
  /** 本轮进审稿。首轮是写手初稿；打回轮次是打回前的定稿。 */
  before: RemixLabPackageInput | null;
  /** 审稿修订稿；审稿没改（pass/skipped/error）时为 null。 */
  after: RemixLabPackageInput | null;
};

/**
 * 从审稿结论解出两版。结论里的快照优先；老运行的结论没有快照时，审稿前退回
 * draft_v1 留档，审稿后退回 fallbackAfter（通常是当前定稿）。
 */
export function resolveReviewVersions(
  review: RemixLabReviewRecord | null,
  draftV1JSON: string | undefined,
  fallbackAfter?: RemixLabPackageInput | null,
): ReviewVersions {
  const before = normalizeDraft(review?.before) ?? parseDraftJSON(draftV1JSON);
  let after = normalizeDraft(review?.revised);
  if (!after && review?.verdict === "fixed" && fallbackAfter) {
    after = fallbackAfter;
  }
  return { before, after };
}

export function fieldEqual(field: CompareField, a: RemixLabPackageInput, b: RemixLabPackageInput): boolean {
  if (field === "continuous_script") return a.continuous_script.trim() === b.continuous_script.trim();
  const left = cleanList(a[field]);
  const right = cleanList(b[field]);
  return left.length === right.length && left.every((item, index) => item === right[index]);
}

/** 两版之间有差异的字段（用于「审稿改了哪些」摘要）。 */
export function changedFields(before: RemixLabPackageInput, after: RemixLabPackageInput | null): CompareField[] {
  if (!after) return [];
  return FIELD_META.filter(({ field }) => !fieldEqual(field, before, after)).map(({ field }) => field);
}

export function fieldLabel(field: CompareField): string {
  return FIELD_META.find((meta) => meta.field === field)?.label ?? field;
}

export function reviewVerdictLabel(verdict: string): string {
  switch (verdict) {
    case "pass":
      return "审稿通过";
    case "fixed":
      return "审稿已修订";
    case "skipped":
      return "审稿跳过";
    case "error":
      return "审稿失败";
    default:
      return `审稿：${verdict}`;
  }
}

export function reviewVerdictTone(verdict: string): "ok" | "live" | "warn" {
  switch (verdict) {
    case "pass":
      return "ok";
    case "fixed":
      return "live";
    default:
      return "warn";
  }
}

/** 审稿结论的 issues 列表（改稿工作台与工作流检视栏共用）。 */
export function ReviewIssueList({ review }: { review: RemixLabReviewRecord }) {
  const issues = review.issues ?? [];
  if (issues.length === 0) return null;
  return (
    <ol className="remix-lab-review__issues">
      {issues.map((issue, index) => (
        <li key={index}>
          <strong>{issue.problem}</strong>
          {issue.where ? <span className="remix-lab-review__where">「{issue.where}」</span> : null}
          {issue.fix ? <p>{issue.fix}</p> : null}
        </li>
      ))}
    </ol>
  );
}

type ReviewCompareProps = {
  before: RemixLabPackageInput;
  after: RemixLabPackageInput | null;
  /** 当前定稿：给了才能标出「当前定稿用的这版」。 */
  current?: RemixLabPackageInput;
  /** 给了每个字段就出现「用这版」按钮，回填到当前定稿（由调用方保存）。 */
  onAdopt?: (patch: Partial<RemixLabPackageInput>) => void;
  /** 窄容器（工作流检视栏）里上下堆叠。 */
  stacked?: boolean;
};

type ScriptView = "diff" | "split";

/**
 * 审稿前 / 审稿后对照：正文默认只看改动（句级 diff，删的划掉、加的标亮，
 * 没改的上下文折叠），可切到并排全文；发布字段改动过的两栏并列并标出差异条，
 * 没动的只显示一遍。有 onAdopt 时每一版下面有「用这版」，操作员自己决定采不采用。
 */
export function ReviewCompare({ before, after, current, onAdopt, stacked }: ReviewCompareProps) {
  const revised = after ?? before;
  const changed = changedFields(before, after);
  const [scriptView, setScriptView] = useState<ScriptView>("diff");
  const scriptOps = useMemo(
    () => (after ? diffSentences(before.continuous_script, after.continuous_script) : []),
    [before.continuous_script, after],
  );
  const scriptStats = useMemo(() => diffStats(scriptOps), [scriptOps]);
  const rows = FIELD_META.filter(({ field, optional }) => {
    if (!optional) return true;
    return cleanList(before[field] as string[]).length > 0 || cleanList(revised[field] as string[]).length > 0;
  });

  const pick = (source: RemixLabPackageInput, fields: CompareField[]): Partial<RemixLabPackageInput> => {
    const patch: Partial<RemixLabPackageInput> = {};
    for (const field of fields) {
      if (field === "continuous_script") patch.continuous_script = source.continuous_script;
      else patch[field] = [...source[field]];
    }
    return patch;
  };

  const usingBeforeAll = current ? rows.every(({ field }) => fieldEqual(field, current, before)) : false;
  const usingAfterAll = current && after ? rows.every(({ field }) => fieldEqual(field, current, after)) : false;
  const allFields = rows.map(({ field }) => field);

  return (
    <div className={stacked ? "remix-lab-compare remix-lab-compare--stacked" : "remix-lab-compare"}>
      <div className="remix-lab-compare__head">
        <div className="remix-lab-compare__col">
          <strong>审稿前</strong>
          <span className="remix-lab-muted">进审稿原样</span>
          {onAdopt ? (
            usingBeforeAll ? (
              <span className="remix-lab-compare__using">
                <Check size={12} strokeWidth={2.5} />
                当前定稿
              </span>
            ) : (
              <button type="button" className="header-button remix-lab-compare__adopt" onClick={() => onAdopt(pick(before, allFields))}>
                全部用审稿前
              </button>
            )
          ) : null}
        </div>
        <div className="remix-lab-compare__col">
          <strong>审稿后</strong>
          <span className="remix-lab-muted">
            {after ? `审稿改了 ${changed.length} 处字段：${changed.map(fieldLabel).join("、") || "无"}` : "审稿没有改动，与审稿前一致"}
          </span>
          {onAdopt && after ? (
            usingAfterAll ? (
              <span className="remix-lab-compare__using">
                <Check size={12} strokeWidth={2.5} />
                当前定稿
              </span>
            ) : (
              <button type="button" className="header-button remix-lab-compare__adopt" onClick={() => onAdopt(pick(after, allFields))}>
                全部用审稿后
              </button>
            )
          ) : null}
        </div>
      </div>

      {rows.map(({ field, label }) => {
        const isChanged = changed.includes(field);
        const currentMatchesBefore = current ? fieldEqual(field, current, before) : false;
        const currentMatchesAfter = current && after ? fieldEqual(field, current, after) : false;
        const renderFooter = (source: RemixLabPackageInput, matches: boolean, version: "审稿前" | "审稿后") => {
          if (!onAdopt) return null;
          return (
            <div className="remix-lab-compare__foot">
              {matches ? (
                <span className="remix-lab-compare__using">
                  <Check size={12} strokeWidth={2.5} />
                  当前定稿
                </span>
              ) : (
                <button
                  type="button"
                  className="header-button remix-lab-compare__adopt"
                  aria-label={`${label}用${version}`}
                  onClick={() => onAdopt(pick(source, [field]))}
                >
                  用{version}
                </button>
              )}
            </div>
          );
        };
        const isScript = field === "continuous_script";
        const showDiff = isScript && isChanged && scriptView === "diff";
        return (
          <section
            key={field}
            className={isChanged ? "remix-lab-compare__row remix-lab-compare__row--changed" : "remix-lab-compare__row"}
          >
            <header className="remix-lab-compare__row-head">
              <h5>{label}</h5>
              <span className={isChanged ? "remix-lab-chip remix-lab-chip--review-live" : "remix-lab-chip"}>
                {isChanged
                  ? isScript
                    ? `审稿改了 ${scriptStats.deleted + scriptStats.added} 句（删 ${scriptStats.deleted} · 加 ${scriptStats.added}）`
                    : "审稿改了"
                  : after
                    ? "两版一致"
                    : "未改动"}
              </span>
              {!isChanged && onAdopt && !currentMatchesBefore ? (
                <span className="remix-lab-muted remix-lab-compare__note">当前定稿是你手改过的版本</span>
              ) : null}
              {isScript && isChanged ? (
                <div className="remix-lab-compare__views" role="group" aria-label="正文对照方式">
                  <button
                    type="button"
                    className={scriptView === "diff" ? "remix-lab-compare__view remix-lab-compare__view--on" : "remix-lab-compare__view"}
                    aria-pressed={scriptView === "diff"}
                    onClick={() => setScriptView("diff")}
                  >
                    只看改动
                  </button>
                  <button
                    type="button"
                    className={scriptView === "split" ? "remix-lab-compare__view remix-lab-compare__view--on" : "remix-lab-compare__view"}
                    aria-pressed={scriptView === "split"}
                    onClick={() => setScriptView("split")}
                  >
                    并排全文
                  </button>
                </div>
              ) : null}
            </header>
            {showDiff ? (
              <div className="remix-lab-compare__cells remix-lab-compare__cells--single">
                <div className="remix-lab-compare__cell">
                  <ScriptDiff ops={scriptOps} />
                  {onAdopt ? (
                    <div className="remix-lab-compare__foot remix-lab-compare__foot--pair">
                      {renderFooter(before, currentMatchesBefore, "审稿前")}
                      {renderFooter(revised, currentMatchesAfter, "审稿后")}
                    </div>
                  ) : null}
                </div>
              </div>
            ) : isChanged ? (
              <div className="remix-lab-compare__cells">
                <div className="remix-lab-compare__cell remix-lab-compare__cell--before">
                  <FieldContent field={field} value={before} other={revised} ops={isScript ? scriptOps : undefined} side="before" />
                  {renderFooter(before, currentMatchesBefore, "审稿前")}
                </div>
                <div className="remix-lab-compare__cell remix-lab-compare__cell--after">
                  <FieldContent field={field} value={revised} other={before} ops={isScript ? scriptOps : undefined} side="after" />
                  {renderFooter(revised, currentMatchesAfter, "审稿后")}
                </div>
              </div>
            ) : (
              <div className="remix-lab-compare__cells remix-lab-compare__cells--single">
                <div className="remix-lab-compare__cell">
                  <FieldContent field={field} value={before} />
                  {!currentMatchesBefore ? renderFooter(before, false, "审稿前") : null}
                </div>
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}

/** 只看改动：删的划掉、加的标亮，长段没改的折叠成一行，点开才展开。 */
function ScriptDiff({ ops }: { ops: DiffOp[] }) {
  const chunks = useMemo(() => chunkDiff(ops, 1), [ops]);
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const renderOps = (items: DiffOp[], keyPrefix: string) =>
    items.map((op, index) => (
      <span
        key={`${keyPrefix}-${index}`}
        className={
          op.type === "del"
            ? "remix-lab-diff__del"
            : op.type === "add"
              ? "remix-lab-diff__add"
              : "remix-lab-diff__same"
        }
      >
        {op.text}
      </span>
    ));
  return (
    <div className="remix-lab-diff" aria-label="正文改动">
      {chunks.map((chunk, index) => {
        if (chunk.kind === "change") {
          return <span key={index}>{renderOps(chunk.ops, `c${index}`)}</span>;
        }
        if (!chunk.collapsible || expanded.has(index)) {
          return <span key={index}>{renderOps(chunk.ops, `s${index}`)}</span>;
        }
        const hiddenCount = chunk.hidden.filter((op) => op.text.trim()).length;
        return (
          <span key={index}>
            {renderOps(chunk.head, `h${index}`)}
            <button
              type="button"
              className="remix-lab-diff__fold"
              onClick={() => setExpanded((current) => new Set(current).add(index))}
            >
              …中间 {hiddenCount} 句没改，点开看…
            </button>
            {renderOps(chunk.tail, `t${index}`)}
          </span>
        );
      })}
    </div>
  );
}

function FieldContent({
  field,
  value,
  other,
  ops,
  side,
}: {
  field: CompareField;
  value: RemixLabPackageInput;
  /** 另一版：给了就把这版里对方没有的条目标出来。 */
  other?: RemixLabPackageInput;
  /** 正文并排时用同一份 diff 给本侧的改动句上色。 */
  ops?: DiffOp[];
  side?: "before" | "after";
}) {
  if (field === "continuous_script") {
    if (ops && side) {
      const mine = side === "before" ? "del" : "add";
      return (
        <pre className="remix-lab-compare__text">
          {ops
            .filter((op) => op.type === "same" || op.type === mine)
            .map((op, index) => (
              <span key={index} className={op.type === mine ? `remix-lab-diff__${mine} remix-lab-diff__${mine}--plain` : undefined}>
                {op.text}
              </span>
            ))}
        </pre>
      );
    }
    return <pre className="remix-lab-compare__text">{value.continuous_script.trim()}</pre>;
  }
  const items = cleanList(value[field]);
  if (items.length === 0) {
    return <p className="remix-lab-compare__empty remix-lab-muted">（空）</p>;
  }
  const otherItems = other ? cleanList(other[field]) : null;
  return (
    <ol className="remix-lab-compare__list">
      {items.map((item, index) => (
        <li
          key={`${field}-${index}`}
          className={otherItems && listItemChanged(item, otherItems) ? "remix-lab-compare__item--changed" : undefined}
        >
          {item}
        </li>
      ))}
    </ol>
  );
}
