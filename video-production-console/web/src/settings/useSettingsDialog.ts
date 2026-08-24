import { useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../query/keys";
import type { PublicSettings, Settings } from "../types";
import { withMontageStyleDefaults } from "./montageStyle";

const emptySecretDraft = {
  grok_api_key: "",
  remix_api_key: "",
  copy_api_key: "",
  pexels_api_key: "",
  volc_speech_api_key: "",
  aurastd_tts_api_key: "",
  image_api_key: "",
  image_text_api_key: "",
  vision_api_key: "",
  embedding_api_key: "",
  pixabay_api_key: "",
};

type SettingsDialogOptions = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  readSettings: (signal?: AbortSignal) => Promise<Settings>;
  setMessage: (message: string) => void;
};

export function useSettingsDialog({ api, readSettings, setMessage }: SettingsDialogOptions) {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [draft, setDraft] = useState<PublicSettings | null>(null);
  const [secretDraft, setSecretDraft] = useState(emptySecretDraft);

  const openDialog = async () => {
    try {
      // staleTime 0 keeps the dialog's always-refetch-on-open behaviour.
      const next = await client.fetchQuery({
        queryKey: queryKeys.settings(),
        queryFn: ({ signal }) => readSettings(signal),
        staleTime: 0,
      });
      setDraft({ ...next.public });
      setSecretDraft({ ...emptySecretDraft });
      setFeedback("");
      setOpen(true);
    } catch {
      setMessage("设置读取失败。");
    }
  };

  const submit = async (): Promise<boolean> => {
    if (!draft) return false;
    // The backend omits zero-value montage_style fields; always send the
    // complete object so defaults survive the round trip.
    const publicDraft: PublicSettings = {
      ...draft,
      montage_style: withMontageStyleDefaults(draft.montage_style),
    };
    const response = await api("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public: publicDraft, secrets: secretDraft }),
    });
    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        setFeedback("登录已失效，请刷新后重试。");
        return false;
      }
      try {
        const payload = (await response.json()) as { message?: string };
        setFeedback(payload.message?.trim() || "设置保存失败，请检查填写内容。");
      } catch {
        setFeedback("设置保存失败，请检查填写内容。");
      }
      return false;
    }
    const next = (await response.json()) as Settings;
    client.setQueryData(queryKeys.settings(), next);
    setDraft({ ...next.public });
    setSecretDraft({ ...emptySecretDraft });
    setFeedback("设置已保存。");
    return true;
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    await submit();
  };

  // Saves first, then asks the backend to restart and reloads the page once
  // the replacement process answers the health probe.
  const saveAndRestart = async () => {
    if (!(await submit())) return;
    const response = await api("/api/system/restart", { method: "POST" });
    if (!response.ok) {
      setFeedback("设置已保存，但重启请求失败，请手动重启控制台。");
      return;
    }
    setFeedback("控制台正在重启，页面稍后自动刷新…");
    const deadline = Date.now() + 60_000;
    // Give the old process a moment to release the port before probing.
    await new Promise((resolve) => setTimeout(resolve, 2000));
    while (Date.now() < deadline) {
      try {
        const health = await fetch("/api/health", { cache: "no-store" });
        if (health.ok) {
          window.location.reload();
          return;
        }
      } catch {
        /* the server is still swapping over */
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    setFeedback("重启超时，请手动刷新页面或检查控制台进程。");
  };

  const close = () => {
    setFeedback("");
    setOpen(false);
  };

  return {
    open,
    setOpen,
    feedback,
    draft,
    setDraft,
    secretDraft,
    setSecretDraft,
    openDialog,
    save,
    saveAndRestart,
    close,
  };
}
