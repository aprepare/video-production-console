import { Clapperboard, Film, Images, type LucideIcon } from "lucide-react";

export type ProductionModeDefinition = {
  id: string;
  title: string;
  description: string;
  href: string;
  accent: "teal" | "amber";
  icon: LucideIcon;
};

export const productionModes: ProductionModeDefinition[] = [
  {
    id: "montage",
    title: "混剪制作",
    description: "管理素材、口播、配音、字幕与剪映草稿，完成整条视频生产流程。",
    href: "/projects",
    accent: "amber",
    icon: Clapperboard,
  },
  {
    id: "movie-montage",
    title: "电影混剪",
    description: "用电影切镜库配口播，在同一项目工作台里生成剪映草稿。",
    href: "/projects",
    accent: "amber",
    icon: Film,
  },
  {
    id: "image",
    title: "图文制作",
    description: "按文案分段，生成带中文标题与说明标注的竖版图文。",
    href: "/image-projects",
    accent: "teal",
    icon: Images,
  },
];
