import type { Account, Project } from "../types";

export const PROJECT_COLLAPSE_LIMIT = 4;

export const stages: Array<Project["stage"]> = [
  "topic",
  "script",
  "assets",
  "mixing",
  "review",
  "published",
];

export function stageLabel(stage: Project["stage"]) {
  return (
    (
      {
        topic: "选题准备",
        script: "文案制作",
        assets: "配音字幕",
        mixing: "混剪制作",
        review: "成片审核",
        published: "已发布",
      } as Record<string, string>
    )[stage] || stage
  );
}

export function projectStageHint(stage: Project["stage"]) {
  return (
    (
      {
        topic: "正在确定选题或生成选题卡",
        script: "选题卡已就绪，正在制作文案",
        assets: "文案已登记，正在准备配音和 SRT",
        mixing: "配音和 SRT 已齐，正在制作混剪",
        review: "检查发布文案并确认发布状态",
        published: "已经发布",
      } as Record<string, string>
    )[stage] || stage
  );
}

export function accountName(id: string, accounts: Account[]) {
  return accounts.find((account) => account.id === id)?.name || "未分配";
}

export function formatDate(value?: string) {
  return value
    ? new Date(value).toLocaleString("zh-CN", {
        month: "numeric",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "暂无";
}
