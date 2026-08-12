import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../query/keys";

type Request = (path: string, init?: RequestInit) => Promise<Response>;
type Updater<T> = T[] | ((current: T[]) => T[]);

// The console lists are fetched through react-query so concurrent callers share
// one request and in-flight fetches are cancelled on unmount. setAccounts and
// setProjects remain because callers apply optimistic updates; those writes go
// to the query cache.
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

  return {
    accounts: accountsQuery.data ?? [],
    setAccounts,
    projects: projectsQuery.data ?? [],
    setProjects,
    loading: accountsQuery.isPending || projectsQuery.isPending,
    // Callers own the wording of the failure, so report only that one happened.
    failed: accountsQuery.isError || projectsQuery.isError,
  };
}
