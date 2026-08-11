import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../query/keys";

type Request = (path: string, init?: RequestInit) => Promise<Response>;
type Updater<T> = T[] | ((current: T[]) => T[]);

// The console lists are fetched through react-query so concurrent callers share
// one request and in-flight fetches are cancelled on unmount. The hook keeps its
// original imperative surface (setAccounts/setProjects/reload) so callers that
// apply optimistic updates continue to work; those writes go to the query cache.
export function useConsoleData<Account, Project>(request: Request, enabled = true) {
  const client = useQueryClient();

  const read = useCallback(
    async <T,>(path: string, signal?: AbortSignal): Promise<T> => {
      const response = await request(path, { signal });
      if (!response.ok) throw new Error("读取控制台数据失败");
      return (await response.json()) as T;
    },
    [request],
  );

  const accountsQuery = useQuery({
    queryKey: queryKeys.accounts(),
    enabled,
    queryFn: ({ signal }) => read<Account[]>("/api/accounts", signal),
  });
  const projectsQuery = useQuery({
    queryKey: queryKeys.projects(),
    enabled,
    queryFn: ({ signal }) => read<Project[]>("/api/projects", signal),
  });

  const setAccounts = useCallback(
    (updater: Updater<Account>) => {
      client.setQueryData<Account[]>(queryKeys.accounts(), (current) =>
        typeof updater === "function" ? updater(current ?? []) : updater,
      );
    },
    [client],
  );
  const setProjects = useCallback(
    (updater: Updater<Project>) => {
      client.setQueryData<Project[]>(queryKeys.projects(), (current) =>
        typeof updater === "function" ? updater(current ?? []) : updater,
      );
    },
    [client],
  );

  // Callers surface load failures themselves, so keep rejecting like the
  // previous hand-rolled reload instead of swallowing the error into state.
  const reload = useCallback(async () => {
    await Promise.all([
      client.fetchQuery({
        queryKey: queryKeys.accounts(),
        queryFn: ({ signal }) => read<Account[]>("/api/accounts", signal),
      }),
      client.fetchQuery({
        queryKey: queryKeys.projects(),
        queryFn: ({ signal }) => read<Project[]>("/api/projects", signal),
      }),
    ]);
  }, [client, read]);

  return {
    accounts: accountsQuery.data ?? [],
    setAccounts,
    projects: projectsQuery.data ?? [],
    setProjects,
    loading: accountsQuery.isPending || projectsQuery.isPending,
    reload,
  };
}
