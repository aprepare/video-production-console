package aishorts

import (
	"encoding/json"
	"strings"
	"testing"

	"video-production-console/internal/narration"
)

func TestApplyStoryboardReplySpeakersAndVoices(t *testing.T) {
	var reply storyboardReply
	raw := `{"headline":"h","characters":[{"name":"狐厨","description":"d","voice":"狡诈"},{"name":"公鸡","description":"d","voice":"胡说的标签"}],
	"shots":[{"narration":"a","speaker":"狐厨"},{"narration":"b","speaker":"路人"},{"narration":"c"},{"narration":"d","speaker":"旁白"}]}`
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		t.Fatal(err)
	}
	short := &Short{}
	if err := applyStoryboardReply(short, reply); err != nil {
		t.Fatal(err)
	}
	if short.Characters[0].Voice != "狡诈" || short.Characters[1].Voice != "" {
		t.Fatalf("voice tags = %q / %q", short.Characters[0].Voice, short.Characters[1].Voice)
	}
	got := []string{short.Shots[0].Speaker, short.Shots[1].Speaker, short.Shots[2].Speaker, short.Shots[3].Speaker}
	want := []string{"狐厨", SpeakerNarrator, SpeakerNarrator, SpeakerNarrator}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("speakers = %v, want %v", got, want)
	}
}

func TestShotVideoPromptSpeaksCharacterLine(t *testing.T) {
	chars := []Character{{Name: "狐厨", Voice: "狡诈"}}
	spoken := shotVideoPrompt(Shot{Speaker: "狐厨", Narration: "自愿入局怨不得别人。", Motion: "狐厨抬头"}, chars)
	for _, want := range []string{`"自愿入局怨不得别人。"`, "狐厨", "普通话", "油滑", "AUDIO:"} {
		if !strings.Contains(spoken, want) {
			t.Fatalf("spoken prompt missing %q:\n%s", want, spoken)
		}
	}
	silent := shotVideoPrompt(Shot{Speaker: SpeakerNarrator, Narration: "他们以为自己在飞跃。", Motion: "大远景"}, chars)
	if strings.Contains(silent, "他们以为") || !strings.Contains(silent, "没有任何人声") {
		t.Fatalf("narrator shot must not put the line into the video prompt:\n%s", silent)
	}
}

func TestSecondsForLine(t *testing.T) {
	cases := map[string]int{"若不敢跳，便永世为鸡。": 6, "只要他们笃信崖上标语，我们便有吃不完的肉，喝不尽的汤，哈哈哈。": 10}
	for line, want := range cases {
		if got := secondsForLine(line); got != want {
			t.Fatalf("secondsForLine(%q) = %d, want %d", line, got, want)
		}
	}
	if got := secondsForLine(strings.Repeat("字", 40)); got != 15 {
		t.Fatalf("40 runes -> %d, want 15", got)
	}
}

func TestSpreadCaptions(t *testing.T) {
	caps := spreadCaptions("只要他们笃信崖上标语，我们便有吃不完的肉，喝不尽的汤，哈哈哈。", 10, 20)
	texts := make([]string, 0, len(caps))
	for _, c := range caps {
		texts = append(texts, c.Text)
	}
	if strings.Join(texts, "|") != "只要他们笃信崖上标语|我们便有吃不完的肉|喝不尽的汤|哈哈哈" {
		t.Fatalf("pieces = %v", texts)
	}
	if caps[0].StartS < 10 || caps[len(caps)-1].EndS > 20.001 {
		t.Fatalf("captions leak outside the shot: %+v", caps)
	}
	for i := 1; i < len(caps); i++ {
		if caps[i].StartS < caps[i-1].EndS-1e-9 {
			t.Fatalf("captions overlap: %+v", caps)
		}
	}
	// 长句无标点：均分成几段，每段 ≤ 上限、长短接近（不是 16+16+3）。
	long := spreadCaptions(strings.Repeat("字", 35), 0, 10)
	if len(long) != 3 {
		t.Fatalf("hard split = %+v", long)
	}
	for _, c := range long {
		if n := len([]rune(c.Text)); n > captionMaxRunes || n < 11 {
			t.Fatalf("unbalanced piece %q in %+v", c.Text, long)
		}
	}
	// 竖版 10 字上限：15 字短语切成 8+7。
	small := splitCaptionPiecesN("这是一句十五字长的旁白文案内容", 10)
	if len(small) != 2 || len([]rune(small[0])) != 8 || len([]rune(small[1])) != 7 {
		t.Fatalf("portrait split = %+v", small)
	}
}

func TestSplitClausesKeepsWholeClauses(t *testing.T) {
	got := splitClauses("一夜之间，你在网上留下的每一条痕迹，买过啥、看过啥、去过哪、跟谁聊过天，全都能被编号、被估价、被交易。")
	want := []string{"一夜之间", "你在网上留下的每一条痕迹", "买过啥、看过啥、去过哪、跟谁聊过天", "全都能被编号、被估价、被交易"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("clauses = %v", got)
	}
	// 超过 32 字的分句才均分。
	long := splitClauses(strings.Repeat("字", 40))
	if len(long) != 2 || len([]rune(long[0])) != 20 {
		t.Fatalf("long clause split = %v", long)
	}
}

func TestNarrationMatchIgnoresPunctuationAndRealigns(t *testing.T) {
	chunk := "一夜之间，你在网上留下的每一条痕迹，买过啥、看过啥、去过哪、跟谁聊过天，全都能被编号。"
	// 模型把"、"写成"，"，还丢了句号——实字一致就该算对上。
	shots := []Shot{
		{Narration: "一夜之间，你在网上留下的每一条痕迹", Scene: "A"},
		{Narration: "买过啥，看过啥，去过哪，跟谁聊过天", Scene: "B"},
		{Narration: "全都能被编号", Scene: "C"},
	}
	if !narrationMatches(shots, chunk) {
		t.Fatal("punctuation-only differences must not fail the match")
	}
	got := realignNarrations(shots, chunk)
	want := []string{"一夜之间，你在网上留下的每一条痕迹，", "买过啥、看过啥、去过哪、跟谁聊过天，", "全都能被编号。"}
	for i := range want {
		if got[i].Narration != want[i] || got[i].Scene != shots[i].Scene {
			t.Fatalf("realigned[%d] = %q (%s), want %q", i, got[i].Narration, got[i].Scene, want[i])
		}
	}
	// 真丢字才算不匹配。
	if narrationMatches([]Shot{{Narration: "一夜之间"}}, chunk) {
		t.Fatal("dropped text must fail the match")
	}
}

func TestFallbackBorrowsScenesFromHints(t *testing.T) {
	chunk := "第一道，数据到底归谁？财政部的分类文件给了答案。第二道，按什么标准定价？"
	hints := []Shot{
		{Narration: "第一道，数据到底归谁", Subject: "贴名字标签的文件夹", Scene: "档案柜前贴着名字标签的文件夹"},
		{Narration: "第二道，按什么标准定价", Subject: "天平和价签", Scene: "桌上的天平与价签"},
	}
	shots := fallbackSplitWithHints(chunk, hints)
	if len(shots) < 2 {
		t.Fatalf("shots = %+v", shots)
	}
	if shots[0].Scene != "档案柜前贴着名字标签的文件夹" {
		t.Fatalf("first shot did not borrow scene: %+v", shots[0])
	}
	last := shots[len(shots)-1]
	if last.Scene != "桌上的天平与价签" {
		t.Fatalf("last shot did not borrow scene: %+v", last)
	}
}

func TestSplitOverlongShotsKeepsScene(t *testing.T) {
	shots := []Shot{{Narration: strings.Repeat("一二三四五六七八九十，", 8), Scene: "桌上的文件", Subject: "文件"}}
	got := splitOverlongShots(shots)
	if len(got) < 2 {
		t.Fatalf("overlong shot not split: %+v", got)
	}
	for _, s := range got {
		if s.Scene != "桌上的文件" || substantiveRunes(s.Narration) > explainerMaxShotRunes {
			t.Fatalf("split shot lost scene or still too long: %+v", s)
		}
	}
}

func TestImagePromptCompactAndPeopleRuleConditional(t *testing.T) {
	noPeople := explainerImagePrompt(Shot{StyleKey: "documentary", Subject: "桌上三份盖红章的文件", Scene: "木桌上三份盖着红色圆章的文件和一部手机，晨光从窗口斜照"})
	if strings.Contains(noPeople, "中老年") {
		t.Fatalf("people rule must not be attached when no person in scene:\n%s", noPeople)
	}
	if strings.Contains(noPeople, "不要出现任何人物") {
		t.Fatalf("automatic blanket ban must not be attached:\n%s", noPeople)
	}
	if n := len([]rune(noPeople)); n > 400 {
		t.Fatalf("prompt too long (%d runes):\n%s", n, noPeople)
	}
	for _, want := range []string{"禁止任何文字", "国徽", "现实生活纪实摄影", "16:9"} {
		if !strings.Contains(noPeople, want) {
			t.Fatalf("prompt missing %q:\n%s", want, noPeople)
		}
	}
	withPeople := explainerImagePrompt(Shot{StyleKey: "documentary", Subject: "看手机的中年男人", Scene: "一位五十多岁的中国男人低头看手机"})
	if !strings.Contains(withPeople, "中国成年人") {
		t.Fatalf("people rule missing when scene has a person:\n%s", withPeople)
	}
}

func TestTightenCaptionsLeadAndGapless(t *testing.T) {
	caps := []jobCaption{{Text: "一", StartS: 1.0, EndS: 1.8}, {Text: "二", StartS: 2.1, EndS: 2.9}, {Text: "三", StartS: 5.0, EndS: 5.8}}
	got := tightenCaptions(caps, 6.0)
	if got[0].StartS != 0.9 { // 提前 0.1 秒
		t.Fatalf("lead not applied: %+v", got[0])
	}
	if got[0].EndS != got[1].StartS { // 0.3 秒的小空隙被填平
		t.Fatalf("small gap not closed: %+v", got[:2])
	}
	if got[1].EndS >= got[2].StartS-1.0 { // 2 秒的大空隙只多停 0.3 秒，不硬撑
		t.Fatalf("large gap should not be bridged: %+v", got[1:])
	}
	if got[2].EndS > 6.0 {
		t.Fatalf("last caption exceeds total: %+v", got[2])
	}
}

func TestPlanSFXRespectsGapAndRoles(t *testing.T) {
	brand := DefaultBrandKit()
	brand.SFX = map[string]string{"opening": "o.mp3", "whoosh": "w.mp3", "conclusion": "c.mp3"}
	shots := []Shot{
		{Narration: "开场第一句。"},
		{Narration: "但是这里有个转折，"}, // 太靠前，离开头不到 12 秒，不放
		{Narration: "第一道，数据归谁？"},
		{Narration: "说白了就是一句话。"},
	}
	times := [][2]float64{{0, 5}, {5, 15}, {15, 30}, {30, 100}}
	cues := planSFX(shots, times, 100, brand)
	if len(cues) != 3 || cues[0].Role != "opening" || cues[0].AtS != 0 {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[1].Role != "whoosh" || cues[1].AtS != 15 || cues[2].Role != "conclusion" || cues[2].AtS != 30 {
		t.Fatalf("cues = %+v", cues)
	}
	for i := 1; i < len(cues); i++ {
		if cues[i].AtS-cues[i-1].AtS < 12 {
			t.Fatalf("sfx too close: %+v", cues)
		}
	}
}

func TestChunkStoryKeepsEveryRune(t *testing.T) {
	para := strings.Repeat("这是一句凑长度的文案。", 20) // 220 字
	story := para + "\n\n" + para + "\n\n" + para + "\n\n" + para
	chunks := chunkStory(story, 500)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2 (%v)", len(chunks), chunks)
	}
	if squash(strings.Join(chunks, "")) != squash(story) {
		t.Fatal("chunking lost text")
	}
	// 单段超过上限时按句子攒，仍不丢字。
	long := strings.Repeat("超长段落里的一句话。", 80)
	if squash(strings.Join(chunkStory(long, 300), "")) != squash(long) {
		t.Fatal("long paragraph chunking lost text")
	}
}

func TestFallbackSplitRespectsLimit(t *testing.T) {
	text := "第一句很短。第二句特别长，逗号很多，一直在说，说到四十个字还没有停下来的样子，继续往后说，再说一点点。"
	shots := fallbackSplit(text)
	var joined strings.Builder
	for _, s := range shots {
		joined.WriteString(s.Narration)
		if n := len([]rune(s.Narration)); n > explainerMaxShotRunes {
			t.Fatalf("shot too long (%d): %q", n, s.Narration)
		}
		if s.Speaker != SpeakerNarrator || s.StyleKey != "documentary" {
			t.Fatalf("fallback shot defaults wrong: %+v", s)
		}
	}
	if squash(joined.String()) != squash(text) {
		t.Fatalf("fallback lost text: %q", joined.String())
	}
}

func TestTidyExplainerShotsMergesFragments(t *testing.T) {
	shots := []Shot{
		{Narration: "9月1号，三份国家级文件在同一天生效了"},
		{Narration: "。"}, // 模型把句号单独切了出来
		{Narration: "你每天刷过、点过、去过、说过留下的那些记录，"},
		{Narration: "那我换一个问法："}, // 7 个实字的碎片
		{Narration: "别人用这些记录换来的钱，凭什么不能有你一份？"},
	}
	got := tidyExplainerShots(shots)
	var joined strings.Builder
	for _, s := range got {
		joined.WriteString(s.Narration)
		if substantiveRunes(s.Narration) < explainerMinShotRunes {
			t.Fatalf("fragment survived: %q", s.Narration)
		}
	}
	if len(got) != 3 {
		t.Fatalf("shots = %d, want 3: %+v", len(got), got)
	}
	if got[0].Narration != "9月1号，三份国家级文件在同一天生效了。" {
		t.Fatalf("punctuation not merged back: %q", got[0].Narration)
	}
	var want strings.Builder
	for _, s := range shots {
		want.WriteString(s.Narration)
	}
	if joined.String() != want.String() {
		t.Fatalf("tidy lost or reordered text:\n%s\n%s", joined.String(), want.String())
	}
}

func TestAlignShotsToWords(t *testing.T) {
	shots := []Shot{{Narration: "三份文件，"}, {Narration: "同一天生效。"}}
	words := []narration.Word{
		{Text: "三份", StartTime: 0.10, EndTime: 0.60},
		{Text: "文件", StartTime: 0.60, EndTime: 1.10},
		{Text: "，", StartTime: 1.10, EndTime: 1.30},
		{Text: "同一天", StartTime: 1.40, EndTime: 2.00},
		{Text: "生效", StartTime: 2.00, EndTime: 2.50},
		{Text: "。", StartTime: 2.50, EndTime: 2.60},
	}
	got := alignShotsToWords(shots, words, 3.0)
	if got[0][0] != 0 || got[0][1] < 1.09 || got[0][1] > 1.31 {
		t.Fatalf("first shot = %v, want 0 → ~1.1-1.3", got[0])
	}
	if got[1][0] != got[0][1] || got[1][1] != 3.0 {
		t.Fatalf("second shot = %v, want contiguous and ending at total", got[1])
	}
	// 没有时间戳：按字数比例。
	prop := alignShotsToWords(shots, nil, 10)
	if prop[0][1] <= 3 || prop[0][1] >= 5 || prop[1][1] != 10 {
		t.Fatalf("proportional fallback = %v", prop)
	}
	// AuraSTD 是整句一个 token：句内的两镜要按字数在句子时长里插值，而不是都拿整句时长。
	sentence := []narration.Word{
		{Text: "三份文件，同一天生效。", StartTime: 0, EndTime: 4.0},
		{Text: "一夜之间，你留下的痕迹全都能被交易。", StartTime: 4.2, EndTime: 10.0},
	}
	four := []Shot{{Narration: "三份文件，"}, {Narration: "同一天生效。"}, {Narration: "一夜之间，"}, {Narration: "你留下的痕迹全都能被交易。"}}
	got = alignShotsToWords(four, sentence, 10.0)
	if got[0][1] < 1.5 || got[0][1] > 2.1 { // 4/9 字 ≈ 1.78 秒
		t.Fatalf("first clause = %v, want ~1.8s", got[0])
	}
	if got[1][1] < 3.9 || got[1][1] > 4.3 {
		t.Fatalf("sentence boundary = %v, want ~4.0-4.2", got[1])
	}
	if got[2][1]-got[2][0] > got[3][1]-got[3][0] {
		t.Fatalf("shorter clause must not get more time than longer one: %v", got)
	}
}

func TestTimesFromCharMapSwitchesInPauseAndFixesOutliers(t *testing.T) {
	shots := []Shot{{Narration: "三份文件。"}, {Narration: "同一天生效。"}, {Narration: "一夜之间全能交易。"}}
	// 4 + 5 + 8 = 17 字；第 2 镜和第 3 镜之间有 1 秒停顿，识别把第 3 镜前几个字的时间漂到很晚。
	chars := [][2]float64{
		{0.0, 0.3}, {0.3, 0.6}, {0.6, 0.9}, {0.9, 1.2}, // 三份文件 → 0–1.2
		{1.4, 1.7}, {1.7, 2.0}, {2.0, 2.3}, {2.3, 2.6}, {2.6, 2.9}, // 同一天生效 → 1.4–2.9
		{3.9, 4.2}, {4.2, 4.5}, {4.5, 4.8}, {4.8, 5.1}, {5.1, 5.4}, {5.4, 5.7}, {5.7, 6.0}, {6.0, 6.3}, // 一夜之间全能交易 → 3.9–6.3
	}
	got := timesFromCharMap(shots, chars, nil, 6.5)
	if got[0][1] < 1.25 || got[0][1] > 1.35 { // 停顿 1.2–1.4 的中点
		t.Fatalf("first boundary = %v, want ~1.3", got[0][1])
	}
	if got[1][1] < 3.3 || got[1][1] > 3.5 { // 停顿 2.9–3.9 的中点
		t.Fatalf("second boundary = %v, want ~3.4", got[1][1])
	}
	if got[2][1] != 6.5 || got[0][0] != 0 {
		t.Fatalf("timeline must span 0..total: %v", got)
	}
	// 离群：识别把第 2 镜末字漂到 5.5s，第 3 镜首字 5.6s → 边界 5.55 明显不合语速，应被拉回比例位置。
	bad := make([][2]float64, len(chars))
	copy(bad, chars)
	bad[8] = [2]float64{5.3, 5.5}
	bad[9] = [2]float64{5.6, 5.7}
	fixed := timesFromCharMap(shots, bad, nil, 6.5)
	if fixed[1][1] > 4.0 {
		t.Fatalf("outlier boundary not corrected: %v", fixed)
	}
	// 供应商粗时间戳当硬约束：第 3 镜整句在 3.9–6.3 这一段里，换图点不能被识别漂移拖出这一段。
	vendor := []narration.Word{
		{Text: "三份文件。同一天生效。", StartTime: 0, EndTime: 2.9},
		{Text: "一夜之间全能交易。", StartTime: 3.9, EndTime: 6.3},
	}
	clamped := timesFromCharMap(shots, bad, vendor, 6.5)
	if clamped[1][1] < 2.9 || clamped[1][1] > 3.9 {
		t.Fatalf("boundary escaped the vendor window: %v", clamped)
	}
}

func TestFinalizeExplainerShots(t *testing.T) {
	shots := make([]Shot, 12)
	for i := range shots {
		shots[i].Narration = "十五个字左右的一句旁白文案内容"
		shots[i].Hero = true
		shots[i].StyleKey = "documentary"
		shots[i].VideoPath, shots[i].VideoStatus = "old.mp4", ShotDone
	}
	finalizeExplainerShots(shots, "collage")
	for i, s := range shots {
		if s.Index != i || s.CameraMove == "" {
			t.Fatalf("shot %d not finalized: %+v", i, s)
		}
		if s.Hero || s.VideoPath != "" || s.VideoStatus != ShotPending {
			t.Fatalf("explainer shot %d must be image-only: %+v", i, s)
		}
		if s.StyleKey != "collage" {
			t.Fatalf("shot %d style = %q, want collage", i, s.StyleKey)
		}
	}
}

func TestExplainerSecondsFollowTTSRate(t *testing.T) {
	if got := explainerSecondsForLine("9月1号，三份国家级文件在同一天生效了。"); got != 6 { // 20 字 ≈ 5 秒
		t.Fatalf("20 runes -> %d, want 6", got)
	}
	if got := explainerSecondsForLine(strings.Repeat("字", 30)); got != 10 {
		t.Fatalf("30 runes -> %d, want 10", got)
	}
	if got := explainerSecondsForLine(strings.Repeat("字", 45)); got != 15 {
		t.Fatalf("45 runes -> %d, want 15", got)
	}
}

func TestExplainerVideoPromptUsesMotionAtNormalSpeed(t *testing.T) {
	p := explainerVideoPrompt(Shot{Motion: "手把三份文件依次翻开，镜头缓慢推近"})
	if !strings.Contains(p, "手把三份文件依次翻开") || !strings.Contains(p, "正常速度") || strings.Contains(p, "轻微") {
		t.Fatalf("prompt = %s", p)
	}
	if !strings.Contains(explainerVideoPrompt(Shot{}), "没有人物时不新增人物或手") || strings.Contains(explainerVideoPrompt(Shot{}), "翻开文件") {
		t.Fatal("empty motion must preserve the actual scene rather than invent a hand action")
	}
}

func TestPromptsVisibleBeforeGeneration(t *testing.T) {
	store := &Store{DataRoot: t.TempDir()}
	short := &Short{ID: "p", Mode: ModeExplainer, Status: StatusStoryboard, Shots: []Shot{
		{Index: 0, Narration: "三份文件同一天生效。", Subject: "桌上三份盖红章的文件", Scene: "桌上三份文件", Motion: "手翻开文件", StyleKey: "documentary", Hero: true},
	}}
	if err := store.Save(short); err != nil {
		t.Fatal(err)
	}
	// 老记录没存提示词，读出来时补上。
	got, err := store.Get("p")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Shots[0].ImagePrompt, "桌上三份盖红章的文件") || got.Shots[0].VideoPrompt != "" || got.Shots[0].Hero {
		t.Fatalf("prompts not prefilled: %+v", got.Shots[0])
	}
	// 改了主体，提示词跟着变。
	svc := &Service{store: store}
	updated, err := svc.UpdateShot("p", 0, ShotPatch{Subject: "银行柜台上的现金"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.Shots[0].ImagePrompt, "银行柜台上的现金") || strings.Contains(updated.Shots[0].ImagePrompt, "桌上三份盖红章") {
		t.Fatalf("prompt not refreshed after edit: %s", updated.Shots[0].ImagePrompt)
	}
}

func TestShotLocksAllowDifferentShotsButBlockSameShotAndBatch(t *testing.T) {
	svc := &Service{}
	if !svc.tryLockShot("short", 0) {
		t.Fatal("first shot lock rejected")
	}
	if !svc.tryLockShot("short", 1) {
		t.Fatal("different shot must be allowed concurrently")
	}
	if svc.tryLockShot("short", 0) {
		t.Fatal("same shot must not be submitted twice")
	}
	if svc.tryLock("short") {
		t.Fatal("batch operation must be blocked while shot jobs run")
	}
	svc.unlockShot("short", 0)
	svc.unlockShot("short", 1)
	if !svc.tryLock("short") {
		t.Fatal("batch operation should start after all shot jobs finish")
	}
	if svc.tryLockShot("short", 2) {
		t.Fatal("shot operation must be blocked while batch operation runs")
	}
	svc.unlock("short")
}

func TestFinishGenerationWaitsForOtherRunningShot(t *testing.T) {
	store := &Store{DataRoot: t.TempDir()}
	short := &Short{
		ID: "parallel", Mode: ModeExplainer, Status: StatusGenerating,
		Shots: []Shot{
			{Index: 0, ImageStatus: ShotDone, ImagePath: "one.jpg"},
			{Index: 1, ImageStatus: ShotRunning},
		},
	}
	if err := store.Save(short); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: store}
	svc.finishGeneration("parallel")
	got, _ := store.Get("parallel")
	if got.Status != StatusGenerating {
		t.Fatalf("status = %q while another shot runs, want generating", got.Status)
	}
	if _, err := store.Update("parallel", func(x *Short) error {
		x.Shots[1].ImageStatus, x.Shots[1].ImagePath = ShotDone, "two.jpg"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc.finishGeneration("parallel")
	got, _ = store.Get("parallel")
	if got.Status != StatusReady {
		t.Fatalf("status = %q after all shots finish, want ready", got.Status)
	}
}

func TestExplainerImagePromptContentBeforeStyle(t *testing.T) {
	p := explainerImagePrompt(Shot{StyleKey: "poster", Subject: "银行柜台前排队的中年人", Scene: "老式银行柜台前排队的人", Narration: "存银行的钱在缩水。"})
	for _, want := range []string{"主体：银行柜台前排队的中年人", "老式银行柜台", "厚涂", "禁止任何文字", "乱码", "中国成年人"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "字幕区") {
		t.Fatalf("subtitle-area clause must be gone:\n%s", p)
	}
	// 旁白原句不能进生图提示词：模型会把它原样印到画面上。
	if strings.Contains(p, "存银行的钱在缩水") {
		t.Fatalf("narration leaked into image prompt:\n%s", p)
	}
	if strings.Index(p, "老式银行柜台") > strings.Index(p, "厚涂") {
		t.Fatal("scene must come before the style block")
	}
	if !strings.Contains(explainerImagePrompt(Shot{StyleKey: "nope", Subject: "兜底"}), "现实生活纪实摄影") {
		t.Fatal("unknown style must fall back to documentary")
	}
}

func TestRenderSRT(t *testing.T) {
	got := renderSRT([]jobCaption{{Text: "族谱上写着", StartS: 0, EndS: 1.25}, {Text: "我们祖上是凤凰", StartS: 61.5, EndS: 63}})
	want := "1\n00:00:00,000 --> 00:00:01,250\n族谱上写着\n\n2\n00:01:01,500 --> 00:01:03,000\n我们祖上是凤凰\n\n"
	if got != want {
		t.Fatalf("srt =\n%s\nwant\n%s", got, want)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	store := &Store{DataRoot: t.TempDir()}
	short := &Short{ID: "abc", Title: "t", Story: "s", Status: StatusDraft, Shots: []Shot{{Index: 0, Narration: "a"}}}
	if err := store.Save(short); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Update("abc", func(x *Short) error { x.Shots[0].ImageStatus = ShotDone; return nil })
	if err != nil || updated.Shots[0].ImageStatus != ShotDone {
		t.Fatalf("update failed: %v %+v", err, updated)
	}
	list, err := store.List()
	if err != nil || len(list) != 1 || list[0].ID != "abc" {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := store.Delete("abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("abc"); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestExtractJSONObject(t *testing.T) {
	got := extractJSONObject("```json\n{\"headline\":\"x\"}\n```")
	if got != `{"headline":"x"}` {
		t.Fatalf("got %q", got)
	}
}
