import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ChevronDown, PenLine } from "lucide-react";
import { ModelSelect } from "../ModelSelect";
import { reasoningEfforts } from "../taskModel";
import type { ReasoningEffort } from "../taskModel";
import type { ImageProject, QuickImageProjectRequest, QuickImageProjectResponse } from "../types";

type API = (path: string, init?: RequestInit) => Promise<Response>;

export type QuickGenerateFormProps = {
  api: API;
  defaultRatio: ImageProject["ratio"];
  defaultStyle: string;
  defaultConcurrency: number;
  defaultTextModel: string;
  defaultReasoningEffort: ReasoningEffort | "";
  defaultImageModel: string;
  defaultImageAttempts: number;
  onCreated: (projectID: string) => void;
  onAdvancedMode: () => void;
};

const styles = [
  ["finance_documentary", "财经纪实插画"],
  ["red_ink", "赤墨风"],
  ["old_newspaper", "旧报档案风"],
  ["ledger_investigation", "账本调查风"],
  ["dark_crisis", "暗黑危机风"],
  ["city_era", "城市时代感"],
  ["blackboard", "黑板讲解风"],
  ["custom", "自定义风格"],
] as const;

const ratios: ImageProject["ratio"][] = ["3:4", "4:3", "9:16", "1:1"];
const concurrencyOptions = Array.from({ length: 18 }, (_, index) => index + 1);
const attemptOptions = [1, 2, 3, 4];
export const DEFAULT_IMAGE_TEXT_MODEL = "gpt-5.6-sol";
export const DEFAULT_IMAGE_MODEL = "gpt-image-2";

export function resolveImageTextModel(value?: string) {
  const trimmed = value?.trim() ?? "";
  return trimmed || DEFAULT_IMAGE_TEXT_MODEL;
}

export function resolveImageModel(value?: string) {
  const trimmed = value?.trim() ?? "";
  return trimmed || DEFAULT_IMAGE_MODEL;
}

function clamp(value: number, min: number, max: number, fallback: number) {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(max, Math.max(min, value));
}

export function QuickGenerateForm({
  api,
  defaultRatio,
  defaultStyle,
  defaultConcurrency,
  defaultTextModel,
  defaultReasoningEffort,
  defaultImageModel,
  defaultImageAttempts,
  onCreated,
  onAdvancedMode,
}: QuickGenerateFormProps) {
  const [script, setScript] = useState(() => { try { return sessionStorage.getItem("console:image-quick-script") || ""; } catch { return ""; } });
  useEffect(() => { try { if (script) sessionStorage.setItem("console:image-quick-script", script); else sessionStorage.removeItem("console:image-quick-script"); } catch { /* Keep editing available without storage. */ } }, [script]);
  const [ratio, setRatio] = useState<ImageProject["ratio"]>(defaultRatio);
  const [style, setStyle] = useState(defaultStyle);
  const [customStyle, setCustomStyle] = useState("");
  const [concurrency, setConcurrency] = useState(clamp(defaultConcurrency, 1, 18, 3));
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort | "">(defaultReasoningEffort);
  const [imageAttempts, setImageAttempts] = useState(clamp(defaultImageAttempts, 1, 4, 2));
  const [textModel, setTextModel] = useState(resolveImageTextModel(defaultTextModel));
  const [imageModel, setImageModel] = useState(resolveImageModel(defaultImageModel));
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const canSubmit = Boolean(script.trim()) && (style !== "custom" || Boolean(customStyle.trim())) && !busy;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canSubmit) return;
    const payload: QuickImageProjectRequest = {
      script,
      image_count: 0,
      ratio,
      style,
      custom_style: customStyle.trim(),
      concurrency: clamp(concurrency, 1, 18, 3),
      text_model: resolveImageTextModel(textModel),
      reasoning_effort: reasoningEffort,
      image_model: resolveImageModel(imageModel),
      image_attempts: clamp(imageAttempts, 1, 4, 2),
    };
    setBusy(true);
    setError("");
    try {
      const response = await api("/api/image-projects/quick-generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const next = await response.json() as QuickImageProjectResponse & { code?: string; message?: string };
      if (!response.ok) {
        setError(next.message?.trim() || "请求未能完成");
        return;
      }
      if (!next.project_id) {
        setError("未返回项目编号");
        return;
      }
      setScript("");
      try { sessionStorage.removeItem("console:image-quick-script"); } catch { /* Storage may be disabled. */ }
      onCreated(next.project_id);
    } catch {
      setError("网络中断，请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="quick-generate-form" onSubmit={(event) => void submit(event)}>
      <section className="quick-generate-script">
        <header className="quick-generate-script__head">
          <p className="image-kicker">一键生成</p>
          <h2>把定稿文案放进来</h2>
          <p>系统按原文出图，不二创。项目名称会在分析文案时生成。</p>
        </header>
        <label>
          最终文案
          <textarea
            rows={16}
            value={script}
            onChange={(event) => setScript(event.target.value)}
            placeholder="例如：钱去哪了。银行流水里藏着家庭真正的现金流。"
          />
        </label>
      </section>
      <aside className="quick-generate-rail">
        <p className="image-kicker">日常参数</p>
        <label>
          文本模型
          <ModelSelect
            aria-label="文本模型"
            value={textModel}
            onChange={setTextModel}
          />
        </label>
        <label>
          图片模型
          <input
            aria-label="图片模型"
            value={imageModel}
            onChange={(event) => setImageModel(event.target.value)}
            placeholder={DEFAULT_IMAGE_MODEL}
            maxLength={128}
          />
        </label>
        <label>
          思考强度
          <select aria-label="思考强度" value={reasoningEffort} onChange={(event) => setReasoningEffort(event.target.value as ReasoningEffort | "")}>
            <option value="">不设置</option>
            {reasoningEfforts.map((value) => <option value={value} key={value}>{value}</option>)}
          </select>
        </label>
        <label>
          图片比例
          <select aria-label="图片比例" value={ratio} onChange={(event) => setRatio(event.target.value as ImageProject["ratio"])}>
            {ratios.map((value) => <option value={value} key={value}>{value}</option>)}
          </select>
        </label>
        <label>
          视觉风格
          <select aria-label="视觉风格" value={style} onChange={(event) => setStyle(event.target.value)}>
            {styles.map(([value, label]) => <option value={value} key={value}>{label}</option>)}
          </select>
        </label>
        {style === "custom" ? (
          <label>
            自定义风格
            <textarea rows={4} value={customStyle} onChange={(event) => setCustomStyle(event.target.value)} placeholder="描述画面材质、光线、色彩与构图" />
          </label>
        ) : null}
        <label>
          项目并发
          <select aria-label="项目并发" value={concurrency} onChange={(event) => setConcurrency(Number(event.target.value))}>
            {concurrencyOptions.map((value) => <option value={value} key={value}>{value}</option>)}
          </select>
        </label>
        <button type="button" className="quick-advanced-toggle" aria-expanded={advancedOpen} onClick={() => setAdvancedOpen((open) => !open)}>
          <ChevronDown size={16} aria-hidden="true" />
          高级参数
        </button>
        {advancedOpen ? (
          <label>
            每张图片最多请求次数
            <select aria-label="每张图片最多请求次数" value={imageAttempts} onChange={(event) => setImageAttempts(Number(event.target.value))}>
              {attemptOptions.map((value) => <option value={value} key={value}>{value}</option>)}
            </select>
          </label>
        ) : null}
        <div className="quick-generate-actions">
          {error ? <p className="image-mode-notice" role="alert">提交失败：{error}</p> : null}
          <button type="submit" className="primary" disabled={!canSubmit}>{busy ? "正在提交…" : "开始生成图片"}</button>
          <button type="button" className="quick-advanced-link" onClick={onAdvancedMode}>
            <PenLine size={16} aria-hidden="true" />
            高级手动模式
          </button>
        </div>
      </aside>
    </form>
  );
}
