import { Clapperboard, Film, ImagePlay, Images, type LucideIcon } from "lucide-react";

export type MontageKind = "scenic" | "movie" | "image-video";

export type ProductionModeDefinition = {
  id: string;
  title: string;
  description: string;
  href: string;
  accent: "teal" | "amber" | "blue";
  icon: LucideIcon;
};

const modeCapabilities: Record<string, readonly string[]> = {
  scenic: ["scenery_montage", "text"],
  "movie-montage": ["movie_montage"],
  "image-video": ["image_video"],
  image: ["image_text", "image"],
};

export function modesForCapabilities(features: readonly string[]): ProductionModeDefinition[] {
  const available = new Set(features);
  return productionModes.filter((mode) =>
    (modeCapabilities[mode.id] ?? []).some((feature) => available.has(feature)),
  );
}

export const productionModes: ProductionModeDefinition[] = [
  {
    id: "scenic",
    title: "风景混剪",
    description: "口播配风景镜头，生成剪映草稿。",
    href: "/projects?mode=scenic",
    accent: "amber",
    icon: Clapperboard,
  },
  {
    id: "movie-montage",
    title: "电影混剪",
    description: "口播配电影切镜，生成剪映草稿。",
    href: "/projects?mode=movie",
    accent: "amber",
    icon: Film,
  },
  {
    id: "image-video",
    title: "图片视频",
    description: "口播配静帧，生成剪映草稿。",
    href: "/projects?mode=image-video",
    accent: "blue",
    icon: ImagePlay,
  },
  {
    id: "image",
    title: "图文制作",
    description: "按文案生成竖版图文。",
    href: "/image-projects",
    accent: "teal",
    icon: Images,
  },
];

export function parseMontageKind(search: string): MontageKind {
  const value = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search).get("mode");
  if (value === "movie" || value === "image-video") return value;
  return "scenic";
}

export function montageKindLabel(kind: MontageKind): string {
  if (kind === "movie") return "电影混剪";
  if (kind === "image-video") return "图片视频";
  return "风景混剪";
}
