import { useState, type FormEvent } from "react";

export type PartnerSetupStatus = {
  complete: boolean;
  detected_jianying_root?: string;
  codes?: string[];
};

export async function fetchPartnerSetup(signal?: AbortSignal): Promise<PartnerSetupStatus> {
  const response = await fetch("/api/partner/setup", { credentials: "same-origin", signal });
  if (!response.ok) throw new Error("setup_unavailable");
  return (await response.json()) as PartnerSetupStatus;
}

export async function submitPartnerSetup(jianyingRoot: string, mediaRoot: string): Promise<PartnerSetupStatus> {
  const response = await fetch("/api/partner/setup", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jianying_root: jianyingRoot, media_root: mediaRoot }),
  });
  let payload: PartnerSetupStatus = { complete: false };
  try {
    payload = (await response.json()) as PartnerSetupStatus;
  } catch {
    payload = { complete: false };
  }
  if (!response.ok) throw new Error(payload.codes?.[0] || "invalid_setup");
  return payload;
}

export function SetupWizard({
  status,
  onComplete,
}: {
  status: PartnerSetupStatus;
  onComplete: () => void;
}) {
  const [jianyingRoot, setJianyingRoot] = useState(status.detected_jianying_root || "");
  const [mediaRoot, setMediaRoot] = useState("");
  const [indexing, setIndexing] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    setIndexing(true);
    try {
      await submitPartnerSetup(jianyingRoot, mediaRoot);
      onComplete();
    } catch {
      setError("目录检查未通过，请确认剪映草稿和风景素材路径。");
      setIndexing(false);
    }
  };

  if (indexing) {
    return (
      <main className="login-page">
        <div className="login-card">
          <span className="eyebrow">本机配置</span>
          <h1>正在建立本机素材索引</h1>
          <p>正在检查剪映草稿目录并扫描风景素材，完成后会自动重启。</p>
        </div>
      </main>
    );
  }

  return (
    <main className="login-page">
      <form className="login-card" onSubmit={(event) => void submit(event)}>
        <span className="eyebrow">本机配置</span>
        <h1>选择本机目录</h1>
        <p>请指定剪映草稿目录和单独收到的风景素材目录。不会复制素材文件，也不接受素材库数据库路径。</p>
        {error ? (
          <div className="notice" role="alert">
            {error}
          </div>
        ) : null}
        <label htmlFor="partner-jianying-root">剪映草稿目录</label>
        <input
          id="partner-jianying-root"
          value={jianyingRoot}
          autoComplete="off"
          onChange={(event) => setJianyingRoot(event.target.value)}
        />
        <label htmlFor="partner-media-root">风景素材目录</label>
        <input
          id="partner-media-root"
          value={mediaRoot}
          autoComplete="off"
          onChange={(event) => setMediaRoot(event.target.value)}
        />
        <button type="submit" disabled={!jianyingRoot.trim() || !mediaRoot.trim()}>
          检查并继续
        </button>
      </form>
    </main>
  );
}
