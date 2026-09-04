// Package aishorts 是「AI 短片」生产线：一段寓言文案 → 分镜 → 角色设定图 →
// 每镜首帧图 → 图生视频 → 配音 → 剪映草稿。与混剪不同，画面全部由模型生成，
// 所以核心资产是分镜卡片，而不是素材库检索。
package aishorts

import (
	"strings"
	"time"
)

const (
	StatusDraft      = "draft"      // 刚建，还没拆分镜
	StatusStoryboard = "storyboard" // 分镜已拆出，等生图/生视频
	StatusGenerating = "generating" // 正在批量生图/生视频
	StatusReady      = "ready"      // 全部镜头就位，可组装
	StatusAssembling = "assembling" // 配音 + 剪映草稿中
	StatusAssembled  = "assembled"  // 草稿已进剪映
	StatusFailed     = "failed"
	ShotPending      = "pending"
	ShotRunning      = "running"
	ShotDone         = "done"
	ShotFailed       = "failed"
	// 参考：上世纪八九十年代国产电视动画（美影厂系）——赛璐璐平涂、粗黑线勾边、
	// 低饱和暖调、略带胶片颗粒和轻微褪色，横屏 16:9。不是水墨山水。
	DefaultStylePrompt = "上世纪八九十年代中国电视动画风格，上海美术电影制片厂质感，赛璐璐平涂上色，粗黑线勾边，颜色低饱和偏暖、略褪色，画面有轻微胶片颗粒，拟人化动物角色穿简单布衣，背景是手绘的山崖、山洞、云雾，横屏 16:9 电影感构图"
	// 第一版的默认画风（水墨 + 竖屏），读到老记录时自动换成新默认。
	legacyStylePrompt = "上世纪中国水墨动画风格，上海美术电影制片厂质感，赛璐璐上色，墨色山水背景，人物拟人化动物，线条干净，胶片颗粒感，竖屏 9:16"
	// 旁白 speaker 的保留名；其他 speaker 都是角色名。
	SpeakerNarrator = "旁白"

	// ModeFable：寓言动画——角色设定图 + 每镜图生视频，角色在视频里开口。
	ModeFable = "fable"
	// ModeExplainer：财经解说——每镜一张图 + 推拉平移，重点镜才图生视频，全部旁白配音。
	ModeExplainer = "explainer"
)

// StylePreset 是解说模式的一套画风：拆分镜时按 Usage 给每镜选一套，生图时用 Prompt。
type StylePreset struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Usage  string `json:"usage"`
}

// ExplainerStyles 是给中老年财经观众选定的三套：纪实为主，画报讲回忆情绪，拼贴讲抽象对比。
// 顺序即优先级，第一套是默认。
var ExplainerStyles = []StylePreset{
	{
		Key: "documentary", Name: "纪实电影感",
		Prompt: "风格：电影感写实摄影，中国现实场景，以物件和环境为主，暖色调，清晨或黄昏柔光，浅景深，纪录片截帧质感，不要塑料感。",
		Usage:  "默认。具体的现实场景：楼、文件、钞票、柜台、街道、餐桌。",
	},
	{
		Key: "poster", Name: "画报厚涂",
		Prompt: "风格：厚涂水粉编辑插画，笔触可见，八九十年代国内画报年代感；配色中国红 #B22B27、暗金 #C9962B、深普蓝 #23395B，米黄纸底。",
		Usage:  "回忆、历史、时代变迁、情绪强烈或感慨的段落。",
	},
	{
		Key: "collage", Name: "照片剪影拼贴",
		Prompt: "风格：Vox 式照片拼贴海报，黑白照片剪影（建筑、街景、物件为主）叠在大块纯色几何色块上，色块只用中国红 #C8102E、暖黄 #F2B134、藏青 #14213D 和米白，半调网点、纸张颗粒，高对比。",
		Usage:  "抽象概念、多方关系、规则条款、对比与拆解、数据和比例。",
	},
}

// StyleByKey 找不到就回第一套。
func StyleByKey(key string) StylePreset {
	for _, s := range ExplainerStyles {
		if s.Key == key {
			return s
		}
	}
	return ExplainerStyles[0]
}

// CameraMoves 是图片镜的推拉平移类型，按镜头序号轮转，Python 侧用关键帧实现。
var CameraMoves = []string{"zoom_in", "zoom_out", "pan_left", "pan_right"}

// Character 是故事里出场的一个角色：一张设定图，全片所有镜头都拿它当参考。
type Character struct {
	Name        string `json:"name"`
	Description string `json:"description"` // 外形与性格，喂给生图
	// Voice 是角色声线标签（狡诈/憨厚/老者/少年/威严/温和），组装时映射到音色 ID。
	Voice     string `json:"voice,omitempty"`
	ImagePath string `json:"image_path,omitempty"`
	Status    string `json:"status"` // pending/running/done/failed
	Error     string `json:"error,omitempty"`
}

// Shot 是一张分镜卡：一句台词对应一段画面。
type Shot struct {
	Index      int      `json:"index"`
	Narration  string   `json:"narration"`  // 这镜念的话
	Speaker    string   `json:"speaker"`    // 「旁白」或角色名：谁在说这句
	Scene      string   `json:"scene"`      // 画面描述（喂生图）
	Motion     string   `json:"motion"`     // 镜头/动作描述（喂生视频）
	Characters []string `json:"characters"` // 出场角色名
	Seconds    int      `json:"seconds"`    // 6 / 10 / 15
	// 解说模式专用：
	StyleKey   string `json:"style_key,omitempty"`   // 画风预设 key（ExplainerStyles）
	Subject    string `json:"subject,omitempty"`     // 画面主体一句话，生图提示词开头
	Hero       bool   `json:"hero,omitempty"`        // 重点镜：图生视频；否则图片 + 推拉
	CameraMove string `json:"camera_move,omitempty"` // 图片镜的推拉类型
	// ImagePrompt / VideoPrompt 是按当前描述和规则算出来的提示词（预览，随时刷新）；
	// ImagePromptUsed 是现有这张图真正用过的提示词，两者不一致说明图是旧的、要重生。
	ImagePrompt     string `json:"image_prompt,omitempty"`
	ImagePromptUsed string `json:"image_prompt_used,omitempty"`
	VideoPrompt     string `json:"video_prompt,omitempty"`
	ImagePath       string `json:"image_path,omitempty"`
	ImageStatus     string `json:"image_status"`
	VideoPath       string `json:"video_path,omitempty"`
	VideoStatus     string `json:"video_status"`
	VideoRequestID  string `json:"video_request_id,omitempty"`
	Error           string `json:"error,omitempty"`
	// 组装后回填：这镜在成片里的起止秒。
	StartS float64 `json:"start_s,omitempty"`
	EndS   float64 `json:"end_s,omitempty"`
}

// Short 是一条 AI 短片项目。
type Short struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id,omitempty"`
	Mode      string `json:"mode"` // fable / explainer；空按 fable
	Title     string `json:"title"`
	Headline  string `json:"headline"` // 顶部大字金句
	Story     string `json:"story"`    // 旁白全文
	Style     string `json:"style"`    // 画风前缀
	// TextModel 是拆分镜用的文本模型；空则用设置里的默认（Runtime.Models.Text）。
	TextModel string `json:"text_model,omitempty"`
	// SegmentModel 是解说模式先把整篇按话题切大段用的模型；空则按段落/字数机械切。
	SegmentModel string      `json:"segment_model,omitempty"`
	Status       string      `json:"status"`
	Error        string      `json:"error,omitempty"`
	Characters   []Character `json:"characters"`
	Shots        []Shot      `json:"shots"`
	// 组装产物
	NarrationPath string    `json:"narration_path,omitempty"`
	SRTPath       string    `json:"srt_path,omitempty"`
	DraftPath     string    `json:"draft_path,omitempty"`
	DraftName     string    `json:"draft_name,omitempty"`
	DurationS     float64   `json:"duration_s,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SpokenByCharacter 表示这句由画面里的角色开口说（视频模型出声），而不是旁白 TTS。
func (s Shot) SpokenByCharacter() bool {
	return strings.TrimSpace(s.Narration) != "" && s.Speaker != "" && s.Speaker != SpeakerNarrator
}

// IsExplainer 判断短片走解说模式。
func (s *Short) IsExplainer() bool { return s.Mode == ModeExplainer }

// NeedsVideo 表示这镜要出视频：寓言动画每镜出视频；财经解说固定只用图片。
func (s *Short) NeedsVideo(shot Shot) bool {
	if s.IsExplainer() {
		return false
	}
	return true
}

// ShotReady 表示这镜的画面素材齐了，可以组装。
func (s *Short) ShotReady(shot Shot) bool {
	if s.NeedsVideo(shot) {
		return shot.VideoStatus == ShotDone && shot.VideoPath != ""
	}
	return shot.ImageStatus == ShotDone && shot.ImagePath != ""
}

// Models 是本生产线用到的模型名，来自设置或默认值。
type Models struct {
	Image string `json:"image"`
	Video string `json:"video"`
	Text  string `json:"text"` // 拆分镜用
}

// VoiceTags 是拆分镜时给角色打的声线标签；组装时按标签查音色 ID，查不到就用旁白音色。
var VoiceTags = []string{"狡诈", "憨厚", "老者", "少年", "威严", "温和", "女声"}

func DefaultModels() Models {
	return Models{Image: "grok-imagine-image-2.0", Video: "grok-imagine-video-1.5", Text: "grok-4.6-fast"}
}
