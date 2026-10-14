package remixlab

import (
	_ "embed"
	"strings"

	"video-production-console/internal/agentruntime/openaicompat"
)

// 技能原文只读嵌入；输入占位段在运行时放进 user，不改源文件。
//
//go:embed stylefiles/finance_v2_writer.txt
var financeV2WriterSource string

const financeV2WriterUser = "【本次补充要求】\n{{NOTES}}\n\n【原文】\n{{SOURCE}}"

func financeV2Writer() string {
	body := financeV2WriterSource
	if i := strings.LastIndex(body, "【本次补充要求】"); i >= 0 {
		body = body[:i]
	}
	return strings.TrimSpace(body) + "\n\n上面第九节的『只输出正文』在本系统中指 continuous_script 字段只放正文；整体按系统附加的 JSON 格式返回。\n\n" + openaicompat.ChannelsPublishingContract
}
