export type MessageTone = "danger" | "success" | "warning" | "info";

export function messageTone(message: string): MessageTone {
  if (/失败|错误|不正确|无法|不可用|离线|中断|不能/.test(message)) return "danger";
  if (/已保存|已创建|已删除|已停止|已发布|已重新/.test(message)) return "success";
  if (/等待|排队|重启|稍候|确认/.test(message)) return "warning";
  return "info";
}
