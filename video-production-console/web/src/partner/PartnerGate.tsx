import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { activatePartner, fetchPartnerStatus, PartnerActivateError, partnerErrorMessage } from "./api";
import { fetchPartnerSetup, SetupWizard, type PartnerSetupStatus } from "./SetupWizard";
import type { PartnerSnapshot } from "./types";

const PartnerSessionContext = createContext<PartnerSnapshot | null>(null);

export function usePartnerSession() {
  return useContext(PartnerSessionContext);
}

export function PartnerGate({ children }: { children: ReactNode }) {
  const [snapshot, setSnapshot] = useState<PartnerSnapshot | null>(null);
  const [setup, setSetup] = useState<PartnerSetupStatus | null>(null);
  const [activationKey, setActivationKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [errorCode, setErrorCode] = useState("");

  const loadStatus = useCallback(async () => {
    try {
      const next = await fetchPartnerStatus();
      setSnapshot(next);
      setErrorCode(next.error_code || "");
    } catch {
      setSnapshot({ state: "needs_activation", error_code: "gateway_unavailable" });
      setErrorCode("gateway_unavailable");
    }
  }, []);

  useEffect(() => {
    void loadStatus();
  }, [loadStatus]);

  useEffect(() => {
    if (snapshot?.state !== "ready") {
      setSetup(null);
      return;
    }
    let cancelled = false;
    void fetchPartnerSetup()
      .then((next) => {
        if (!cancelled) setSetup(next);
      })
      .catch(() => {
        if (!cancelled) setSetup({ complete: false });
      });
    return () => {
      cancelled = true;
    };
  }, [snapshot]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const key = activationKey;
    setSubmitting(true);
    try {
      const next = await activatePartner(key);
      setSnapshot(next);
      setErrorCode(next.error_code || "");
    } catch (error) {
      setErrorCode(error instanceof PartnerActivateError ? error.code : "gateway_unavailable");
    } finally {
      setActivationKey("");
      setSubmitting(false);
    }
  };

  if (!snapshot || snapshot.state === "verifying") {
    return <div className="splash">正在验证伙伴授权…</div>;
  }

  if (snapshot.state === "ready") {
    if (!setup) {
      return <div className="splash">正在检查本机配置…</div>;
    }
    if (!setup.complete) {
      return (
        <SetupWizard
          status={setup}
          onComplete={() => setSetup({ complete: true, detected_jianying_root: setup.detected_jianying_root })}
        />
      );
    }
    return (
      <PartnerSessionContext.Provider value={snapshot}>{children}</PartnerSessionContext.Provider>
    );
  }

  if (snapshot.state === "locked") {
    return (
      <main className="login-page">
        <div className="login-card">
          <span className="eyebrow">伙伴授权</span>
          <h1>当前无法使用</h1>
          <p className="notice" role="alert">
            {partnerErrorMessage(snapshot.error_code || errorCode)}
          </p>
          <button type="button" onClick={() => void loadStatus()}>
            重试
          </button>
        </div>
      </main>
    );
  }

  return (
    <main className="login-page">
      <form className="login-card" onSubmit={(event) => void submit(event)}>
        <span className="eyebrow">伙伴授权</span>
        <h1>激活伙伴软件</h1>
        <p>请输入伙伴密钥后继续。</p>
        {errorCode ? (
          <div className="notice" role="alert">
            {partnerErrorMessage(errorCode)}
          </div>
        ) : null}
        <label htmlFor="partner-activation-key">伙伴密钥</label>
        <input
          id="partner-activation-key"
          type="password"
          value={activationKey}
          autoComplete="off"
          autoFocus
          disabled={submitting}
          onChange={(event) => setActivationKey(event.target.value)}
        />
        <button type="submit" disabled={submitting}>
          激活
        </button>
      </form>
    </main>
  );
}
