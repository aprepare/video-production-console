export type RemixPromptStyle = "rewrite" | "wash";

export function RemixPromptStyleFields({
  value,
  onChange,
  name,
}: {
  value: RemixPromptStyle;
  onChange: (value: RemixPromptStyle) => void;
  name: string;
}) {
  return (
    <fieldset className="remix-prompt-style">
      <legend>二创提示词</legend>
      <label className={value === "rewrite" ? "is-selected" : undefined}>
        <input
          type="radio"
          name={name}
          value="rewrite"
          checked={value === "rewrite"}
          onChange={() => onChange("rewrite")}
        />
        <span>
          <strong>换说法</strong>
          <small>同一台机器，换金句和例子。现在用的那套。</small>
        </span>
      </label>
      <label className={value === "wash" ? "is-selected" : undefined}>
        <input
          type="radio"
          name={name}
          value="wash"
          checked={value === "wash"}
          onChange={() => onChange("wash")}
        />
        <span>
          <strong>洗稿</strong>
          <small>保顺序、数字和例子，只改气口和少量用词。</small>
        </span>
      </label>
    </fieldset>
  );
}
