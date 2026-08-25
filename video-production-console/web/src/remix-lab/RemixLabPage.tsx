import { useCallback, useEffect, useState } from "react";

export type PromptTemplate = {
  id: string;
  name: string;
  description: string;
  style: string;
  stamp: string;
  system: string;
  user: string;
  builtin: boolean;
};

type ActiveResponse = {
  active: boolean;
  prompt: {
    id: string;
    name: string;
    stamp: string;
    style: string;
    system?: string;
    user?: string;
  };
};

type Props = {
  onClose: () => void;
};

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
    ...init,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.message || `请求失败 ${res.status}`);
  }
  return res.json();
}

export function RemixLabPage({ onClose }: Props) {
  const [prompts, setPrompts] = useState<PromptTemplate[]>([]);
  const [selectedId, setSelectedId] = useState("elder_stable");
  const [system, setSystem] = useState("");
  const [user, setUser] = useState("");
  const [active, setActive] = useState<ActiveResponse | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const [list, act] = await Promise.all([
      api<{ prompts: PromptTemplate[] }>("/api/remix-lab/prompts"),
      api<ActiveResponse>("/api/remix-lab/active-prompt"),
    ]);
    setPrompts(list.prompts || []);
    setActive(act);
    const prefer = act.active && act.prompt?.id ? act.prompt.id : "elder_stable";
    setSelectedId(prefer);
    const match = (list.prompts || []).find((p) => p.id === prefer);
    if (act.active && (act.prompt.system || act.prompt.user)) {
      setSystem(act.prompt.system || "");
      setUser(act.prompt.user || "");
    } else if (match) {
      setSystem(match.system || "");
      setUser(match.user || "");
    }
  }, []);

  useEffect(() => {
    load().catch((err) => setMessage(String(err.message || err)));
  }, [load]);

  const onSelect = (id: string) => {
    setSelectedId(id);
    const match = prompts.find((p) => p.id === id);
    if (match) {
      setSystem(match.system || "");
      setUser(match.user || "");
    }
    setMessage("");
  };

  const adopt = async () => {
    setBusy(true);
    setMessage("");
    try {
      const res = await api<ActiveResponse>("/api/remix-lab/active-prompt", {
        method: "PUT",
        body: JSON.stringify({ id: selectedId, system, user }),
      });
      setActive(res);
      setMessage(
        res.active
          ? `已采用「${res.prompt.name}」为全局系统提示词。后续二创任务会注入这份提示词。`
          : `已恢复内置「${res.prompt.name}」。`,
      );
    } catch (err) {
      setMessage(String((err as Error).message || err));
    } finally {
      setBusy(false);
    }
  };

  const restoreDefault = async () => {
    setBusy(true);
    setMessage("");
    try {
      const res = await api<ActiveResponse>("/api/remix-lab/active-prompt", {
        method: "PUT",
        body: JSON.stringify({ clear: true }),
      });
      setActive(res);
      onSelect("elder_stable");
      setMessage("已恢复生产默认：中老年定稿。");
    } catch (err) {
      setMessage(String((err as Error).message || err));
    } finally {
      setBusy(false);
    }
  };

  const current = prompts.find((p) => p.id === selectedId);

  return (
    <div className="remix-lab" role="dialog" aria-labelledby="remix-lab-title">
      <header className="remix-lab-header">
        <div>
          <span className="eyebrow">文案进化台</span>
          <h1 id="remix-lab-title">二创提示词试验场</h1>
          <p className="muted">
            选择策略 → 可直接改 system / user → 采用后作为全局系统提示词，后续二创都用它。
            测试请回到项目里跑一条二创对照效果。
          </p>
        </div>
        <button type="button" className="header-button" onClick={onClose}>
          返回
        </button>
      </header>

      {message ? (
        <div className="notice notice--info" role="status">
          {message}
        </div>
      ) : null}

      {active ? (
        <div className="remix-lab-active muted">
          当前全局：
          <strong>
            {active.active ? active.prompt.name : `${active.prompt.name}（代码内置）`}
          </strong>
          {active.prompt.stamp ? ` · ${active.prompt.stamp}` : ""}
        </div>
      ) : null}

      <div className="remix-lab-layout">
        <aside className="remix-lab-catalog">
          <h2>提示词目录</h2>
          <ul>
            {prompts.map((p) => (
              <li key={p.id}>
                <button
                  type="button"
                  className={p.id === selectedId ? "is-selected" : undefined}
                  onClick={() => onSelect(p.id)}
                >
                  <strong>{p.name}</strong>
                  <small>{p.description}</small>
                </button>
              </li>
            ))}
          </ul>
        </aside>

        <section className="remix-lab-editor">
          <div className="remix-lab-meta">
            <span>ID: {current?.id}</span>
            <span>style: {current?.style}</span>
            <span>stamp: {current?.stamp}</span>
          </div>
          <label>
            System 提示词
            <textarea
              value={system}
              onChange={(e) => setSystem(e.target.value)}
              rows={18}
              spellCheck={false}
            />
          </label>
          <label>
            User 模板（可用 {"{{SOURCE}}"} {"{{NOTES}}"}）
            <textarea
              value={user}
              onChange={(e) => setUser(e.target.value)}
              rows={10}
              spellCheck={false}
            />
          </label>
          <div className="remix-lab-actions">
            <button type="button" className="primary" disabled={busy} onClick={adopt}>
              设为全局系统提示词
            </button>
            <button type="button" disabled={busy} onClick={restoreDefault}>
              恢复中老年定稿
            </button>
          </div>
        </section>
      </div>
    </div>
  );
}
