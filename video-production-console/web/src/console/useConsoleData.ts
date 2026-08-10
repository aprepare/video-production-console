import { useCallback, useState } from "react";

type Request = (path: string) => Promise<Response>;

export function useConsoleData<Account, Project>(request: Request) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const [a, p] = await Promise.all([
        request("/api/accounts"),
        request("/api/projects"),
      ]);
      if (!a.ok || !p.ok) throw new Error("读取控制台数据失败");
      setAccounts((await a.json()) as Account[]);
      setProjects((await p.json()) as Project[]);
    } finally {
      setLoading(false);
    }
  }, [request]);

  return { accounts, setAccounts, projects, setProjects, loading, reload };
}
