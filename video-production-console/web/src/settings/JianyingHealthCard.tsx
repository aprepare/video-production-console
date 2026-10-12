import { useCallback, useEffect, useState } from "react";
import { HardDrive, RefreshCw } from "lucide-react";

type Api = (path: string, init?: RequestInit) => Promise<Response>;

export type JianyingHealth = {
  jianying_root: string;
  root_exists: boolean;
  installed_version: string;
  version_source: string;
  version_note: string;
  verified_versions: string[];
  compatible: boolean;
  backup_count: number;
  backup_bytes: number;
};

type PruneResult = { deleted: Array<{ name: string; size: number }>; kept: number; freed_bytes: number };

function megabytes(bytes: number) {
  return bytes >= 1 << 20 ? `${(bytes / (1 << 20)).toFixed(1)} MB` : `${Math.round(bytes / 1024)} KB`;
}

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

/** Jianying install check plus registration-backup cleanup (preview first). */
export function JianyingHealthCard({ api }: { api: Api }) {
  const [health, setHealth] = useState<JianyingHealth | null>(null);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<PruneResult | null>(null);
  const [busy, setBusy] = useState<"" | "load" | "preview" | "prune">("load");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    setBusy("load");
    try {
      const response = await api("/api/jianying-drafts/health");
      if (!response.ok) throw new Error(await readError(response, "剪映状态读取失败。"));
      setHealth(await response.json());
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "剪映状态读取失败。");
    } finally {
      setBusy("");
    }
  }, [api]);
  useEffect(() => { void load(); }, [load]);

  const prune = async (dryRun: boolean) => {
    setBusy(dryRun ? "preview" : "prune");
    setDone("");
    try {
      const response = await api("/api/jianying-drafts/backups/prune", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ keep_latest: 10, older_than_days: 7, dry_run: dryRun }),
      });
      if (!response.ok) throw new Error(await readError(response, "清理失败。"));
      const result = (await response.json()) as PruneResult;
      if (dryRun) {
        setPreview(result);
      } else {
        setPreview(null);
        setDone(`已删除 ${result.deleted.length} 个备份，释放 ${megabytes(result.freed_bytes)}。`);
        await load();
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "清理失败。");
    } finally {
      setBusy("");
    }
  };

  return <section className="settings-field settings-field--wide jianying-health" aria-label="剪映连接">
    <span className="jianying-health__title"><HardDrive size={15} aria-hidden="true" /> 剪映连接
      <button type="button" className="settings-inline-button" aria-label="重新检测剪映" disabled={busy === "load"} onClick={() => void load()}><RefreshCw size={13} aria-hidden="true" /></button>
    </span>
    {error ? <p className="settings-feedback settings-feedback--error" role="alert">{error}</p> : null}
    {health ? <dl className="jianying-health__facts">
      <div><dt>草稿目录</dt><dd>{health.jianying_root ? (health.root_exists ? "存在" : "不存在，请检查路径") : "未配置"}</dd></div>
      <div><dt>剪映版本</dt><dd>{health.installed_version || "未检测到"}{health.installed_version ? <span className="studio-status" data-status={health.compatible ? "ready" : "attention"}>{health.compatible ? "已验证" : `未验证（已验证 ${health.verified_versions.join("、")}）`}</span> : null}</dd></div>
      {health.version_note && !health.compatible ? <div><dt>说明</dt><dd>{health.version_note}</dd></div> : null}
      <div><dt>注册备份</dt><dd>{health.backup_count} 个 · {megabytes(health.backup_bytes)}</dd></div>
    </dl> : null}
    {health && !health.compatible && health.installed_version ? <p className="settings-feedback" role="status">剪映升级后草稿结构可能变化。生成草稿后请先在剪映里打开检查一次，再批量制作。</p> : null}
    {health && health.backup_count > 10 ? <div className="jianying-health__prune">
      {!preview
        ? <button type="button" className="settings-inline-button" disabled={Boolean(busy)} onClick={() => void prune(true)}>{busy === "preview" ? "正在检查…" : "预览清理（保留最近 10 个，删除 7 天前的）"}</button>
        : <>
          <span>{preview.deleted.length ? `将删除 ${preview.deleted.length} 个备份，释放 ${megabytes(preview.freed_bytes)}；root_meta_info.json 不会被动。` : "没有符合条件的旧备份。"}</span>
          {preview.deleted.length ? <button type="button" className="settings-inline-button settings-inline-button--danger" disabled={Boolean(busy)} onClick={() => void prune(false)}>{busy === "prune" ? "正在删除…" : "确认删除"}</button> : null}
          <button type="button" className="settings-inline-button" onClick={() => setPreview(null)}>取消</button>
        </>}
    </div> : null}
    {done ? <p className="settings-feedback" role="status">{done}</p> : null}
  </section>;
}
