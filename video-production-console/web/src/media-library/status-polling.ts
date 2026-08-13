import type { MediaCatalogStatus } from "../api/mediaCatalog";

// The status query only polls while a build is actually running: no active
// job means nothing changes, and a terminal state means the run is over.
export function catalogStatusRefetchInterval(status?: MediaCatalogStatus): number | false {
  if (!status?.active_job) return false;
  if (status.state === "ready" || status.state === "degraded" || status.state === "failed") return false;
  return 2000;
}
