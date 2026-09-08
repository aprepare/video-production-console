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
	// Note 给选画风的人看：适合哪类观众、能不能让人停下、什么段落用。不进提示词。
	Note string `json:"note,omitempty"`
	// Preview 参考图地址（前端同一测试场景各出一张，放 web/public/style-previews/<key>.jpg）。
	Preview string `json:"preview,omitempty"`
}

// StylePreviewPath 是画风参考图在前端静态资源里的路径；图由 scripts/ai-shorts/render_style_previews.py 生成。
func StylePreviewPath(key string) string { return "/style-previews/" + key + ".jpg" }

// ExplainerStyles 包含单一画风与混合策略。第一套保留为旧数据的兼容默认；
// 新建项目默认由 Create 指定为 financeEditorial。
var ExplainerStyles = []StylePreset{
	{
		Key: "documentary", Name: "A · 生活纪实",
		Prompt: "风格：中国现实生活纪实摄影，自然日光、中性色彩，真实材质与适度生活痕迹，清楚不过暗；主体、人物关系与动作由场景决定，不做广告摆拍。",
		Usage:  "具体的现实场景：楼、文件、钞票、柜台、街道、餐桌。",
		Note:   "中老年一眼看懂；停留力一般，像新闻配图，靠内容不靠画面。",
	},
	{
		// 2026-09-07 用 grok-imagine-image-2.0 实测：三种画风里 50 岁以上观众最一眼看懂的是写实纪实；
		// 保留 B 的暖光，去掉 B 容易生成假招牌乱码的问题（禁字规则在提示词末尾另附）。
		Key: "cinematic_doc", Name: "写实纪实（电影感）",
		Prompt: "风格：电影级写实、纪实摄影质感，自然光为主、室内用柔和暖光；真实的中国普通家庭、小区、菜市场、银行营业厅和街道；人物皮肤与衣物材质真实不磨皮，色彩中性略暖，清楚不过暗，2K 高清。",
		Usage:  "9:16 解说推荐。人物 + 动作 + 环境的现实场景，中老年观众一眼看懂。",
		Note:   "中老年最稳的一档：像身边的人和事，共情最快；停留力中上，靠人物表情和光线。",
	},
	{
		Key: "warm_realism", Name: "B · 温暖明亮",
		Prompt: "风格：温暖明亮的现实生活摄影，柔和日间暖光，普通中国生活环境，清楚不过曝，保留自然材质，主体与人物按场景表现，不做奢华广告摆拍。",
		Usage:  "生活与学习场景；与A同属真实摄影，光线更温暖。",
		Note:   "比 A 亮、比电影感软，适合祝福和家庭段；停留力一般，容易生成假招牌。",
	},
	{
		Key: "poster", Name: "画报厚涂",
		Prompt: "风格：厚涂水粉编辑插画，笔触可见，八九十年代国内画报年代感；配色中国红 #B22B27、暗金 #C9962B、深普蓝 #23395B，米黄纸底。",
		Usage:  "回忆、历史、时代变迁、情绪强烈或感慨的段落。",
		Note:   "年代感对 50 岁以上有亲切感；色块大、手机上抓眼；整篇用会显得像插画书。",
	},
	{
		Key: "collage", Name: "照片剪影拼贴",
		Prompt: "风格：Vox 式照片拼贴海报，黑白照片剪影（人物、建筑或物件按场景选择）叠在大块纯色几何色块上，色块只用中国红 #C8102E、暖黄 #F2B134、藏青 #14213D 和米白，半调网点、纸张颗粒，高对比。",
		Usage:  "抽象概念、多方关系、规则条款、对比与拆解、数据和比例。",
		Note:   "高对比、最抓眼的一档；但偏年轻编辑感，中老年要适应，适合开头和概念段。",
	},
	{Key: financeEditorial, Name: "财经编辑混合（拼贴为主＋概念微缩）", Prompt: "财经编辑混合，按镜头使用纸张拼贴或微缩模型。", Usage: "新建默认。叙述以纸张拼贴为主，概念关系用微缩模型，逐镜可调整。", Note: "杂志感、统一克制；中老年接受度中等，停留力中等，靠画面之间的变化。"},
	{Key: "paper_collage", Name: "纸张拼贴", Prompt: "风格：成熟财经杂志纸张拼贴，米白纸底、深绿色纸带，朱红只作为一小片撕纸或一条细纸带出现在角落；黑白半色调照片剪影、真实撕纸毛边、纸张投影与轻微印刷颗粒。主次清楚、留白克制，一张完整编辑式画面，不分格。画面上不要画红圈、圆圈、手绘标注线、箭头、涂鸦或任何符号，不在人物和物件上加圈注。", Usage: "生活处境、政策解读、具体对象和观点转折。", Note: "克制、成人、有品；中老年看得懂但不激动，停留力中等；已在天中观局版实测。"},
	{Key: "miniature", Name: "微缩模型", Prompt: "风格：精致微缩模型摄影，哑光纸黏土与真实细小材质，米白底、深绿和少量朱红，共享财经编辑配色。柔和侧光与层次阴影，主体大小、距离、分组表达清楚的概念关系，完整微缩场景，成人审美、不做幼儿卡通，不堆装饰物。", Usage: "资金分配、风险分散、层级、积累和流向关系。", Note: "新鲜、精致，讲关系最清楚；中老年觉得像玩具的风险，只做概念镜别整篇用。"},
	// ---- 2026-09-08 新增候选（用户要求多几种、并标出哪种适合中老年、哪种能让人停下）。同一测试场景各出一张参考图，用户看图定去留。----
	{
		Key: "retro_film", Name: "年代老照片（八九十年代胶片）",
		Prompt: "风格：八九十年代中国老照片的胶片质感，柯达式暖黄偏色、轻微褪色与颗粒、边角略暗；老式居民楼、国营商店、自行车、旧木家具、搪瓷杯、缝纫机等年代物件，人物穿着朴素，真实生活抓拍，柔和自然光，画面清楚不做过度滤镜。",
		Usage:  "回忆、\"前些年 / 那些年\"的对比段、经历过前两轮的人、时代变迁。",
		Note:   "中老年共情最强的一档——画的是他们自己的青春；停留靠熟悉感和怀旧，开头用一张就能把人钉住。整篇用会让\"现在\"的段落也显旧，适合和写实纪实搭配。",
	},
	{
		Key: "ink_wash", Name: "新中式水墨",
		Prompt: "风格：现代水墨插画，宣纸底色、墨色浓淡层次和大面积留白，只用少量朱砂红和赭石点色；城市楼群、银行柜台、存折算盘、家庭餐桌等现代事物用简练墨线和墨块表现，气韵克制，成人审美，不做卡通不做古装。",
		Usage:  "讲道理、讲规律、时代转折的段落（\"锚\"\"水往低处流\"\"风向变了\"）。",
		Note:   "中老年审美最熟悉、最有面子感；但画面安静，停留力中下，适合中段讲理，不适合开头抓人。",
	},
	{
		Key: "woodcut_poster", Name: "版画宣传画",
		// 09-08 首张参考图底部生成了一条乱码标语：宣传画这个词会让模型自己留"标题区"。明说不做标题区、画面铺满四边。
		Prompt: "风格：粗犷木刻版画与丝网宣传画结合，黑色粗轮廓、大块红黄蓝平涂、纸张纹理，人物动作有力、造型概括，八十年代年画海报的构图和饱和度；画面元素少而大，一眼可读。只画场景本身，不做海报标题区、不留标语位、不加边框和装饰栏，画面铺满到四边。",
		Usage:  "叫停、号召、三条建议、强判断句、开头钩子。",
		Note:   "手机小屏最抓眼的一档：形状大、对比高，划过去也会停一下；中老年熟悉年画宣传画的语言。情绪偏硬，讲家庭温情的段落不合适。",
	},
	{
		Key: "clay_3d", Name: "黏土微场景（3D）",
		Prompt: "风格：手工黏土质感的 3D 渲染，圆润造型、哑光材质、柔和影棚光、暖米白背景；小区楼房、银行大厅、菜市场、家庭客厅做成迷你立体场景，人物为成年人、比例适中、表情克制不夸张，整体成人审美，不做幼儿玩具感。",
		Usage:  "概念解释、多方关系、钱的流向和分配、对比。",
		Note:   "停留力强（新鲜、可爱、颜色亮），近两年财经短视频里很常见；但 50 岁以上容易觉得是\"小孩画\"，要靠成人化的人物和场景压住，适合概念段不适合处境段。",
	},
	{
		Key: "papercut", Name: "剪纸皮影",
		Prompt: "风格：中国传统剪纸与皮影的层叠效果，红纸镂空为主、米白底、少量金色点缀，层与层之间有柔和投影和空间感；人物用侧影和镂空纹样表现，楼房、钞票、存折、餐桌等物件也做成镂空剪纸，构图对称饱满，节庆感克制不喧闹，成人审美。",
		Usage:  "祝福词段、家庭和年节相关段、开头或结尾的整版画面。",
		Note:   "中老年最亲切、最有\"自家的东西\"感，红色在信息流里抓眼；连续多镜会单调，适合穿插用（祝福段、开头、结尾），不适合整篇。",
	},
	{
		Key: "macro_money", Name: "钞票存折静物（微距）",
		Prompt: "风格：高清静物微距摄影，人民币纸币和硬币、存折、银行卡、计算器、老花镜、茶杯放在旧木桌或米色桌布上，柔和侧光、浅景深、真实材质与细节，构图简洁克制；不出现人脸，手只在需要时局部出现。",
		Usage:  "利息、存款、价格、\"一万块\"、数字和钱本身的段落。",
		Note:   "和\"钱\"直接挂钩，中老年一眼懂，视频号财经爆款最常用的画面之一，停留力高；但只能做点，不能做面——整篇都是钱堆会腻，也容易触发钞票相关审核，混着用。",
	},
	{
		Key: "oil_painting", Name: "乡土写实油画",
		Prompt: "风格：中国乡土写实油画，厚重可见的笔触、暖棕与土黄为主的色调、柔和侧光；普通人的劳作、赶集、吃饭、看病、家庭场景，人物面部含蓄有故事感、皱纹和手部真实，成人审美，不做糖水甜腻。",
		Usage:  "处境场景、结尾情绪段（老伴住院、彩礼酒席、孙子红包）、\"底气\"一类的段落。",
		Note:   "情绪最重、最能让中老年代入的一档，和写实纪实比多了一层\"被画下来\"的郑重；停留力中上。整篇用偏沉，适合结尾和处境段。",
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
	StyleKey     string        `json:"style_key,omitempty"` // 画风预设 key（ExplainerStyles）
	// Role 镜头在全片里的位置角色（opening / money / blessing / scenario / course / body），拆完分镜按旁白打，
	// 分段画风靠它选画风；StylePinned 表示用户手动定过这镜的画风，分段画风和项目画风都不再覆盖。
	Role        string `json:"role,omitempty"`
	StylePinned bool   `json:"style_pinned,omitempty"`
	Subject     string `json:"subject,omitempty"` // 画面主体一句话，生图提示词开头
	SourceText   string        `json:"source_text,omitempty"`
	VisualIntent string        `json:"visual_intent,omitempty"`
	SubjectType  string        `json:"subject_type,omitempty"`
	Annotation   string        `json:"annotation,omitempty"`
	Keywords     []ShotKeyword `json:"keywords"`
	CaptionLines []string      `json:"caption_lines,omitempty"`
	AspectRatio  string        `json:"aspect_ratio,omitempty"`
	Hero         bool          `json:"hero,omitempty"`        // 重点镜：图生视频；否则图片 + 推拉
	CameraMove   string        `json:"camera_move,omitempty"` // 图片镜的推拉类型
	// ImagePrompt / VideoPrompt 是按当前描述和规则算出来的提示词（预览，随时刷新）；
	// ImagePromptUsed 是现有这张图真正用过的提示词，两者不一致说明图是旧的、要重生。
	ImagePrompt          string `json:"image_prompt,omitempty"`
	ImagePromptUsed      string `json:"image_prompt_used,omitempty"`
	VideoPrompt          string `json:"video_prompt,omitempty"`
	ImagePath            string `json:"image_path,omitempty"`
	ImageStale           bool   `json:"image_stale,omitempty"`
	ImageStatus          string `json:"image_status"`
	VideoPath            string `json:"video_path,omitempty"`
	VideoStatus          string `json:"video_status"`
	VideoRequestID       string `json:"video_request_id,omitempty"`
	VideoRequestBaseURL  string `json:"video_request_base_url,omitempty"`
	VideoSubmitUncertain bool   `json:"video_submit_uncertain,omitempty"`
	Error                string `json:"error,omitempty"`
	// 组装后回填：这镜在成片里的起止秒。
	StartS float64 `json:"start_s,omitempty"`
	EndS   float64 `json:"end_s,omitempty"`
}

// Short 是一条 AI 短片项目。
type Short struct {
	TextReasoningEffort string            `json:"text_reasoning_effort,omitempty"`
	AssemblyProgress    *AssemblyProgress `json:"assembly_progress,omitempty"`
	ID                  string            `json:"id"`
	AccountID           string            `json:"account_id,omitempty"`
	Mode                string            `json:"mode"` // fable / explainer；空按 fable
	Title               string            `json:"title"`
	Headline            string            `json:"headline"` // 顶部大字金句
	Story               string            `json:"story"`    // 旁白全文
	Style               string            `json:"style"`    // 画风前缀
	VisualSettings      *VisualSettings   `json:"visual_settings,omitempty"`
	DraftStale          bool              `json:"draft_stale,omitempty"`
	StoryboardStale     bool              `json:"storyboard_stale,omitempty"`
	Captions            []CaptionCue      `json:"captions,omitempty"`
	// TextModel 是拆分镜用的文本模型；空则用设置里的默认（Runtime.Models.Text）。
	TextModel string `json:"text_model,omitempty"`
	// ImageModel 用于本项目后续生图；空则使用 Runtime.Models.Image，不影响已有素材。
	ImageModel string `json:"image_model,omitempty"`
	// SegmentModel 是解说模式先把整篇按话题切大段用的模型；空则按段落/字数机械切。
	SegmentModel string      `json:"segment_model,omitempty"`
	Status       string      `json:"status"`
	Error        string      `json:"error,omitempty"`
	Characters   []Character `json:"characters"`
	Shots        []Shot      `json:"shots"`
	// Cover 是按大标题单独出的一张 9:16 封面，不进分镜、不进剪映时间轴；发布时下载用。
	Cover *Cover `json:"cover,omitempty"`
	// 组装产物
	NarrationPath string    `json:"narration_path,omitempty"`
	SRTPath       string    `json:"srt_path,omitempty"`
	DraftPath     string    `json:"draft_path,omitempty"`
	DraftName     string    `json:"draft_name,omitempty"`
	DurationS     float64   `json:"duration_s,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Cover 是短片的发布封面：按 Headline + 项目画风生一张图，和分镜图分开存。
type Cover struct {
	Path         string `json:"path,omitempty"`
	Status       string `json:"status,omitempty"` // pending/running/done/failed
	Prompt       string `json:"prompt,omitempty"`
	PromptUsed   string `json:"prompt_used,omitempty"`
	HeadlineUsed string `json:"headline_used,omitempty"`
	StyleKey     string `json:"style_key,omitempty"`
	Error        string `json:"error,omitempty"`
	Stale        bool   `json:"stale,omitempty"`
}

// SpokenByCharacter 表示这句由画面里的角色开口说（视频模型出声），而不是旁白 TTS。
func (s Shot) SpokenByCharacter() bool {
	return strings.TrimSpace(s.Narration) != "" && s.Speaker != "" && s.Speaker != SpeakerNarrator
}

// IsExplainer 判断短片走解说模式。
func (s *Short) IsExplainer() bool { return s.Mode == ModeExplainer }

// WantsAnyVideo：解说模式是否有任何镜头要做图生视频。
func (s *Short) WantsAnyVideo() bool {
	if !s.IsExplainer() {
		return true
	}
	return s.VisualSettings.Plan() != VideoPlanNone
}

// shotStart 取这镜的起始秒；还没配音定时的用 0.23 秒/字估。
func (s *Short) shotStart(shot Shot) float64 {
	if shot.EndS > shot.StartS {
		return shot.StartS
	}
	start := 0.0
	for _, previous := range s.Shots {
		if previous.Index == shot.Index {
			break
		}
		start += float64(substantiveRunes(previous.Narration))*0.23 + shotGapSeconds
	}
	return start
}

// isMidHookShot：中段钩子 = 邀请留四字祝福的那一镜和紧接着的一镜（"接着说，因为……"）。
func (s *Short) isMidHookShot(shot Shot) bool {
	for i, x := range s.Shots {
		if strings.Contains(x.Narration, "四个字") {
			return shot.Index == x.Index || (i+1 < len(s.Shots) && shot.Index == s.Shots[i+1].Index)
		}
	}
	return false
}

// NeedsVideo：寓言每镜视频；财经解说按 VideoPlan 决定。
func (s *Short) NeedsVideo(shot Shot) bool {
	if !s.IsExplainer() {
		return true
	}
	switch s.VisualSettings.Plan() {
	case VideoPlanAll:
		return true
	case VideoPlanFirstN:
		return shot.Index < s.VisualSettings.VideoFirstN
	case VideoPlanOpening:
		return s.shotStart(shot) < s.VisualSettings.OpeningLimit()
	case VideoPlanHooks:
		if s.shotStart(shot) < hooksOpeningSecs {
			return true
		}
		if s.isMidHookShot(shot) {
			return true
		}
		return shot.Index >= len(s.Shots)-hooksClosingShots
	}
	return false
}

// ShotReady 表示这镜的画面素材齐了，可以组装。
func (s *Short) ShotReady(shot Shot) bool {
	if s.NeedsVideo(shot) {
		return shot.VideoStatus == ShotDone && shot.VideoPath != "" && !shot.ImageStale
	}
	return shot.ImageStatus == ShotDone && shot.ImagePath != "" && !shot.ImageStale
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
