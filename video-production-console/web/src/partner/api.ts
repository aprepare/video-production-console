import {
  partnerErrorMessages,
  type PartnerErrorCode,
  type PartnerSnapshot,
} from "./types";

export function partnerErrorMessage(code: string | undefined): string {
  if (code && code in partnerErrorMessages) {
    return partnerErrorMessages[code as PartnerErrorCode];
  }
  return partnerErrorMessages.gateway_unavailable;
}

export class PartnerActivateError extends Error {
  readonly code: string;

  constructor(code: string) {
    super(partnerErrorMessage(code));
    this.name = "PartnerActivateError";
    this.code = code && code in partnerErrorMessages ? code : "gateway_unavailable";
  }
}

function readErrorCode(payload: unknown): string {
  if (!payload || typeof payload !== "object") return "gateway_unavailable";
  const code = (payload as { code?: unknown }).code;
  return typeof code === "string" ? code : "gateway_unavailable";
}

export async function fetchPartnerStatus(signal?: AbortSignal): Promise<PartnerSnapshot> {
  const response = await fetch("/api/partner/status", {
    credentials: "same-origin",
    signal,
  });
  if (!response.ok) throw new PartnerActivateError("gateway_unavailable");
  return (await response.json()) as PartnerSnapshot;
}

export async function activatePartner(activationKey: string): Promise<PartnerSnapshot> {
  const response = await fetch("/api/partner/activate", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ activation_key: activationKey }),
  });
  let payload: unknown = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (!response.ok) throw new PartnerActivateError(readErrorCode(payload));
  return payload as PartnerSnapshot;
}
