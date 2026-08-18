export type ApiRequestOptions = {
  csrfToken?: string;
  onUnauthorized?: () => void;
};

export async function apiRequest(
  path: string,
  init: RequestInit = {},
  options: ApiRequestOptions = {},
): Promise<Response> {
  const method = (init.method || "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (!["GET", "HEAD", "OPTIONS"].includes(method) && options.csrfToken) {
    headers.set("X-CSRF-Token", options.csrfToken);
  }
  const response = await globalThis.fetch(path, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  if (response.status === 401) options.onUnauthorized?.();
  return response;
}
