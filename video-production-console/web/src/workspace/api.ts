export type WorkspaceApi = (path: string, init?: RequestInit) => Promise<Response>;

export type WorkspaceEntry = {
  path: string;
  name: string;
  kind: "file" | "dir";
  mtime?: string;
  children?: WorkspaceEntry[];
};

export type WorkspaceTree = { root: string; entries: WorkspaceEntry[] };

export type WorkspaceFile = {
  path: string;
  content: string;
  mtime: string;
  spoken_body: string;
};

async function readError(response: Response, fallback: string): Promise<string> {
  try {
    const body = (await response.json()) as { message?: string };
    if (body.message) return body.message;
  } catch {
    /* keep fallback */
  }
  return fallback;
}

export async function fetchWorkspaceTree(api: WorkspaceApi): Promise<WorkspaceTree> {
  const response = await api("/api/workspace/tree");
  if (!response.ok) throw new Error(await readError(response, "工作区目录读取失败。"));
  return response.json();
}

export async function fetchWorkspaceFile(api: WorkspaceApi, path: string): Promise<WorkspaceFile> {
  const response = await api(`/api/workspace/file?path=${encodeURIComponent(path)}`);
  if (!response.ok) throw new Error(await readError(response, "文件读取失败。"));
  return response.json();
}

export async function saveWorkspaceFile(api: WorkspaceApi, path: string, content: string): Promise<WorkspaceFile> {
  const response = await api("/api/workspace/file", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path, content }),
  });
  if (!response.ok) throw new Error(await readError(response, "保存失败。"));
  return response.json();
}

export function flattenFiles(entries: WorkspaceEntry[]): WorkspaceEntry[] {
  const out: WorkspaceEntry[] = [];
  for (const entry of entries) {
    if (entry.kind === "file") out.push(entry);
    if (entry.children?.length) out.push(...flattenFiles(entry.children));
  }
  return out;
}

export function defaultWorkspaceFile(tree: WorkspaceTree): string {
  const files = flattenFiles(tree.entries);
  const drafts = files.filter((item) => item.name.startsWith("成稿_"));
  drafts.sort((a, b) => (b.mtime ?? "").localeCompare(a.mtime ?? ""));
  if (drafts[0]) return drafts[0].path;
  return files.find((item) => item.path === "agent.md")?.path ?? files[0]?.path ?? "";
}
