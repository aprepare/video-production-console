export type AppLocation =
  | { view: "mode-home" }
  | { view: "projects" }
  | { view: "project"; projectID: string }
  | { view: "image-projects" }
  | { view: "image-projects-advanced" }
  | { view: "image-project"; projectID: string }
  | { view: "not-found" };

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function parseLocation(pathname: string): AppLocation {
  if (pathname === "/") return { view: "mode-home" };
  if (pathname === "/image-projects" || pathname === "/image-projects/") {
    return { view: "image-projects" };
  }
  if (pathname === "/image-projects/advanced" || pathname === "/image-projects/advanced/") {
    return { view: "image-projects-advanced" };
  }
  const imageMatch = pathname.match(/^\/image-projects\/([^/]+)\/?$/);
  if (imageMatch && uuidPattern.test(imageMatch[1])) {
    return { view: "image-project", projectID: imageMatch[1].toLowerCase() };
  }
  if (pathname === "/projects" || pathname === "/projects/") {
    return { view: "projects" };
  }
  const match = pathname.match(/^\/projects\/([^/]+)\/?$/);
  if (!match || !uuidPattern.test(match[1])) return { view: "not-found" };
  return { view: "project", projectID: match[1].toLowerCase() };
}
