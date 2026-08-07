export type AppLocation = { view: "projects" } | { view: "project"; projectID: string };

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function parseLocation(pathname: string): AppLocation {
  if (pathname === "/" || pathname === "/projects" || pathname === "/projects/") {
    return { view: "projects" };
  }
  const match = pathname.match(/^\/projects\/([^/]+)\/?$/);
  if (!match || !uuidPattern.test(match[1])) return { view: "projects" };
  return { view: "project", projectID: match[1].toLowerCase() };
}
