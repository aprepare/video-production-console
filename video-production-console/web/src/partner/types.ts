export type PartnerEdition = "owner" | "partner";

export type PartnerState = "needs_activation" | "verifying" | "ready" | "locked";

export type PartnerCapabilities = {
  features?: string[];
  text_models?: string[];
  reasoning_efforts?: string[];
  image_model?: string;
};

export type PartnerSnapshot = {
  state: PartnerState;
  partner_name?: string;
  capabilities?: PartnerCapabilities;
  session_expires_at?: string;
  error_code?: string;
};

export type PartnerSettingsView = {
  text_models?: string[];
  reasoning_efforts?: string[];
  image_model?: string;
  aura_model?: string;
  aura_voice_id?: string;
  media_root?: string;
  jianying_root?: string;
  machine_profile_path?: string;
  default_image_ratio?: string;
  default_image_style?: string;
  aura_speed?: number;
  aura_volume?: number;
};

export type PartnerSettingsUpdate = {
  media_root?: string;
  jianying_root?: string;
  machine_profile_path?: string;
  default_image_ratio?: string;
  default_image_style?: string;
  aura_speed?: number;
  aura_volume?: number;
};

export const partnerErrorMessages = {
  gateway_unavailable: "授权服务暂时不可用，请稍后重试。",
  authorization_failed: "授权已失效，请重新输入伙伴密钥。",
  invalid_credential: "伙伴密钥不正确。",
  device_mismatch: "该密钥已绑定其他电脑。",
  partner_disabled: "当前账号已停用。",
  client_too_old: "软件版本过低，请获取新版。",
} as const;

export type PartnerErrorCode = keyof typeof partnerErrorMessages;
