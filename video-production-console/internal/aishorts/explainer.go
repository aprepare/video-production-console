package aishorts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"video-production-console/internal/agentruntime/openaicompat"
)

// 解说模式的拆分镜。文案通常 1500～3000 字（念 6～12 分钟），一次喂给模型
// 输出几十镜的 JSON 容易丢句，所以按段落切成 ≤ explainerChunkRunes 字的块，
// 各块并行拆，再按原顺序拼起来。每块拆完都核对「全部 narration 连起来 == 原文」，
// 不等就重试一次，还不等就退回按句号硬切，保证一个字都不丢。
const (
	explainerChunkRunes    = 450
	explainerMaxShotRunes  = 30 // 一镜最多多少个实字（不含标点）
	explainerMinShotRunes  = 8  // 少于这个数的碎片并入相邻镜头
	explainerChunkParallel = 6
)

const explainerSystemPrompt = `你是财经解说视频的分镜师。观众是中老年人，视频横屏 16:9，画面是一张张 AI 生成的图配旁白。输入是一段口播文案，输出 JSON。

切镜规则：
- 一镜 = 一个完整的意思，通常是一个短句或一个分句，目标 14～26 字，最多 30 字（约 3～6 秒换一张图）。
- 不要为了拆而拆：一个意思说完之前不要切；少于 8 个字的碎片（如「那我换一个问法：」「现在全被钉死了。」）必须并入前一镜或后一镜，绝不能单独成镜；一个标点、一个词绝不能单独成镜。
- 一句话超过 30 字时才在逗号、分号、冒号处切开；切开后每一半都要能独立配一张图。
- narration 必须是原文原句，不改字、不增删、不合并跨段落的句子；全部 narration 按顺序连起来必须严格等于输入原文。
- 并列结构（第一/第二/第三、归属/定价/分配）各自成镜；因果和转折（因为…所以…、虽然…但是…）如果两半都很短就合成一镜。

每镜字段：
- subject：画面主体，6～12 字，一眼能和这句话对上的那个东西（如「桌上三份盖红章的文件」「银行柜台上的一叠现金」「关上的铁门」）。
- scene：画面描述 30～60 字，规则：
  1. 直白优先。画面里必须出现这句话提到的具体名词；观众看图就能猜到这句话在说什么。不要文艺隐喻，不要"像……一样"，不要只拍情绪，不要多场景叠加。
  2. 一镜一事：一个主体、一个状态或动作。
  3. 尽量不画人。能用物件、场景、环境表达的，就不要出现人物（文件、印章、存折、现金、手机、柜台、门、街道、房子、餐桌都比人更直白）。十镜里出现人物的不超过三镜。
  4. 确实需要人（讲某个人的处境、动作、情绪）时，只出现一位，写成「一位五十多岁的中国男人」「六十岁上下的中国大妈」这类，穿着朴素；不要外国人面孔，不要年轻模特脸；文案明确提到年轻人、孩子时才出现其他年龄。
  5. 抽象词的固定译法：政策/文件→桌上盖红章的正式文件；数据/记录→手机屏幕上的点赞、定位、聊天图标；钱/收益→柜台上的现金、存折；归属→贴名字标签的文件夹；定价→价签、天平；分配→切开的蛋糕；机会/窗口→敞开的门透进光；错过→关上的门、开走的列车；跟风→排队的人群（背影即可）。
  6. 写法顺序：先主体，再状态/动作，再环境和光线。
  7. 禁止：警察、军人、制服人员、国旗国徽、领导人、文字、数字、图表、K线。政府机关只用"大楼外观"或"文件与公章"表示。

全片画风由系统统一添加，你不要选择画风。只返回 JSON 对象：
{"shots":[{"narration":"","subject":"","scene":""}]}`

type explainerReply struct {
	Shots []struct {
		Narration string `json:"narration"`
		Subject   string `json:"subject"`
		Scene     string `json:"scene"`
	} `json:"shots"`
}

// buildExplainerStoryboard 把 short.Story 拆成解说镜头，写回 short.Shots。
// 两段式：先由 segmentModel 按话题把整篇切成几个大段（空则按段落/字数机械切），
// 再由 model 对每个大段并行拆镜。大段之间互不依赖，几十秒就能拆完一篇。
func buildExplainerStoryboard(ctx context.Context, chat openaicompat.ChatClient, model, segmentModel string, short *Short) error {
	chunks := segmentStory(ctx, chat, segmentModel, short.Story)
	if len(chunks) == 0 {
		return errors.New("文案为空")
	}
	results := make([][]Shot, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, explainerChunkParallel)
	for i, chunk := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, chunk string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = storyboardChunk(ctx, chat, model, chunk)
		}(i, chunk)
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
	finalizeExplainerShots(short.Shots, StyleByKey(short.Style).Key)
	if len(short.Shots) == 0 {
		return errors.New("没拆出镜头")
	}
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
func storyboardChunk(ctx context.Context, chat openaicompat.ChatClient, model, chunk string) ([]Shot, error) {
	var lastErr error
	var hints []Shot
	for attempt := 0; attempt < 3; attempt++ {
		shots, err := askExplainerModel(ctx, chat, model, chunk)
		if err != nil {
			lastErr = err
			continue
		}
		if len(shots) > len(hints) {
			hints = shots
		}
		if !narrationMatches(shots, chunk) {
			lastErr = errors.New("模型改动或漏掉了原文")
			slog.Default().Warn("ai short storyboard: narration mismatch, retrying", "attempt", attempt+1, "model", model)
			continue
		}
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
			shots[i].Subject, shots[i].Scene = hints[best].Subject, hints[best].Scene
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

func askExplainerModel(ctx context.Context, chat openaicompat.ChatClient, model, chunk string) ([]Shot, error) {
	resp, err := chat.Chat(openaicompat.ChatRequest{
		Model:           model,
		Stream:          true,
		ReasoningEffort: "low",
		Messages: []openaicompat.Message{
			{Role: "system", Content: explainerSystemPrompt},
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
			Narration: line, Speaker: SpeakerNarrator, Subject: strings.TrimSpace(s.Subject), Scene: strings.TrimSpace(s.Scene),
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
				Narration: piece, Speaker: SpeakerNarrator, Scene: piece,
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
			if runes[i-1] == '，' || runes[i-1] == '；' || runes[i-1] == '：' ||
				runes[i-1] == '、' || runes[i-1] == ',' || runes[i-1] == ';' || runes[i-1] == ':' {
				cut = i
				break
			}
		}
		if cut < 0 {
			cut = end
		}
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

// finalizeExplainerShots 编号、统一全片画风、轮转推拉类型。解说模式只用图片，不生成视频。
func finalizeExplainerShots(shots []Shot, styleKey string) {
	styleKey = StyleByKey(styleKey).Key
	for i := range shots {
		shots[i].Index = i
		shots[i].StyleKey = styleKey
		shots[i].CameraMove = CameraMoves[i%len(CameraMoves)]
		shots[i].Hero = false
		shots[i].Seconds = 6
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
	shot := short.Shots[i]
	if short.IsExplainer() {
		short.Shots[i].ImagePrompt = explainerImagePrompt(shot)
		short.Shots[i].VideoPrompt = ""
		return
	}
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
	explainerPeopleRule = "人物：必须是中国中老年人（45～70 岁），朴素日常着装，一位为宜；不要外国面孔、不要年轻模特脸。"
	// 画面描述里没写人就明确禁人：不说的话，"纪实/新闻感"这类风格词会让模型自己往画里加人。
	explainerNoPeopleRule = "画面中不要出现任何人物、人脸、人体、手或人影，只画物件、建筑和场景。"
	// 平台审核红线。
	explainerSafetyRule = "禁止国徽、国旗、领导人像、警察或军人制服；印章只能是普通红色圆章。"
)

// explainerImagePrompt 是解说镜的生图提示词，控制在 200 字以内：
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
	b.WriteString("构图：单一主体、简洁、主体占画面大部分，16:9 横屏。")
	b.WriteString(preset.Prompt)
	b.WriteString(explainerNoTextRule)
	if mentionsPeople(scene) || mentionsPeople(shot.Subject) {
		b.WriteString(explainerPeopleRule)
	} else {
		b.WriteString(explainerNoPeopleRule)
	}
	b.WriteString(explainerSafetyRule)
	return b.String()
}

// mentionsPeople 粗判画面描述里有没有人：有就附人物约束，没有就明确禁人。
// 先剔掉"手机""人民币"这类带"人/手"却不是人的词。
func mentionsPeople(text string) bool {
	for _, notPerson := range []string{"手机", "人民币", "人行道", "人民", "无人", "没人", "不要人", "不出现人", "机器人"} {
		text = strings.ReplaceAll(text, notPerson, "")
	}
	for _, k := range []string{"人", "男", "女", "爷", "奶", "叔", "姨", "妈", "爸", "老两口", "夫妻", "员", "手", "脸", "他", "她", "们", "背影", "身影", "顾客", "老板", "家庭", "孩子", "老人"} {
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
		motion = "画面里的人或物按正常速度完成一个自然动作（翻开文件、抬头、递东西、按下印章），同时镜头缓慢推近"
	}
	var b strings.Builder
	b.WriteString("以这张图为第一帧，动作：" + strings.TrimRight(motion, "。") + "。")
	b.WriteString("动作按真实生活的正常速度进行，不要慢动作、不要定格，动作在几秒内自然完成后画面停稳。")
	b.WriteString("画风、构图、光线、配色与第一帧完全一致，不出现新的人或物，不出现文字。")
	b.WriteString("AUDIO: 只有轻的环境音，没有任何人声、没有说话、没有旁白、没有音乐。")
	return b.String()
}
