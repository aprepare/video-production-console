// Central query key factory. Keys are declared here so cache invalidation stays
// consistent as more of App.tsx moves off hand-rolled fetch effects.
export const queryKeys = {
  accounts: () => ["accounts"] as const,
  projects: () => ["projects"] as const,
  project: (projectID: string) => ["project", projectID] as const,
  tasks: (projectID: string) => ["tasks", projectID] as const,
  task: (taskID: string) => ["task", taskID] as const,
  runtime: () => ["runtime"] as const,
  settings: () => ["settings"] as const,
};
