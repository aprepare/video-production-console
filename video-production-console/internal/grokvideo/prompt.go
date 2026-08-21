package grokvideo

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxPromptRunes = 4000

type MotionPromptInput struct {
	Paragraph  string
	ImageTitle string
	MotionHint string
}

// BuildMotionPrompt keeps baked-in Chinese titles stable while asking for the
// faster visual change needed by a short-video hook. It accepts semantic text
// only; local paths, project IDs, endpoints, and credentials are not inputs.
func BuildMotionPrompt(input MotionPromptInput) (string, error) {
	paragraph := strings.TrimSpace(input.Paragraph)
	if paragraph == "" {
		return "", fmt.Errorf("video paragraph is empty")
	}
	imageTitle := strings.TrimSpace(input.ImageTitle)
	motionHint := strings.TrimSpace(input.MotionHint)
	if motionHint == "" {
		motionHint = "根据段落语义让主体、光影、环境元素产生清晰而自然的快速变化，镜头保持稳定，不要剧烈抖动"
	}
	prompt := strings.Join([]string{
		"把这张赤墨风竖屏图片制作成节奏明快的短视频镜头。",
		"必须在0.5秒内开始运动，并在3秒内出现肉眼可见的画面变化；不要慢动作，不要长时间静止。",
		"保持原图构图、人物身份、中文标题的位置、字形、大小和每个字的拼写完全稳定；标题不能漂移、变形、闪烁或消失。",
		"不得新增任何文字、数字、水印、logo、字幕或界面元素。",
		"如果画面有人物，人物只做自然肢体和视线动作，不说话，不对口型。",
		"动作方向：" + motionHint + "。",
		"对应口播段落：" + paragraph,
		"原图标题：" + imageTitle,
	}, "\n")
	if utf8.RuneCountInString(prompt) > maxPromptRunes {
		return "", fmt.Errorf("video prompt exceeds %d characters", maxPromptRunes)
	}
	return prompt, nil
}
