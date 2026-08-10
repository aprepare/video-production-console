import { useQuery } from "@tanstack/react-query";

export type RuntimeStatus = {
  Limit: number;
  Running: number;
  Queued: number;
};

type Request = (path: string, init?: RequestInit) => Promise<Response>;

export function useRuntimeQuery(request: Request, enabled: boolean) {
  return useQuery({
    queryKey: ["runtime"],
    enabled,
    refetchInterval: 7_000,
    queryFn: async ({ signal }) => {
      const response = await request("/api/runtime", { signal });
      if (!response.ok) throw new Error("Runtime status could not be read.");
      return (await response.json()) as RuntimeStatus;
    },
  });
}
