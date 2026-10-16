export type AppLocation =
  | { view: "projects" }
  | { view: "project"; projectID: string }
  | { view: "image-projects" }
  | { view: "image-projects-advanced" }
  | { view: "image-project"; projectID: string }
  | { view: "image-videos" }
  | { view: "image-video"; projectID: string }
  | { view: "remix-lab" }
  | { view: "remix-lab"; experimentID: string }
  | { view: "references" }
  | { view: "replicate" }
  | { view: "replicate-project"; projectID: string }
  | { view: "ai-access" }
  | { view: "merchants" }
  | { view: "merchant"; merchantID: string }
  | { view: "setup" }
  | { view: "guide" }
  | { view: "not-found" };

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isRemovedWorkspacePath(pathname: string): boolean {
  return ["/workspace", "/ai-shorts"].some(path => pathname === path || pathname.startsWith(`${path}/`));
}

export function parseLocation(pathname: string): AppLocation {
  if (pathname === "/" || isRemovedWorkspacePath(pathname)) return { view: "remix-lab" };
  if (pathname === "/references" || pathname === "/references/") return { view: "references" };
  if (pathname === "/replicate" || pathname === "/replicate/") return { view: "replicate" };
  const replicateMatch = pathname.match(/^\/replicate\/([^/]+)\/?$/);
  if (replicateMatch) return uuidPattern.test(replicateMatch[1]) ? { view: "replicate-project", projectID: replicateMatch[1].toLowerCase() } : { view: "not-found" };
  if (pathname === "/ai-access" || pathname === "/ai-access/") return { view: "ai-access" };
  if (pathname === "/setup" || pathname === "/setup/") return { view: "setup" };
  if (pathname === "/guide" || pathname === "/guide/") return { view: "guide" };
  if (pathname === "/merchants" || pathname === "/merchants/") return { view: "merchants" };
  const merchantMatch = pathname.match(/^\/merchants\/([^/]+)\/?$/);
  if (merchantMatch) return uuidPattern.test(merchantMatch[1]) ? { view: "merchant", merchantID: merchantMatch[1].toLowerCase() } : { view: "not-found" };
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
  if (pathname === "/image-videos" || pathname === "/image-videos/") {
    return { view: "image-videos" };
  }
  const videoMatch = pathname.match(/^\/image-videos\/([^/]+)\/?$/);
  if (videoMatch && uuidPattern.test(videoMatch[1])) {
    return { view: "image-video", projectID: videoMatch[1].toLowerCase() };
  }
  if (pathname === "/remix-lab" || pathname === "/remix-lab/") {
    return { view: "remix-lab" };
  }
  const remixMatch = pathname.match(/^\/remix-lab\/([^/]+)\/?$/);
  if (remixMatch) {
    if (!uuidPattern.test(remixMatch[1])) return { view: "not-found" };
    return { view: "remix-lab", experimentID: remixMatch[1].toLowerCase() };
  }
  if (pathname.startsWith("/remix-lab/")) return { view: "not-found" };
  if (pathname === "/projects" || pathname === "/projects/") {
    return { view: "projects" };
  }
  const match = pathname.match(/^\/projects\/([^/]+)\/?$/);
  if (!match || !uuidPattern.test(match[1])) return { view: "not-found" };
  return { view: "project", projectID: match[1].toLowerCase() };
}
