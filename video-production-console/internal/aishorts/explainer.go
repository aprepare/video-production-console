package aishorts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/agentruntime/openaicompat"
)

// 解说模式的拆分镜。文案通常 1500～3000 字（念 6～12 分钟），一次喂给模型
// 输出几十镜的 JSON 容易丢句，所以按段落切成 ≤ explainerChunkRunes 字的块，
// 各块并行拆，再按原顺序拼起来。每块拆完都核对「全部 narration 连起来 == 原文」，
// 不等就重试一次，还不等就退回按句号硬切，保证一个字都不丢。
const (
	// 09-08 提速：块从 450 字缩到 320 字、并发从 6 提到 10。单次调用的耗时基本跟输出 token 成正比，
	// 块小一半每次就快一半；一篇 1800～2300 字的稿 6～8 块同时在跑，总时长 ≈ 最慢那一块。
	explainerChunkRunes    = 320
	explainerMaxShotRunes  = 80 // 防止整段挤进一镜；实际按语义和配音定时（一镜约 40～65 字，≈9～15 秒）
	explainerMinShotRunes  = 8  // 少于这个数的碎片并入相邻镜头
	explainerChunkParallel = 10
	// 开场加密区之后的镜头长度：不足 explainerTargetMin 的镜并入后一镜，合并后不超过 explainerTargetMax。
	explainerTargetMin  = 40
	explainerTargetMax  = 68
	explainerOpeningRun = 120 // fast_opening 时前约 120 实字（≈30 秒）保持短镜
)

const explainerSystemPrompt = `你是财经口播的视觉导演。观众是 50 岁以上的普通人，画面要一眼看懂：这是谁、在哪、正在做什么。系统另行指定画幅和统一画风。

先通读全文，知道开头、转折、高潮、结尾在哪，再逐段选画面。每镜不是把这段话全画出来，而是选出这段里最具体、最容易看懂、最值得被看到的一个现实瞬间。

切镜：一个完整意思一镜，通常约 40～65 字（念 9～15 秒），以完整句为主，在观点变化、举例、对比、转折处切开，最长 80 字；不足 20 字的短句并入相邻镜。不拆断金额、百分比、年份、课程名或因果关系。narration 必须逐字保留原文，所有镜头连起来与输入一致；不要替用户改写口播。

配图（按优先级）：
1. 一眼看懂优先：能直接画事实就不画象征。禁止拟人钞票、变小的钱、乌云、天平、锁链、空椅子、破碎镜子、多重叠影、概念海报这类要观众猜的画面。抽象的判断和总结，回到前后文找一个能承载它的真实人物、事件或生活场景来画。
2. 具体真实优先：选现实中真能拍到的场景——家里餐桌、小区门口、菜市场、银行营业厅、医院缴费窗口、单位、街道、车站、店铺。人物 + 动作 + 环境是默认结构；只有确实需要空镜或物件特写时，才在 scene 明确“无人入镜”。
3. 人物要有明确动作（数存折、翻保单、排队、打电话、劝人、指着什么、回头看），不能机械站着。人物按语境定身份和年龄：观众自己一辈用五六十岁的中国普通人，子女用年轻成年人，孙辈用孩子；朴素日常着装，自然皮肤和体态，不摆拍、不磨皮、不夸张愁容。
4. 视觉起伏：开头前三镜、转折、高潮、课尾这些位置用更明确的动作、更强的情绪或更大的场面；过渡段可以平静。连续三镜不能同时出现同一人物 + 同一地点 + 同一景别，主动换场景、人数、景别、机位、光线。远景交代环境和人群，中景交代行为，近景交代情绪，特写交代物件和手部动作，俯拍交代规模，低机位交代建筑和车辆。
5. 一镜只有一个视觉中心：1 个主视觉 + 少量辅助元素，不把多个时间、地点、事件塞进一张图，约 1 秒内能看懂。
6. scene 按“地点 + 主体 + 动作 + 环境 + 景别 + 光线”写成可直接生图的描述，70～110 字。日期、金额、利率、课程名交给后期字幕，图片中不生成任何可读文字、假界面或数据图表；场景是示意，不伪装新闻现场。
7. camera_move 选择 still / zoom_in / zoom_out / pan_left / pan_right：人物情绪和物件细节推近，环境和人群平移，对比可静止；运动服务主体，不按顺序轮换。这里只做静帧镜头运动，不承诺人物真的行动。
6. keywords 选0～3个在本镜旁白中逐字存在的连续词组：number 数值含单位，risk 风险词，concept 核心概念。金额、0.95%、2007年等保持完整；不要整句变黄。
7. annotation 是可选的屏幕重点标注，通常空；关键转折、对比或总结时写一个短语，不重复整条字幕，不新增结论、不杜撰数字，也不写操作指令。标注由后期独立文字轨生成。

只返回 JSON：{"shots":[{"narration":"原句","visual_intent":"原句与画面的对应理由","subject_type":"object/environment/person/comparison/metaphor","subject":"具体主体","scene":"可见场景","camera_move":"zoom_in","keywords":[{"text":"旁白中原词","kind":"concept"}],"annotation":""}]}`

type explainerReply struct {
	Shots []struct {
		StyleKey     string        `json:"style_key"`
		Narration    string        `json:"narration"`
		Subject      string        `json:"subject"`
		Scene        string        `json:"scene"`
		VisualIntent string        `json:"visual_intent"`
		SubjectType  string        `json:"subject_type"`
		CameraMove   string        `json:"camera_move"`
		Motion       string        `json:"motion"`
		Keywords     []ShotKeyword `json:"keywords"`
		Annotation   string        `json:"annotation"`
	} `json:"shots"`
}

// buildExplainerStoryboard 把 short.Story 拆成解说镜头，写回 short.Shots。
// 两段式：先由 segmentModel 按话题把整篇切成几个大段（空则按段落/字数机械切），
// 再由 model 对每个大段并行拆镜。大段之间互不依赖，几十秒就能拆完一篇。
func buildExplainerStoryboard(ctx context.Context, chat openaicompat.ChatClient, model, segmentModel string, short *Short) error {
	started := time.Now()
	// 只给没写思考强度的请求补上用户选的强度：分大段和重试用的 "low" 要保住，不然一篇稿 3 分钟大半耗在这。
	chat = reasoningClient{ChatClient: chat, effort: reasoningOf(short.TextReasoningEffort)}
	chunks := segmentStory(ctx, chat, segmentModel, short.Story)
	if len(chunks) == 0 {
		return errors.New("文案为空")
	}
	slog.Default().Info("ai short storyboard: start", "chunks", len(chunks), "runes", substantiveRunes(short.Story), "segment_model", segmentModel, "segment_elapsed", time.Since(started).Round(time.Second))
	results := make([][]Shot, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, explainerChunkParallel)
	prefixRunes := 0
	fastOpening := short.VisualSettings != nil && short.VisualSettings.FastOpening
	for i, chunk := range chunks {
		openingRunes := 0
		if fastOpening {
			openingRunes = max(0, explainerOpeningRun-prefixRunes)
		}
		prefixRunes += substantiveRunes(chunk)
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, chunk string, openingRunes int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = storyboardChunk(ctx, visualPlanningClient{ChatClient: chat, style: short.Style}, model, chunk, openingRunes)
		}(i, chunk, openingRunes)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	short.Characters = []Character{}
	short.Shots = short.Shots[:0]
	for _, part := range results {
		short.Shots = append(short.Shots, part...)
	}
	short.Shots = tidyExplainerShots(short.Shots)
	openingKeep := 0
	if fastOpening {
		openingKeep = explainerOpeningRun
	}
	short.Shots = balanceExplainerShots(short.Shots, openingKeep)
	finalizeExplainerShots(short)
	if len(short.Shots) == 0 {
		return errors.New("没拆出镜头")
	}
	slog.Default().Info("ai short storyboard: done", "chunks", len(chunks), "shots", len(short.Shots), "elapsed", time.Since(started).Round(time.Second), "effort", reasoningOf(short.TextReasoningEffort))
	return nil
}

const segmentSystemPrompt = `你是财经解说视频的编辑。把下面这篇口播文案按话题切成 4～10 个大段，每段 200～700 字，切在话题转换、论点切换、举例开始/结束这类自然边界上。
规则：每段必须是原文原句连续片段，不改字、不增删、不重排；所有段按顺序连起来必须严格等于原文。
只返回 JSON 对象：{"sections":["第一段原文","第二段原文"]}`

// segmentStory 用分段模型把整篇按话题切成大段；模型没给、失败或对不上原文时退回机械切。
// 超过 700 字的大段再按段落/字数细切，避免分镜模型一次输出太长丢句。
func segmentStory(ctx context.Context, chat openaicompat.ChatClient, model, story string) []string {
	mechanical := chunkStory(story, explainerChunkRunes)
	if strings.TrimSpace(model) == "" || chat == nil || len([]rune(squash(story))) < explainerChunkRunes {
		return mechanical
	}
	resp, err := chat.Chat(openaicompat.ChatRequest{
		Model:           model,
		Stream:          true,
		ReasoningEffort: "low",
		Messages: []openaicompat.Message{
			{Role: "system", Content: segmentSystemPrompt},
			{Role: "user", Content: "文案：\n" + strings.TrimSpace(story)},
		},
	})
	if err != nil || len(resp.Choices) == 0 {
		return mechanical
	}
	var reply struct {
		Sections []string `json:"sections"`
	}
	if json.Unmarshal([]byte(extractJSONObject(resp.Choices[0].Message.Content)), &reply) != nil || len(reply.Sections) == 0 {
		return mechanical
	}
	var joined strings.Builder
	for _, s := range reply.Sections {
		joined.WriteString(s)
	}
	if squash(joined.String()) != squash(story) {
		return mechanical
	}
	var out []string
	for _, s := range reply.Sections {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if len([]rune(s)) > 700 {
			out = append(out, chunkStory(s, explainerChunkRunes)...)
		} else {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return mechanical
	}
	return out
}

// storyboardChunk 拆一块文案。校验只看实字（标点、空白不算）：模型常把"、"写成"，"或丢个引号，
// 这不该让整块作废；实字对上就按模型给的切点从原文重新截出 narration（标点保留原文）。
// 实字真对不上才重试；三次都不行就机械切，但把模型写好的画面描述按内容匹配借过来，
// 不再出现"画面 = 旁白原句"这种没法生图的镜头。
func storyboardChunk(ctx context.Context, chat openaicompat.ChatClient, model, chunk string, openingRunes ...int) ([]Shot, error) {
	var lastErr error
	var hints []Shot
	started := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		// 第一次按用户选的思考强度；重试只是为了把原文对齐，用 low 快出结果，画面描述已经有第一次的 hints 兜底。
		effort := ""
		if attempt > 0 {
			effort = "low"
		}
		shots, err := askExplainerModel(ctx, chat, model, chunk, effort, openingRunes...)
		if err != nil {
			lastErr = err
			continue
		}
		if len(shots) > len(hints) {
			hints = shots
		}
		if !narrationMatches(shots, chunk) {
			lastErr = errors.New("模型改动或漏掉了原文")
			slog.Default().Warn("ai short storyboard: narration mismatch, retrying", "attempt", attempt+1, "model", model, "elapsed", time.Since(started).Round(time.Second))
			continue
		}
		slog.Default().Info("ai short storyboard: chunk ok", "runes", substantiveRunes(chunk), "shots", len(shots), "attempts", attempt+1, "elapsed", time.Since(started).Round(time.Second))
		return splitOverlongShots(realignNarrations(shots, chunk)), nil
	}
	slog.Default().Warn("ai short storyboard: falling back to mechanical split", "model", model, "error", lastErr)
	shots := fallbackSplitWithHints(chunk, hints)
	if len(shots) == 0 {
		return nil, lastErr
	}
	return shots, nil
}

// realignNarrations 按模型各镜的实字数，从原文里顺序截出每镜的 narration，
// 把该镜后面紧跟的标点/空白也带上。这样切点是模型的，字和标点是原文的。
func realignNarrations(shots []Shot, chunk string) []Shot {
	runes := []rune(strings.TrimSpace(chunk))
	pos := 0
	out := make([]Shot, 0, len(shots))
	for i, shot := range shots {
		need := substantiveRunes(shot.Narration)
		start := pos
		got := 0
		for pos < len(runes) && (got < need || isPunctOrSpace(runes[pos])) {
			if !isPunctOrSpace(runes[pos]) {
				got++
			}
			pos++
			if got >= need && (pos >= len(runes) || !isPunctOrSpace(runes[pos])) {
				break
			}
		}
		if i == len(shots)-1 {
			pos = len(runes)
		}
		shot.Narration = strings.TrimSpace(string(runes[start:pos]))
		if shot.Narration != "" {
			out = append(out, shot)
		}
	}
	return out
}

// fallbackSplitWithHints 机械切句，再从模型给的（对不上原文的）镜头里按内容相似度借画面描述。
func fallbackSplitWithHints(chunk string, hints []Shot) []Shot {
	shots := fallbackSplit(chunk)
	if len(hints) == 0 {
		return shots
	}
	for i := range shots {
		best, bestScore := -1, 0
		for j, h := range hints {
			if strings.TrimSpace(h.Scene) == "" {
				continue
			}
			if score := bigramOverlap(shots[i].Narration, h.Narration); score > bestScore {
				best, bestScore = j, score
			}
		}
		// 至少共享 3 个二元组才算同一句，避免把别处的画面借错。
		if best >= 0 && bestScore >= 3 {
			shots[i].StyleKey = hints[best].StyleKey
			shots[i].Subject, shots[i].Scene = hints[best].Subject, hints[best].Scene
			shots[i].VisualIntent, shots[i].SubjectType, shots[i].CameraMove = hints[best].VisualIntent, hints[best].SubjectType, hints[best].CameraMove
			shots[i].Keywords = normalizedKeywords(shots[i].Narration, hints[best].Keywords)
			// 回退匹配不复制标注，避免把其他句的结论放在本镜。
		}
	}
	return shots
}

// bigramOverlap 数两段文字（只看实字）共享多少个相邻字对。
func bigramOverlap(a, b string) int {
	grams := func(s string) map[string]int {
		var rs []rune
		for _, r := range s {
			if !isPunctOrSpace(r) {
				rs = append(rs, r)
			}
		}
		m := map[string]int{}
		for i := 0; i+1 < len(rs); i++ {
			m[string(rs[i:i+2])]++
		}
		return m
	}
	ga, gb := grams(a), grams(b)
	n := 0
	for g, c := range ga {
		if cb, ok := gb[g]; ok {
			if cb < c {
				c = cb
			}
			n += c
		}
	}
	return n
}

// splitOverlongShots 把实字超过上限的镜按标点切成几镜，画面主体/描述沿用原镜。
func splitOverlongShots(shots []Shot) []Shot {
	out := make([]Shot, 0, len(shots))
	for _, shot := range shots {
		if substantiveRunes(shot.Narration) <= explainerMaxShotRunes {
			out = append(out, shot)
			continue
		}
		for _, piece := range splitLong(shot.Narration, explainerMaxShotRunes) {
			part := shot
			part.Narration = piece
			out = append(out, part)
		}
	}
	return out
}

// askExplainerModel 拆一块。effort 为空时由外层 reasoningClient 补成用户选的思考强度；重试传 "low"。
func askExplainerModel(ctx context.Context, chat openaicompat.ChatClient, model, chunk, effort string, openingRunes ...int) ([]Shot, error) {
	systemPrompt := explainerSystemPrompt + "\n额外返回 motion：基于本镜 scene 中已有主体的一项可行动态，供开场图生视频使用。建筑和物件优先用镜头移动、自然反光或已有环境变化；没有人物的场景不要新增手或人，不强迫每镜翻文件、递物品。"
	if len(openingRunes) > 0 && openingRunes[0] > 0 {
		systemPrompt += fmt.Sprintf("\n开场加密：本段最前约%d个实字属于前30秒附近（仅为策划估算，最终按配音对齐）。这些内容优先每10～16个实字一个完整意思，约2～4秒换一次可见主体或景别；避免连续重复画面，不切断数字、课程名和短语，不改原文。后面恢复通常 40～65 字一镜，不要再切成十几字的碎镜。", openingRunes[0])
	}
	resp, err := chat.Chat(openaicompat.ChatRequest{
		Model:           model,
		Stream:          true,
		ReasoningEffort: effort,
		Messages: []openaicompat.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "文案：\n" + strings.TrimSpace(chunk)},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("storyboard model returned no choices")
	}
	var reply explainerReply
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Choices[0].Message.Content)), &reply); err != nil {
		return nil, fmt.Errorf("storyboard JSON unparsable: %w", err)
	}
	out := make([]Shot, 0, len(reply.Shots))
	for _, s := range reply.Shots {
		line := strings.TrimSpace(s.Narration)
		if substantiveRunes(line) == 0 {
			// 只有标点或空白：模型偶尔把句号单独切出来，直接并回上一镜。
			if line != "" && len(out) > 0 {
				out[len(out)-1].Narration += line
			}
			continue
		}
		out = append(out, Shot{
			StyleKey:  strings.TrimSpace(s.StyleKey),
			Narration: line, Speaker: SpeakerNarrator, Subject: strings.TrimSpace(s.Subject), Scene: strings.TrimSpace(s.Scene),
			VisualIntent: strings.TrimSpace(s.VisualIntent), SubjectType: s.SubjectType, CameraMove: s.CameraMove,
			Motion:   strings.TrimSpace(s.Motion),
			Keywords: s.Keywords, Annotation: strings.TrimSpace(s.Annotation),
			Characters: []string{}, ImageStatus: ShotPending, VideoStatus: ShotPending,
		})
	}
	return out, nil
}

func shotsAreDetailed(shots []Shot) bool {
	for _, shot := range shots {
		if substantiveRunes(shot.Narration) > explainerMaxShotRunes {
			return false
		}
	}
	return len(shots) > 0
}

// substantiveRunes 数一句话里的实字：去掉空白和中英文标点后剩多少个字符。
func substantiveRunes(s string) int {
	n := 0
	for _, r := range s {
		if !isPunctOrSpace(r) {
			n++
		}
	}
	return n
}

func isPunctOrSpace(r rune) bool {
	switch r {
	case ' ', '\n', '\r', '\t', '\u3000',
		'，', '。', '！', '？', '；', '：', '、', '…', '—', '～', '·',
		'"', '\'', '\u201c', '\u201d', '\u2018', '\u2019', '「', '」', '『', '』', '（', '）', '《', '》', '〈', '〉', '【', '】',
		',', '.', '!', '?', ';', ':', '(', ')', '[', ']', '<', '>', '-', '_', '/', '\\', '|':
		return true
	}
	return false
}

// tidyExplainerShots 收拾模型切出来的碎片：只有标点的镜并回前一镜；
// 实字少于 explainerMinShotRunes 的镜并入前一镜（放不下再并入后一镜）。
// 合并时保留原文顺序，不丢一个字。
func tidyExplainerShots(shots []Shot) []Shot {
	// 第一轮：纯标点并回前一镜；开头就是标点的挂到第一个实镜前面。
	cleaned := make([]Shot, 0, len(shots))
	pendingPrefix := ""
	for _, shot := range shots {
		if substantiveRunes(shot.Narration) == 0 {
			if len(cleaned) > 0 {
				cleaned[len(cleaned)-1].Narration += shot.Narration
			} else {
				pendingPrefix += shot.Narration
			}
			continue
		}
		if pendingPrefix != "" {
			shot.Narration = pendingPrefix + shot.Narration
			pendingPrefix = ""
		}
		cleaned = append(cleaned, shot)
	}
	if len(cleaned) == 0 {
		return cleaned
	}
	// 第二轮：碎片并入邻镜，反复到没有可并的为止。
	for pass := 0; pass < 4; pass++ {
		merged := false
		out := make([]Shot, 0, len(cleaned))
		for i := 0; i < len(cleaned); i++ {
			cur := cleaned[i]
			if substantiveRunes(cur.Narration) >= explainerMinShotRunes || len(cleaned) == 1 {
				out = append(out, cur)
				continue
			}
			// 优先并入前一镜（碎片多是句尾），前一镜放不下再并入后一镜。
			if len(out) > 0 && substantiveRunes(out[len(out)-1].Narration)+substantiveRunes(cur.Narration) <= explainerMaxShotRunes {
				out[len(out)-1].Narration += cur.Narration
				merged = true
				continue
			}
			if i+1 < len(cleaned) && substantiveRunes(cleaned[i+1].Narration)+substantiveRunes(cur.Narration) <= explainerMaxShotRunes {
				// 碎片自己的画面描述通常没意义，沿用后一镜的主体和画面。
				cleaned[i+1].Narration = cur.Narration + cleaned[i+1].Narration
				merged = true
				continue
			}
			out = append(out, cur)
		}
		cleaned = out
		if !merged {
			break
		}
	}
	return cleaned
}

// balanceExplainerShots 把开场加密区之后的碎镜并成 40～68 实字一镜：模型常按句切出十几字的镜头，
// 每镜一图会让画面切得太碎、也让图片费用翻倍。前 openingKeep 个实字（约前 30 秒）保持原切法，
// 之后不足 explainerTargetMin 的镜并入后一镜，直到合并会超过 explainerTargetMax 为止；结尾的短尾并回前一镜。
// 合并后的画面沿用旁白更长那一镜的场景，keywords 合并后最多三个。
func balanceExplainerShots(shots []Shot, openingKeep int) []Shot {
	if len(shots) < 2 {
		return shots
	}
	out := make([]Shot, 0, len(shots))
	pos := 0
	for i := 0; i < len(shots); {
		cur := shots[i]
		start := pos
		pos += substantiveRunes(cur.Narration)
		i++
		if start < openingKeep {
			out = append(out, cur)
			continue
		}
		for i < len(shots) && substantiveRunes(cur.Narration) < explainerTargetMin &&
			substantiveRunes(cur.Narration)+substantiveRunes(shots[i].Narration) <= explainerTargetMax {
			cur = mergeShots(cur, shots[i])
			pos += substantiveRunes(shots[i].Narration)
			i++
		}
		out = append(out, cur)
	}
	// 结尾短尾（不到目标下限的一半）并回前一镜，前一镜不在开场区且合并后不超上限。
	if n := len(out); n >= 2 {
		last, prev := out[n-1], out[n-2]
		prevStart := 0
		for _, s := range out[:n-2] {
			prevStart += substantiveRunes(s.Narration)
		}
		if prevStart >= openingKeep && substantiveRunes(last.Narration) < explainerTargetMin/2 &&
			substantiveRunes(prev.Narration)+substantiveRunes(last.Narration) <= explainerTargetMax+8 {
			out[n-2] = mergeShots(prev, last)
			out = out[:n-1]
		}
	}
	return out
}

// mergeShots 把 b 并入 a：旁白顺序拼接；画面取旁白更长的一镜，平局取前一镜。
func mergeShots(a, b Shot) Shot {
	base, other := a, b
	if substantiveRunes(b.Narration) > substantiveRunes(a.Narration) {
		base, other = b, a
	}
	merged := base
	merged.Narration = a.Narration + b.Narration
	if merged.Annotation == "" {
		merged.Annotation = other.Annotation
	}
	merged.Hero = a.Hero || b.Hero
	seen := map[string]bool{}
	merged.Keywords = nil
	for _, kw := range append(append([]ShotKeyword{}, a.Keywords...), b.Keywords...) {
		if kw.Text == "" || seen[kw.Text] || len(merged.Keywords) >= 3 {
			continue
		}
		seen[kw.Text] = true
		merged.Keywords = append(merged.Keywords, kw)
	}
	merged.ImageStatus, merged.VideoStatus = ShotPending, ShotPending
	merged.ImagePath, merged.VideoPath = "", ""
	return merged
}

// narrationMatches 只比实字：忽略空白和标点，拼接结果与原文一致就算对上。
func narrationMatches(shots []Shot, chunk string) bool {
	var b strings.Builder
	for _, s := range shots {
		b.WriteString(s.Narration)
	}
	return substantiveOnly(b.String()) == substantiveOnly(chunk)
}

// substantiveOnly 去掉空白和标点，只留实字。
func substantiveOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if isPunctOrSpace(r) {
			return -1
		}
		return r
	}, s)
}

func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '\u3000' {
			return -1
		}
		return r
	}, s)
}

// fallbackSplit 按句号/问号/感叹号切句，长句再在标点处拆到 ≤22 字；画面先用原句顶着。
func fallbackSplit(chunk string) []Shot {
	var shots []Shot
	for _, sentence := range splitSentences(chunk) {
		for _, piece := range splitLong(sentence, explainerMaxShotRunes) {
			shots = append(shots, Shot{
				Narration: piece, Speaker: SpeakerNarrator, VisualIntent: "分镜回退：原句已保留，请补充具体主体与画面。",
				StyleKey: ExplainerStyles[0].Key, Characters: []string{},
				ImageStatus: ShotPending, VideoStatus: ShotPending,
			})
		}
	}
	return tidyExplainerShots(shots)
}

func splitSentences(text string) []string {
	var out []string
	var cur []rune
	for _, r := range strings.TrimSpace(text) {
		if r == '\n' || r == '\r' {
			if s := strings.TrimSpace(string(cur)); s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, r)
		if r == '。' || r == '！' || r == '？' || r == '!' || r == '?' {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	if s := strings.TrimSpace(string(cur)); s != "" {
		out = append(out, s)
	}
	return out
}

func splitLong(sentence string, max int) []string {
	runes := []rune(sentence)
	if len(runes) <= max {
		return []string{sentence}
	}
	var out []string
	start := 0
	for start < len(runes) {
		end := start + max
		if end >= len(runes) {
			out = append(out, string(runes[start:]))
			break
		}
		cut := -1
		for i := end; i > start+8; i-- {
			if numericPunctuation(runes, i-1) {
				continue
			}
			if runes[i-1] == '，' || runes[i-1] == '；' || runes[i-1] == '：' ||
				runes[i-1] == '、' || runes[i-1] == ',' || runes[i-1] == ';' || runes[i-1] == ':' {
				cut = i
				break
			}
		}
		if cut < 0 {
			cut = end
		}
		cut = protectFinancialCut(runes, start, cut)
		out = append(out, string(runes[start:cut]))
		start = cut
	}
	return out
}

// chunkStory 按空行分段，再把相邻段落攒到 ≤max 字一块；单段超长就按句子攒。
func chunkStory(story string, max int) []string {
	paragraphs := strings.Split(strings.ReplaceAll(strings.TrimSpace(story), "\r\n", "\n"), "\n")
	var units []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len([]rune(p)) <= max {
			units = append(units, p)
			continue
		}
		units = append(units, splitSentences(p)...)
	}
	var chunks []string
	var cur strings.Builder
	curLen := 0
	for _, u := range units {
		n := len([]rune(u))
		if curLen > 0 && curLen+n > max {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curLen = 0
		}
		if curLen > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(u)
		curLen += n
	}
	if curLen > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

// finalizeExplainerShots 编号、按旁白打镜头角色、解析单镜画风（分段画风在这里生效）并补齐镜头运动，清除旧视频状态。
func finalizeExplainerShots(short *Short) {
	shots := short.Shots
	assignShotRoles(shots, segmentStylesOf(short).openingShots())
	for i := range shots {
		shots[i].Index = i
		shots[i].StylePinned = false // 重新拆的镜头是新镜头，没有手动定过的画风
		shots[i].StyleKey = resolvedShotStyleFor(short, shots[i])
		shots[i].SourceText = shots[i].Narration
		if !validCameraMove(shots[i].CameraMove) {
			switch shots[i].SubjectType {
			case "environment":
				shots[i].CameraMove = "pan_left"
			case "comparison":
				shots[i].CameraMove = "still"
			default:
				shots[i].CameraMove = "zoom_in"
			}
		}
		shots[i].Hero = false
		shots[i].Seconds = explainerSecondsForLine(shots[i].Narration)
		shots[i].VideoPath, shots[i].VideoStatus, shots[i].VideoRequestID, shots[i].VideoPrompt = "", ShotPending, "", ""
	}
}

// explainerSecondsForLine 按旁白 TTS 的实际语速估这镜要多长的视频：
// 实测约 0.21 秒/字（含气口），按 0.23 留点余量，取够用的最小档，多出来的在草稿里截掉。
// 别按角色说台词那套（secondsForLine）算——那是给视频里开口说话留的时间，解说镜不需要。
func explainerSecondsForLine(line string) int {
	need := float64(len([]rune(strings.TrimSpace(line))))*0.23 + shotGapSeconds
	switch {
	case need <= 6:
		return 6
	case need <= 10:
		return 10
	default:
		return 15
	}
}

// fillShotPrompts 把第 i 镜将要发给生图/生视频模型的提示词预先算好写进 Shot，
// 让人在花钱生成之前就能在卡片上看到；改描述后立刻能看到新的。
// 真正生成时会再写一次实际用的（解说模式带参考图时多一句）。
func fillShotPrompts(short *Short, i int) {
	if i < 0 || i >= len(short.Shots) {
		return
	}
	if short.IsExplainer() {
		normalizeVisualShot(short, i)
		short.Shots[i].ImagePrompt = explainerImagePrompt(short.Shots[i])
		short.Shots[i].VideoPrompt = ""
		if short.NeedsVideo(short.Shots[i]) {
			short.Shots[i].VideoPrompt = explainerVideoPrompt(short.Shots[i])
		}
		return
	}
	shot := short.Shots[i]
	short.Shots[i].ImagePrompt = shotImagePrompt(short.Style, shot, short.Characters)
	short.Shots[i].VideoPrompt = shotVideoPrompt(shot, short.Characters)
}

// fillAllPrompts 给全部镜头预算提示词；onlyEmpty 时只补缺的（读老记录用），不覆盖生成时记下的实际值。
func fillAllPrompts(short *Short, onlyEmpty bool) {
	for i := range short.Shots {
		if onlyEmpty && short.Shots[i].ImagePrompt != "" {
			continue
		}
		fillShotPrompts(short, i)
	}
}

// 生图提示词的固定条款。写短：生图模型对长提示词的后半段基本不看，关键约束各一句、放在最后。
const (
	// 禁字：点名最爱长乱码的载体，并给替代画法。
	explainerNoTextRule = "禁止任何文字、字母、数字、乱码或类似文字的笔画，包括文件、屏幕、招牌、标签上——这些地方留白或用色块、线条代替。"
	// 画面描述里写了人才附：人物约束。
	explainerPeopleRule = "人物：按场景指定年龄与动作表现，未指定时为普通中国成年人，朴素日常着装，自然皮肤与体态；不要模特摆拍。"
	// 平台审核红线。
	explainerSafetyRule = "禁止国徽、国旗、领导人像、警察或军人制服。不要自行添加印章、图章、落款或角标；只有画面中的文件确实需要盖章时，才用一枚无字的普通红色圆章。"
)

// explainerImagePrompt 保留策划的主体、动作和关系，避免附加相反的主体约束：
// 画面内容 → 构图 → 风格 → 三条硬约束（禁字、人物、审核）。旁白原句不放进来——实测会被原样印进图里。
func explainerImagePrompt(shot Shot) string {
	preset := StyleByKey(shot.StyleKey)
	scene := strings.TrimSpace(shot.Scene)
	if scene == "" {
		scene = strings.TrimSpace(shot.Subject)
	}
	var b strings.Builder
	if subject := strings.TrimSpace(shot.Subject); subject != "" && !strings.Contains(scene, subject) {
		b.WriteString("主体：" + subject + "。")
	}
	b.WriteString("画面：" + strings.TrimRight(scene, "。") + "。")
	if shot.VisualIntent != "" {
		b.WriteString("画面意图（只转为可见关系，不渲染文字）：" + shot.VisualIntent + "。")
	}
	if shot.AspectRatio == "9:16" {
		// 之前写"下方五分之一留白"，实测出来的图底部是一大块空桌面/空地板；改成主体偏上、纵深填满。
		// 不在提示词里提"字幕"：09-07 实测提了之后模型在底部自己印了一行乱码字幕。
		b.WriteString("构图：9:16 原生竖屏全幅，真正按竖屏设计：主体放在画面中上部，利用道路、桌面、走廊、站立人物的纵深把画面填满，前景—中景—远景层次清楚；最下方约六分之一只延续地面、桌面或环境，不放脸和关键动作，也不要留成空白。")
	} else {
		b.WriteString("构图：16:9 横屏，主体清楚、环境简洁，下缘留出自然的字幕空间。")
	}
	b.WriteString(preset.Prompt)
	b.WriteString(explainerNoTextRule)
	if preset.Key == "macro_money" {
		// 分段画风把"讲钱的镜"换成静物时，scene 多半还是拆镜时按底色写的人物场景；这里明说改成静物，免得人脸和"不出现人脸"打架。
		b.WriteString("本镜改为静物特写：人物不入镜，把场景里的人和动作换成桌面上的钱、存折、票据、老花镜、茶杯和手部局部，保留原场景的光线和环境感。")
	} else if shot.SubjectType == "person" || mentionsPeople(scene) || mentionsPeople(shot.Subject) {
		b.WriteString(styledPeopleRule(shot.StyleKey))
	}
	b.WriteString(explainerSafetyRule)
	return b.String()
}

// StylePreviewPrompt 用生产同一条提示词链路，给某套画风出"参考图"用的提示词：
// 同一个测试场景 + 该画风 + 三条硬约束，这样前端缩略图和实际出图是一回事。
// scripts/ai-shorts/render_style_previews.py 通过 /api/ai-shorts/style-preview-prompts 取它。
func StylePreviewPrompt(styleKey, scene string) string {
	key := StyleByKey(styleKey).Key
	if styleKey == financeEditorial {
		key = "paper_collage" // 混合策略没有自己的画风，参考图用它的主表达
	}
	shot := Shot{StyleKey: key, Scene: strings.TrimSpace(scene), AspectRatio: "9:16"}
	if mentionsPeople(shot.Scene) {
		shot.SubjectType = "person"
	}
	return explainerImagePrompt(shot)
}

// mentionsPeople 仅决定是否补充人物质感说明；未识别到人物不会触发禁人指令。
// 先剔掉"手机""人民币"这类带"人/手"却不是人的词。
func mentionsPeople(text string) bool {
	for _, notPerson := range []string{"手机", "人民币", "人行道", "人民", "无人", "没人", "不要人", "不出现人", "机器人", "手写", "手工", "家庭", "个人", "人均", "人工智能"} {
		text = strings.ReplaceAll(text, notPerson, "")
	}
	for _, k := range []string{"人物", "上班族", "青年", "子女", "父母", "儿子", "女儿", "闺女", "工人", "同事", "男人", "女人", "男性", "女性", "爷爷", "奶奶", "大叔", "阿姨", "妈妈", "爸爸", "老两口", "夫妻", "工作人员", "职员", "手部", "双手", "一只手", "脸", "背影", "身影", "顾客", "老板", "孩子", "老人", "中年人", "年轻人", "人群"} {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// explainerVideoPrompt 是重点镜图生视频的提示词：一个正常速度的明确动作 + 镜头运动，不出人声。
// 之前写的是"轻微、缓慢"，出来的片子像慢动作；现在要求正常速度、动作说完为止。
func explainerVideoPrompt(shot Shot) string {
	motion := strings.TrimSpace(shot.Motion)
	if motion == "" {
		motion = "镜头平稳推近或轻柔横移，保留已有主体，反光和环境随视角自然变化；建筑保持稳定，物件不变形，没有人物时不新增人物或手"
	}
	var b strings.Builder
	b.WriteString("以这张图为第一帧，动作：" + strings.TrimRight(motion, "。") + "。")
	if editorialShotStyle(shot.StyleKey) {
		b.WriteString("保持纸片或模型材质，用轻微层次视差、整体推拉或已有元素的小幅移动表达动作；不要变成真人实拍，不改变元素数量、形状或图中关系。")
	} else {
		b.WriteString("动作按真实生活的正常速度进行，不要慢动作、不要定格，动作在几秒内自然完成后画面停稳。")
	}
	b.WriteString("画风、构图、光线、配色与第一帧完全一致，不出现新的人或物，不出现文字。")
	b.WriteString("AUDIO: 只有轻的环境音，没有任何人声、没有说话、没有旁白、没有音乐。")
	return b.String()
}
