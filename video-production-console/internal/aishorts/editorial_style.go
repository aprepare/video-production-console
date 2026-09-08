package aishorts

import (
	"strings"
	"video-production-console/internal/agentruntime/openaicompat"
)

const financeEditorial = "finance_editorial"

func editorialShotStyle(key string) bool { return key == "paper_collage" || key == "miniature" }

// Legacy single-style projects remain uniform. Missing mixed choices conservatively use collage.
func resolvedShotStyle(projectStyle, shotStyle string) string {
	if projectStyle == financeEditorial {
		if editorialShotStyle(shotStyle) {
			return shotStyle
		}
		return "paper_collage"
	}
	return StyleByKey(projectStyle).Key
}

const editorialPlanningRule = `
本项目采用财经编辑混合画风，替代上文的生活纪实摄影表达要求；原文保真、切镜、禁字规则仍适用。
先判断本镜要传递的信息，再选择 style_key，最后按所选风格设计 scene、subject 和 motion。
paper_collage 是主要表达：生活处境、政策解读、具体对象、观点转折，用半色调照片剪影、撕纸层次和清楚的空间关系，构成一张完整编辑式拼贴。不是把现实场景拍完再套滤镜。
miniature 仅用于需要解释分配、分散、层级、积累或流向关系的概念镜头：例如日常开支/应急储备/长期积累的三个微缩平台；主体数量与摆放要能说明关系。不要因为出现“钱”“家庭”就选模型，不把所有抽象词机械画成隐喻。
不设固定比例，不机械交替；按文意选择，缺乏明确概念关系时用 paper_collage。两者共享米白、深绿、少量朱红，成人编辑视觉，不做幼儿卡通。
人物只有在关系表达确实需要时才出现；拼贴用人物照片剪影，微缩用无夸张表情的模型人物。不要反复用合影、钞票堆、存折和信封替代所有内容。
每个 shot 额外返回 "style_key":"paper_collage" 或 "miniature"。visual_intent 解释这套可见关系如何对应原句。motion 保持纸片或模型材质，只用轻微层次视差、整体推拉、已有元素的小幅移动，不变成真人实拍，不凭空新增元素。`

// Only wrap the shot planner: section segmentation does not need visual instructions.
type visualPlanningClient struct {
	openaicompat.ChatClient
	style string
}

func (c visualPlanningClient) Chat(req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	req.Messages = append([]openaicompat.Message(nil), req.Messages...)
	for i := range req.Messages {
		if req.Messages[i].Role != "system" {
			continue
		}
		if c.style == financeEditorial {
			req.Messages[i].Content += editorialPlanningRule
		} else {
			preset := StyleByKey(c.style)
			req.Messages[i].Content += "\n本项目统一画风：" + preset.Prompt + "请按该画风设计可见场景与动作；如与前文生活纪实摄影表达冲突，以本画风为准。"
		}
	}
	return c.ChatClient.Chat(req)
}

func styledPeopleRule(style string) string {
	switch strings.TrimSpace(style) {
	case "paper_collage":
		return "人物如需出现，使用普通中国生活人物的半色调照片剪影，保留纸片边缘，与拼贴材质一致。"
	case "miniature":
		return "人物如需出现，使用朴素成年微缩模型人物，材质与场景一致，无夸张表情，不生成真人皮肤特写。"
	case "clay_3d":
		return "人物如需出现，使用黏土质感的成年人物，比例适中、表情克制，服装朴素，和场景同一材质，不做卡通大眼。"
	case "papercut":
		return "人物如需出现，用剪纸侧影或皮影式的镂空人形表现，不画写实面孔。"
	case "ink_wash", "woodcut_poster", "poster", "oil_painting":
		return "人物如需出现，按本画风的绘画语言表现普通中国成年人，朴素日常着装、动作和年龄符合场景，不做写实照片质感，不做模特摆拍。"
	case "retro_film":
		return "人物如需出现，为八九十年代打扮的普通中国人，衣着朴素、发型和物件符合年代，自然抓拍不摆拍，胶片颗粒下皮肤真实。"
	default:
		return explainerPeopleRule
	}
}
