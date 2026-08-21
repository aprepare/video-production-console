import { Clapperboard, Film, ImagePlay, Images, type LucideIcon } from "lucide-react";

export type MontageKind = "scenic" | "movie" | "image-video";

export type ProductionModeDefinition = {
  id: string;
  title: string;
  description: string;
  href: string;
  accent: "teal" | "amber" | "blue";
  icon: LucideIcon;
  /** 目录里仍保留，控制台入口暂不开放。 */
  paused?: boolean;
};

export const SCENIC_BOARD_HREF = "/projects?mode=scenic";
export const IMAGE_PROJECTS_HREF = "/image-projects";

export const productionModes: ProductionModeDefinition[] = [
  {
    id: "scenic",
    title: "风景混剪",
    description: "口播配风景镜头，生成剪映草稿。",
    href: SCENIC_BOARD_HREF,
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
    paused: true,
  },
  {
    id: "image",
    title: "图文制作",
    description: "按文案生成最多 18 张竖版图片，打包 ZIP。不走剪映草稿。",
    href: IMAGE_PROJECTS_HREF,
    accent: "teal",
    icon: Images,
  },
  {
    id: "image-video",
    title: "图文视频",
    description: "粘贴完整口播，按时长拆段生图，再出无字幕剪映草稿。",
    href: "/image-videos",
    accent: "blue",
    icon: ImagePlay,
    paused: true,
  },
];

export const visibleProductionModes = productionModes.filter((mode) => !mode.paused);

export function parseMontageKind(search: string): MontageKind {
  const value = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search).get("mode");
  if (value === "movie" || value === "image-video") return value;
  return "scenic";
}

/** 电影混剪、图文视频入口已屏蔽，界面一律按风景混剪处理。 */
export function visibleMontageKind(_search: string): MontageKind {
  return "scenic";
}

export function montageKindLabel(kind: MontageKind): string {
  if (kind === "movie") return "电影混剪";
  if (kind === "image-video") return "图片视频";
  return "风景混剪";
}

export function isShieldedProductionPath(pathname: string): boolean {
  return pathname === "/image-videos" || pathname.startsWith("/image-videos/");
}
