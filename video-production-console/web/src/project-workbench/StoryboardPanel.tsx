import { useEffect, useMemo, useState } from "react";
import { Film, ImageOff } from "lucide-react";
import "./storyboard.css";

type Api = (path: string, init?: RequestInit) => Promise<Response>;

export type StoryboardShot = {
  shot_no: number;
  start_s: number;
  end_s: number;
  source_path: string;
  source_in_s?: number;
  source_out_s?: number;
  media_kind?: string;
  caption?: string;
  match_level?: string;
  match_reason?: string;
};

export type Storyboard = {
  supported: boolean;
  title?: string;
  duration_s?: number;
  shots?: StoryboardShot[];
};

const levelLabels: Record<string, string> = { exact: "直接对应", semantic: "语义相关", metaphor: "意象", neutral: "过渡", fallback: "补位" };

function clock(seconds: number) {
  const total = Math.max(0, Math.round(seconds));
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
}

function sourceName(path: string) {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

/** Repeated sources across a draft read as copy-paste to viewers and platforms. */
export function repeatedSources(shots: StoryboardShot[]) {
  const counts = new Map<string, number>();
  for (const shot of shots) counts.set(shot.source_path, (counts.get(shot.source_path) || 0) + 1);
  return new Set([...counts].filter(([, count]) => count > 1).map(([path]) => path));
}

type Props = { api: Api; taskID: string };

/** Frame strip of a montage draft built from its production plan, so shot
 * choices can be reviewed before opening Jianying. */
export function StoryboardPanel({ api, taskID }: Props) {
  const [board, setBoard] = useState<Storyboard | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [failedFrames, setFailedFrames] = useState<Set<number>>(new Set());

  useEffect(() => {
    let cancelled = false;
    setState("loading");
    void (async () => {
      try {
        const response = await api(`/api/jianying-drafts/storyboard?task_id=${encodeURIComponent(taskID)}`);
        if (!response.ok) throw new Error();
        const payload = (await response.json()) as Storyboard;
        if (!cancelled) { setBoard(payload); setState("ready"); }
      } catch {
        if (!cancelled) setState("error");
      }
    })();
    return () => { cancelled = true; };
  }, [api, taskID]);

  const shots = useMemo(() => board?.shots ?? [], [board]);
  const repeated = useMemo(() => repeatedSources(shots), [shots]);

  if (state === "loading") return <section className="storyboard" aria-label="分镜预览"><p className="storyboard__note" role="status">正在读取分镜…</p></section>;
  if (state === "error") return <section className="storyboard" aria-label="分镜预览"><p className="storyboard__note">分镜读取失败；草稿本身不受影响，可以直接在剪映里查看。</p></section>;
  if (!board?.supported || !shots.length) return <section className="storyboard" aria-label="分镜预览"><p className="storyboard__note">这份草稿是旧版计划格式，没有可预览的分镜。重做混剪后即可预览。</p></section>;

  return <section className="storyboard" aria-labelledby="storyboard-title">
    <header>
      <div>
        <p className="storyboard__kicker">STORYBOARD</p>
        <h2 id="storyboard-title"><Film size={16} aria-hidden="true" /> 分镜预览</h2>
      </div>
      <span className="storyboard__meta">{shots.length} 个镜头 · {clock(board.duration_s ?? shots[shots.length - 1].end_s)}{repeated.size ? ` · ${repeated.size} 个素材重复使用` : ""}</span>
    </header>
    <ol className="storyboard__strip">
      {shots.map((shot) => {
        const reused = repeated.has(shot.source_path);
        return <li key={shot.shot_no} className="storyboard__shot" data-reused={reused ? "true" : undefined}>
          <div className="storyboard__frame">
            {failedFrames.has(shot.shot_no)
              ? <span className="storyboard__frame-missing"><ImageOff size={18} aria-hidden="true" /></span>
              : <img loading="lazy" alt={`镜头 ${shot.shot_no}：${sourceName(shot.source_path)}`}
                  src={`/api/jianying-drafts/storyboard/frame?task_id=${encodeURIComponent(taskID)}&shot=${shot.shot_no}`}
                  onError={() => setFailedFrames((current) => new Set(current).add(shot.shot_no))} />}
            <span className="storyboard__time">{clock(shot.start_s)}</span>
            <span className="storyboard__len">{(shot.end_s - shot.start_s).toFixed(1)}s</span>
          </div>
          <div className="storyboard__caption">
            <strong>#{shot.shot_no}{shot.match_level ? <em data-level={shot.match_level}>{levelLabels[shot.match_level] || shot.match_level}</em> : null}{reused ? <em data-level="reused">重复</em> : null}</strong>
            {shot.match_reason ? <span title={shot.match_reason}>{shot.match_reason}</span> : <span title={shot.source_path}>{sourceName(shot.source_path)}</span>}
          </div>
        </li>;
      })}
    </ol>
  </section>;
}
