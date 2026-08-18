// Media catalog API client. The type literals below are pinned against the Go
// response structs by internal/httpapi/contract_types_test.go, so renames on
// either side fail a test instead of silently reading undefined.

export type MediaCatalogCounts = {
  sources: number;
  shots: number;
  ready_shots: number;
  failed_shots: number;
};

export type MediaCatalogActiveJob = {
  id: string;
  phase: string;
  completed_units: number;
  total_units: number;
};

export type MediaCatalogWarning = {
  code: string;
  count: number;
};

export type MediaCatalogStatus = {
  state: "idle" | "scanning" | "extracting" | "analyzing" | "ready" | "degraded" | "failed";
  counts: MediaCatalogCounts;
  active_job?: MediaCatalogActiveJob | null;
  warnings: MediaCatalogWarning[];
};

export type MediaCatalogSource = {
  id: string;
  kind: "movie" | "broll" | "image";
  subtype: string;
  origin: string;
  relative_path: string;
  status: "pending_probe" | "ready" | "failed";
  error_code: string;
  size_bytes: number;
  duration_ms: number;
  updated_at: string;
};

export type MediaCatalogSourcesPage = {
  sources: MediaCatalogSource[];
  next_cursor?: string;
};

export type MediaCatalogSourceFilter = {
  kind?: string;
  status?: string;
  cursor?: string;
};

export type MediaCatalogProviderStatus = {
  name: string;
  configured: boolean;
};

export type MediaCatalogProviders = {
  providers: MediaCatalogProviderStatus[];
};

export type MediaCatalogRemoteAsset = {
  provider: string;
  id: string;
  kind: string;
  download_url: string;
  page_url: string;
  creator: string;
  license_code: string;
  license_url: string;
  width: number;
  height: number;
};

export type MediaCatalogSearchPage = {
  assets: MediaCatalogRemoteAsset[];
};

export type MediaCatalogImportResult = {
  source: MediaCatalogSource;
  created: boolean;
  publishability: string;
};

type API = (path: string, init?: RequestInit) => Promise<Response>;

export class MediaCatalogError extends Error {
  code: string;

  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

async function readJSONOrThrow<T>(response: Response): Promise<T> {
  if (response.ok) return (await response.json()) as T;
  let code = "catalog_request_failed";
  let message = "素材库请求失败。";
  try {
    const payload = (await response.json()) as { code?: string; message?: string };
    if (payload.code) code = payload.code;
    if (payload.message) message = payload.message;
  } catch {
    // Keep the fallback code when the body is not JSON.
  }
  throw new MediaCatalogError(code, message);
}

export function fetchCatalogStatus(api: API): Promise<MediaCatalogStatus> {
  return api("/api/media-catalog/status").then((response) =>
    readJSONOrThrow<MediaCatalogStatus>(response),
  );
}

export function fetchCatalogSources(
  api: API,
  filter: MediaCatalogSourceFilter = {},
): Promise<MediaCatalogSourcesPage> {
  const query = new URLSearchParams();
  if (filter.kind) query.set("kind", filter.kind);
  if (filter.status) query.set("status", filter.status);
  if (filter.cursor) query.set("cursor", filter.cursor);
  const suffix = query.toString() ? `?${query.toString()}` : "";
  return api(`/api/media-catalog/sources${suffix}`).then((response) =>
    readJSONOrThrow<MediaCatalogSourcesPage>(response),
  );
}

export function startCatalogIndex(api: API): Promise<MediaCatalogStatus> {
  return api("/api/media-catalog/index", { method: "POST" }).then((response) =>
    readJSONOrThrow<MediaCatalogStatus>(response),
  );
}

export function retryCatalogSource(api: API, sourceID: string): Promise<MediaCatalogStatus> {
  return api(`/api/media-catalog/sources/${encodeURIComponent(sourceID)}/retry`, {
    method: "POST",
  }).then((response) => readJSONOrThrow<MediaCatalogStatus>(response));
}

export function fetchCatalogProviders(api: API): Promise<MediaCatalogProviders> {
  return api("/api/media-catalog/providers").then((response) =>
    readJSONOrThrow<MediaCatalogProviders>(response),
  );
}

export function searchCatalogAssets(
  api: API,
  query: { provider: string; q: string; limit?: number },
): Promise<MediaCatalogSearchPage> {
  const params = new URLSearchParams();
  params.set("provider", query.provider);
  params.set("q", query.q);
  if (query.limit != null) params.set("limit", String(query.limit));
  return api(`/api/media-catalog/search?${params.toString()}`).then((response) =>
    readJSONOrThrow<MediaCatalogSearchPage>(response),
  );
}

export function importCatalogRemote(
  api: API,
  asset: MediaCatalogRemoteAsset,
): Promise<MediaCatalogImportResult> {
  return api("/api/media-catalog/import", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ source: "remote", ...asset }),
  }).then((response) => readJSONOrThrow<MediaCatalogImportResult>(response));
}

export function importCatalogLocal(
  api: API,
  file: File,
  kind: string,
): Promise<MediaCatalogImportResult> {
  const body = new FormData();
  body.set("kind", kind);
  body.set("file", file);
  return api("/api/media-catalog/import", { method: "POST", body }).then((response) =>
    readJSONOrThrow<MediaCatalogImportResult>(response),
  );
}
