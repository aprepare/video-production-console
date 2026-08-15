import { useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../query/keys";
import type { PublicSettings, Settings } from "../types";

const emptySecretDraft = {
  grok_api_key: "",
  remix_api_key: "",
  pexels_api_key: "",
  volc_speech_api_key: "",
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

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!draft) return;
    const response = await api("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public: draft, secrets: secretDraft }),
    });
    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        setFeedback("登录已失效，请刷新后重试。");
        return;
      }
      try {
        const payload = (await response.json()) as { message?: string };
        setFeedback(payload.message?.trim() || "设置保存失败，请检查填写内容。");
      } catch {
        setFeedback("设置保存失败，请检查填写内容。");
      }
      return;
    }
    const next = (await response.json()) as Settings;
    client.setQueryData(queryKeys.settings(), next);
    setDraft({ ...next.public });
    setSecretDraft({ ...emptySecretDraft });
    setFeedback("设置已保存。");
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
    close,
  };
}
