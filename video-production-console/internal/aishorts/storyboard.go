package aishorts

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"video-production-console/internal/agentruntime/openaicompat"
)

// 分镜拆解：把一段寓言旁白拆成 6～12 张分镜卡，每张卡一句旁白 + 画面 + 镜头，
// 同时给出全片角色表和顶部金句。角色设定先于分镜生成，后面每镜都拿角色图当参考。

const storyboardSystemPrompt = `你是短视频寓言动画的分镜师兼编剧。输入是一段 100～300 字的文案（财富/认知/人性题材），输出 JSON。

第一步先判断文案类型：
A. 文案本身就是寓言，有明确的动物或人物、有对话——直接沿用它的角色。
B. 文案是抽象道理（讲存款、认知差、跟风、窗口期），没有具体角色——你要先为它设计一个寓言载体：选一组动物或古装人物、一个场景，把每个抽象概念对应成具体的物件和动作（例如「割韭菜的人」→ 崖下熬汤的狐狸，「跟风的人」→ 排队跳崖的鸡，「窗口期」→ 涨潮前的滩涂）。角色和场景都从这个载体长出来，禁止把抽象句子直接画成画（不要出现 K 线、钞票飞、文字特效）。

输出要求：
1. headline：一句 12～18 字的顶部大字金句，点破寓意（如「拿幻想当出路，最终沦为他人的养料」）。原文有现成金句就用，没有就写。
2. characters：出场角色表，每个角色：
   - name（2～4 字，如「狐厨」「领头鸡」）
   - description（外形与性格 60 字内：物种、体色、服饰道具、表情气质；同一角色全片外形固定）
   - voice：声线标签，只能从这些里选一个：狡诈 / 憨厚 / 老者 / 少年 / 威严 / 温和 / 女声。按角色性格选，配音时每个角色用自己的声音。
3. shots：把文案按语义切成 6～12 镜，每镜：
   - narration：这镜念的原句（不改字、不增删；全部 shots 的 narration 顺序连起来必须等于原文）
   - speaker：这句是谁说的。角色的台词（第一人称、对白、喊话）填该角色的 name；叙述、评论、点题的句子填「旁白」。类型 A 的文案里大部分是角色台词，类型 B 的文案基本都是旁白。
     角色台词会由视频模型让角色在画面里开口说出来（对口型），所以：一镜只能一个人说；说话的角色必须在 characters 里且是画面主体；台词太长（超过 30 字）要拆成两镜。
   - scene：画面描述 40～80 字：景别、主体、环境、光线、情绪；只描述这一瞬间的静态画面；横屏构图。角色说话的镜头用中景或近景，能看清嘴。
   - motion：镜头与动作 20～40 字（缓慢推近/横移/主体做什么），动作幅度小
   - characters：这镜出场的角色名（来自角色表），空数组表示纯景
   - seconds：6（默认）；这句特别长可以 10
4. 画面不要出现可读文字。不要血腥。风格是老动画，不要写"写实""照片级"。
只返回 JSON 对象：{"headline":"","characters":[{"name":"","description":"","voice":""}],"shots":[{"narration":"","speaker":"旁白","scene":"","motion":"","characters":[],"seconds":6}]}`

type storyboardReply struct {
	Headline   string `json:"headline"`
	Characters []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Voice       string `json:"voice"`
	} `json:"characters"`
	Shots []struct {
		Narration  string   `json:"narration"`
		Speaker    string   `json:"speaker"`
		Scene      string   `json:"scene"`
		Motion     string   `json:"motion"`
		Characters []string `json:"characters"`
		Seconds    int      `json:"seconds"`
	} `json:"shots"`
}

// buildStoryboard 调文本模型拆分镜，写回 short 的 headline/characters/shots。
func buildStoryboard(chat openaicompat.ChatClient, model string, short *Short) error {
	resp, err := chat.Chat(openaicompat.ChatRequest{
		Model:           model,
		Stream:          true,
		ReasoningEffort: reasoningOf(short.TextReasoningEffort),
		Messages: []openaicompat.Message{
			{Role: "system", Content: storyboardSystemPrompt},
			{Role: "user", Content: "旁白：\n" + strings.TrimSpace(short.Story)},
		},
	})
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return errors.New("storyboard model returned no choices")
	}
	raw := extractJSONObject(resp.Choices[0].Message.Content)
	var reply storyboardReply
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		return fmt.Errorf("storyboard JSON unparsable: %w", err)
	}
	return applyStoryboardReply(short, reply)
}

// applyStoryboardReply 把模型回复写进 Short：角色声线标签只认白名单，
// speaker 只认「旁白」或角色表里的名字，其他一律回落到旁白。
func applyStoryboardReply(short *Short, reply storyboardReply) error {
	if len(reply.Shots) == 0 {
		return errors.New("storyboard has no shots")
	}
	if strings.TrimSpace(reply.Headline) != "" && strings.TrimSpace(short.Headline) == "" {
		short.Headline = strings.TrimSpace(reply.Headline)
	}
	short.Characters = short.Characters[:0]
	names := map[string]bool{}
	for _, c := range reply.Characters {
		if strings.TrimSpace(c.Name) == "" {
			continue
		}
		name := strings.TrimSpace(c.Name)
		names[name] = true
		short.Characters = append(short.Characters, Character{
			Name: name, Description: strings.TrimSpace(c.Description), Voice: normalizeVoiceTag(c.Voice), Status: ShotPending,
		})
	}
	short.Shots = short.Shots[:0]
	for i, s := range reply.Shots {
		seconds := s.Seconds
		if seconds != 10 && seconds != 15 {
			seconds = 6
		}
		speaker := strings.TrimSpace(s.Speaker)
		if speaker == "" || (speaker != SpeakerNarrator && !names[speaker]) {
			speaker = SpeakerNarrator
		}
		if speaker != SpeakerNarrator {
			// 角色要在视频里把这句说完，时长跟着台词走。
			if need := secondsForLine(s.Narration); need > seconds {
				seconds = need
			}
		}
		short.Shots = append(short.Shots, Shot{
			Index: i, Narration: strings.TrimSpace(s.Narration), Speaker: speaker, Scene: strings.TrimSpace(s.Scene),
			Motion: strings.TrimSpace(s.Motion), Characters: s.Characters, Seconds: seconds,
			ImageStatus: ShotPending, VideoStatus: ShotPending,
		})
	}
	return nil
}

func normalizeVoiceTag(tag string) string {
	tag = strings.TrimSpace(tag)
	for _, known := range VoiceTags {
		if tag == known {
			return tag
		}
	}
	return ""
}

// extractJSONObject 容错取出回复里的第一个 JSON 对象（模型偶尔包 Markdown 围栏）。
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			return text[start : end+1]
		}
	}
	return text
}

// characterPrompt 是角色设定图的生图提示词：白底/简单背景、全身、正面，方便当参考。
func characterPrompt(style string, c Character) string {
	return fmt.Sprintf("%s。角色设定图：%s，%s。全身，正面略侧，站姿，居中，简洁的浅色手绘背景，无文字。", style, c.Name, c.Description)
}

// shotImagePrompt 是分镜首帧的生图提示词。
func shotImagePrompt(style string, shot Shot, characters []Character) string {
	var b strings.Builder
	b.WriteString(style)
	b.WriteString("。画面：")
	b.WriteString(shot.Scene)
	if names := describeCharacters(shot.Characters, characters); names != "" {
		b.WriteString("。出场角色（保持与参考图一致）：")
		b.WriteString(names)
	}
	b.WriteString("。横屏 16:9 电影感构图，无文字，无水印。")
	return b.String()
}

// shotVideoPrompt 是图生视频的提示词。视频模型自带音轨：
//   - 角色台词镜：把台词放进双引号让角色开口说（模型会对口型），并描述声线；
//   - 旁白镜：只要环境音，明确禁止任何人声，旁白由 TTS 另配。
func shotVideoPrompt(shot Shot, characters []Character) string {
	motion := strings.TrimSpace(shot.Motion)
	if motion == "" {
		motion = "镜头缓慢推近，画面里的主体做轻微自然的动作"
	}
	var b strings.Builder
	b.WriteString(motion)
	b.WriteString("。")
	if shot.SpokenByCharacter() {
		voice := "成年男声"
		for _, c := range characters {
			if c.Name == shot.Speaker {
				voice = voiceDescription(c.Voice)
				break
			}
		}
		fmt.Fprintf(&b, "%s面向镜头或对方开口说话，口型与台词同步，用普通话、%s说：\"%s\"。",
			shot.Speaker, voice, strings.TrimSpace(shot.Narration))
		b.WriteString("画风与首帧完全一致，动作幅度小，无字幕无文字。")
		b.WriteString("AUDIO: 只有这一句台词，清晰、语速正常、说完为止；配很轻的环境音（风声、火声或鸟鸣），没有背景音乐，没有其他人声。")
		return b.String()
	}
	b.WriteString("画风与首帧完全一致，动作幅度小，无字幕无文字，角色不开口。")
	b.WriteString("AUDIO: 只有轻微环境音（风声、水声、火声之类），没有任何人声、没有说话、没有旁白、没有背景音乐。")
	return b.String()
}

// voiceDescription 把声线标签翻成视频模型能听懂的嗓音描述。
func voiceDescription(tag string) string {
	switch tag {
	case "狡诈":
		return "油滑拖腔、带一点阴笑的中年男声"
	case "憨厚":
		return "憨直缓慢、嗓音粗厚的男声"
	case "老者":
		return "苍老低沉、略带沙哑的老人声"
	case "少年":
		return "清亮急切的少年声"
	case "威严":
		return "洪亮有力、居高临下的男声"
	case "温和":
		return "温和平缓的成年男声"
	case "女声":
		return "柔和清晰的女声"
	}
	return "成年男声"
}

// secondsForLine 按台词长度选视频时长，保证角色能把话说完（普通话约 4 字/秒）。
func secondsForLine(line string) int {
	switch n := len([]rune(strings.TrimSpace(line))); {
	case n <= 16:
		return 6
	case n <= 32:
		return 10
	default:
		return 15
	}
}

func describeCharacters(names []string, characters []Character) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		for _, c := range characters {
			if c.Name == name {
				parts = append(parts, c.Name+"（"+c.Description+"）")
			}
		}
	}
	return strings.Join(parts, "；")
}
