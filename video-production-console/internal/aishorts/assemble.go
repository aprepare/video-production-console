package aishorts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/narration"
	"video-production-console/internal/spokenlines"
)

// NarrationProducer 是现有的配音入口（AuraSTD / 火山），由 httpapi 提供。
type NarrationProducer func(ctx context.Context, req narration.ProduceRequest) (narration.Delivery, error)

// NarrationRequestBuilder 按设置组好 ProduceRequest（音色、语速等），accountID 用于账号专属音色。
type NarrationRequestBuilder func(ctx context.Context, script, accountID string) (narration.ProduceRequest, error)

// DraftAssembler 把每镜视频、声音、字幕交给 Python 脚本组成剪映草稿并注册。
//
// 声音来源分两路：
//   - 角色台词镜：视频模型生成时已经让角色开口说了这句话，直接用视频自带的声轨，
//     镜头时长就是视频本身的时长；
//   - 旁白镜：TTS 用账号音色单独配这一句，镜头时长 = 配音时长 + 气口，视频自带声轨压到很低只留环境音。
//
// 全部镜头首尾相接排上时间轴。
type DraftAssembler struct {
	Produce      NarrationProducer
	BuildRequest NarrationRequestBuilder
	ScriptPath   string // scripts/ai-shorts/build_ai_short_draft.py
	Python       string
	AccountName  func(accountID string) string
	// Brand 解出这条短片的账号包装（背景框、BGM、音效、字幕样式）；nil 或出错时用 DefaultBrandKit。
	Brand func(ctx context.Context, accountID string) (BrandKit, error)
	// 预跑配音的互斥：同一条短片同时只跑一次。
	prewarm sync.Map
}

type jobShot struct {
	Video       string  `json:"video,omitempty"` // 视频镜
	Image       string  `json:"image,omitempty"` // 图片镜（解说模式），配 camera_move
	CameraMove  string  `json:"camera_move,omitempty"`
	StartS      float64 `json:"start_s"`
	EndS        float64 `json:"end_s"`
	Audio       string  `json:"audio,omitempty"` // 旁白镜的 TTS 文件；角色镜为空
	VideoVolume float64 `json:"video_volume"`
	Speaker     string  `json:"speaker"`
}

type jobCaption struct {
	Text   string  `json:"text"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
}

// ttsClip 是一句旁白的配音结果。
type ttsClip struct {
	path     string
	duration float64
	captions []narration.Caption
}

const (
	shotGapSeconds          = 0.35 // 旁白镜之间留一点气口
	narratorShotVideoVolume = 0.15 // 旁白镜保留一点环境音垫在 TTS 底下
	captionMaxRunes         = 16   // 单条字幕最多多少字，长句按标点/字数切开
	ttsParallel             = 3
)

func (a *DraftAssembler) Assemble(ctx context.Context, rt Runtime, short *Short, assetDir string, progress func(string)) (AssembleResult, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if strings.TrimSpace(rt.JianyingRoot) == "" {
		return AssembleResult{}, errors.New("剪映草稿目录未配置")
	}
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return AssembleResult{}, err
	}
	if short.IsExplainer() {
		return a.assembleExplainer(ctx, rt, short, assetDir, progress)
	}

	// 先把所有要 TTS 的镜头挑出来并行配音，再排时间轴。
	needTTS := map[int]bool{}
	for i, shot := range short.Shots {
		if !short.ShotReady(shot) || strings.TrimSpace(shot.Narration) == "" {
			continue
		}
		if shot.SpokenByCharacter() && shot.VideoPath != "" && probeHasAudio(rt.FFprobePath, shot.VideoPath) {
			continue
		}
		needTTS[i] = true
	}
	clips, err := a.synthesizeAll(ctx, rt, short, assetDir, needTTS, progress)
	if err != nil {
		return AssembleResult{}, err
	}

	shots := make([]jobShot, 0, len(short.Shots))
	caps := make([]jobCaption, 0, 64)
	shotTimes := make([][2]float64, 0, len(short.Shots))
	firstNarration := ""
	cursor := 0.0
	for i, shot := range short.Shots {
		if !short.ShotReady(shot) {
			continue
		}
		line := strings.TrimSpace(shot.Narration)
		start := cursor
		var end float64
		js := jobShot{StartS: start, Speaker: shot.Speaker}
		if short.NeedsVideo(shot) {
			js.Video = shot.VideoPath
		} else {
			js.Image, js.CameraMove = shot.ImagePath, shot.CameraMove
		}
		if clip, ok := clips[i]; ok {
			// 旁白 TTS 定长；视频只留一点环境音。
			end = start + clip.duration + shotGapSeconds
			js.Audio, js.VideoVolume = clip.path, narratorShotVideoVolume
			if js.Image != "" {
				js.VideoVolume = 0
			}
			if firstNarration == "" {
				firstNarration = clip.path
			}
			if len(clip.captions) > 0 {
				for _, c := range clip.captions {
					caps = append(caps, jobCaption{Text: c.Text, StartS: start + c.Start, EndS: start + c.End})
				}
			} else {
				caps = append(caps, spreadCaptions(line, start, start+clip.duration)...)
			}
		} else {
			// 角色自己说的 / 没台词的镜：视频原声，长度就是视频长度。
			dur := probeDuration(rt.FFprobePath, shot.VideoPath)
			if dur <= 0 {
				dur = float64(shot.Seconds)
			}
			end = start + dur
			js.VideoVolume = 1.0
			if line != "" {
				caps = append(caps, spreadCaptions(line, start, end)...)
			}
		}
		js.EndS = end
		shots = append(shots, js)
		shotTimes = append(shotTimes, [2]float64{start, end})
		cursor = end
	}
	if len(shots) == 0 {
		return AssembleResult{}, errors.New("没有可用的镜头画面")
	}
	total := cursor
	srtPath := filepath.Join(assetDir, "narration.srt")
	_ = os.WriteFile(srtPath, []byte(renderSRT(caps)), 0o644)

	progress("生成剪映草稿")
	return a.buildDraft(ctx, rt, short, assetDir, draftJob{
		Headline: short.Headline, DurationS: total, Shots: shots, Captions: caps,
		NarrationPath: firstNarration, SRTPath: srtPath, ShotTimes: shotTimes,
	})
}

// draftJob 是交给 Python 组草稿的全部材料。Narration 非空时整条旁白是一个文件；
// 否则每镜自带 Audio。
type draftJob struct {
	Headline      string
	DurationS     float64
	Shots         []jobShot
	Captions      []jobCaption
	Narration     string // 整段配音文件（解说模式）
	NarrationPath string // 回填到短片记录里给页面试听的那个文件
	SRTPath       string
	ShotTimes     [][2]float64
	// Portrait 为 true 走 9:16 内嵌布局（背景框 + BGM + 音效 + 底色字幕）。
	Portrait bool
	Brand    BrandKit
	SFX      []sfxCue
}

// sfxCue 是一个音效落点。
type sfxCue struct {
	Role   string  `json:"role"`
	Path   string  `json:"path"`
	AtS    float64 `json:"at_s"`
	Volume float64 `json:"volume"`
}

func (a *DraftAssembler) buildDraft(ctx context.Context, rt Runtime, short *Short, assetDir string, in draftJob) (AssembleResult, error) {
	name := a.draftName(short)
	job := map[string]any{
		"draft_name":       name,
		"jianying_root":    rt.JianyingRoot,
		"replace_draft":    short.DraftPath,
		"workspace_parent": filepath.Join(assetDir, "draft"),
		"headline":         in.Headline,
		"duration_s":       in.DurationS,
		"shots":            in.Shots,
		"captions":         in.Captions,
		"style": map[string]any{
			"caption_font": "新青年体", "caption_size": 9, "caption_color": []float64{1, 1, 1},
			"headline_font": "新青年体", "headline_size": 12, "headline_color": []float64{1, 1, 1},
		},
	}
	if in.Narration != "" {
		job["narration"] = in.Narration
	}
	if in.Portrait {
		job["layout"] = "portrait_inset"
		job["brand"] = map[string]any{
			"background": in.Brand.BackgroundPath,
			"bgm": map[string]any{
				"path": in.Brand.BGMPath, "volume": in.Brand.BGMVolume,
				"usable_head_s": in.Brand.BGMUsableHeadS, "climax_start_s": in.Brand.BGMClimaxStartS, "climax_duration_s": in.Brand.BGMClimaxDurationS,
			},
			"sfx": in.SFX,
			"caption": map[string]any{
				"font": in.Brand.CaptionFont, "size": in.Brand.CaptionSize, "color": in.Brand.CaptionColor,
				"bg_color": in.Brand.CaptionBgColor, "bg_alpha": in.Brand.CaptionBgAlpha,
			},
		}
	}
	jobPath := filepath.Join(assetDir, "draft_job.json")
	raw, _ := json.MarshalIndent(job, "", "  ")
	if err := os.WriteFile(jobPath, raw, 0o644); err != nil {
		return AssembleResult{}, err
	}
	python := strings.TrimSpace(a.Python)
	if python == "" {
		python = "python"
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, python, a.ScriptPath, jobPath)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return AssembleResult{}, fmt.Errorf("草稿脚本失败：%v\n%s\n%s", err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	}
	var out struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		DraftPath  string `json:"draft_path"`
		DurationUS int64  `json:"duration_us"`
	}
	lastLine := lastNonEmptyLine(stdout.String())
	if err := json.Unmarshal([]byte(lastLine), &out); err != nil {
		return AssembleResult{}, fmt.Errorf("草稿脚本输出无法解析：%s\n%s", lastLine, strings.TrimSpace(stderr.String()))
	}
	if !out.OK {
		return AssembleResult{}, errors.New("草稿构建失败：" + out.Error)
	}
	return AssembleResult{
		NarrationPath: in.NarrationPath, SRTPath: in.SRTPath, DraftPath: out.DraftPath, DraftName: name,
		DurationS: float64(out.DurationUS) / 1e6, ShotTimes: in.ShotTimes,
	}, nil
}

// assembleExplainer：解说模式整篇文案一次配音（韵律连贯、没有逐句拼接的断气感），
// 再用 TTS 返回的逐字时间戳把每张图对到它那句话的起止点上。字幕直接用配音自带的。
func (a *DraftAssembler) assembleExplainer(ctx context.Context, rt Runtime, short *Short, assetDir string, progress func(string)) (AssembleResult, error) {
	if a.Produce == nil || a.BuildRequest == nil {
		return AssembleResult{}, errors.New("配音服务未接入")
	}
	ready := make([]Shot, 0, len(short.Shots))
	for _, shot := range short.Shots {
		if short.ShotReady(shot) && strings.TrimSpace(shot.Narration) != "" {
			ready = append(ready, shot)
		}
	}
	// 老分镜里可能有「。」或三五个字的碎片：不给它们单独出画面，文字并进邻镜，
	// 邻镜的图多停留一会儿。旧记录不用重拆重生也能出片。
	ready = mergeFragmentShots(ready)
	if len(ready) == 0 {
		return AssembleResult{}, errors.New("没有可用的镜头画面")
	}
	script := joinNarrations(ready)

	nar, err := a.prepareNarration(ctx, rt, short, assetDir, script, progress)
	if err != nil {
		return AssembleResult{}, err
	}
	narrationPath, total := nar.AudioPath, nar.DurationS
	var times [][2]float64
	if len(nar.CharTimes) > 0 {
		times = timesFromCharMap(ready, nar.CharTimes, nar.Words, total)
	} else {
		times = alignShotsToWords(ready, nar.Words, total)
	}
	charTimes := nar.CharTimes
	alignErr := error(nil)
	if len(charTimes) == 0 {
		alignErr = errors.New("no char timings")
	}
	shots := make([]jobShot, 0, len(ready))
	// ShotTimes 按原始镜号回填：被并掉的碎片镜沿用吸收它的那一镜的区间。
	shotTimes := make([][2]float64, len(short.Shots))
	filled := make([]bool, len(short.Shots))
	for i, shot := range ready {
		shots = append(shots, jobShot{
			Image: shot.ImagePath, CameraMove: shot.CameraMove,
			StartS: times[i][0], EndS: times[i][1], VideoVolume: 0, Speaker: SpeakerNarrator,
		})
		if shot.Index >= 0 && shot.Index < len(shotTimes) {
			shotTimes[shot.Index], filled[shot.Index] = times[i], true
		}
	}
	for i := range shotTimes {
		if filled[i] {
			continue
		}
		if i > 0 {
			shotTimes[i] = shotTimes[i-1]
		} else {
			for j := 1; j < len(shotTimes); j++ {
				if filled[j] {
					shotTimes[i] = shotTimes[j]
					break
				}
			}
		}
	}
	// 字幕：一条 = 一个完整分句（只在逗号/句号/问号/感叹号/分号处切，顿号不切），
	// 小字号自动折成两行，最长 32 字（超了才均分成两条）；
	// 时间从逐字映射里取，提前 0.1 秒出、和下一条无缝接（没有映射就在镜内按字数铺）。
	caps := make([]jobCaption, 0, len(ready)*2)
	pos := 0
	for i, shot := range ready {
		n := substantiveRunes(shot.Narration)
		if alignErr == nil {
			caps = append(caps, clauseCaptionsFromCharMap(shot.Narration, pos, charTimes, times[i])...)
		} else {
			caps = append(caps, spreadClauseCaptions(shot.Narration, times[i][0], times[i][1])...)
		}
		pos += n
	}
	caps = tightenCaptions(caps, total)
	srtPath := filepath.Join(assetDir, "narration.srt")
	_ = os.WriteFile(srtPath, []byte(renderSRT(caps)), 0o644)

	// 账号包装：背景框、BGM、音效、字幕样式。拿不到就用默认（无背景、无 BGM）。
	brand := DefaultBrandKit()
	if a.Brand != nil {
		if kit, err := a.Brand(ctx, short.AccountID); err == nil {
			brand = kit
		} else {
			slog.Default().Warn("ai short: brand kit unavailable", "error", err)
		}
	}
	cues := planSFX(ready, times, total, brand)

	progress("生成剪映草稿")
	return a.buildDraft(ctx, rt, short, assetDir, draftJob{
		Headline: "", DurationS: total, Shots: shots, Captions: caps,
		Narration: narrationPath, NarrationPath: narrationPath, SRTPath: srtPath, ShotTimes: shotTimes,
		Portrait: true, Brand: brand, SFX: cues,
	})
}

const (
	portraitCaptionMaxRunes = 32   // 竖版一条字幕最多几个字（小字号两行装得下）
	captionLeadSeconds      = 0.10 // 字幕比声音早出一点，观感上才"同步"
	captionMaxHoldSeconds   = 0.60 // 一条字幕最多在下一条出现前多停多久
)

// splitClauses 按分句标点切（，。！？；：），顿号和引号不切，保留分句内的标点；
// 超过 portraitCaptionMaxRunes 的分句均分成几条。
func splitClauses(line string) []string {
	var clauses []string
	var cur []rune
	flush := func() {
		if s := strings.TrimSpace(string(cur)); s != "" {
			clauses = append(clauses, strings.TrimRight(s, "，。！？；：,.!?;:"))
		}
		cur = cur[:0]
	}
	for _, r := range strings.TrimSpace(line) {
		switch r {
		case '\n', '"', '\u201c', '\u201d', '「', '」', '（', '）', '(', ')':
			continue
		case '，', '。', '！', '？', '；', '：', ',', '.', '!', '?', ';', ':':
			cur = append(cur, r)
			flush()
		default:
			cur = append(cur, r)
		}
	}
	flush()
	var out []string
	for _, c := range clauses {
		runes := []rune(c)
		if len(runes) <= portraitCaptionMaxRunes {
			out = append(out, c)
			continue
		}
		parts := (len(runes) + portraitCaptionMaxRunes - 1) / portraitCaptionMaxRunes
		base, extra := len(runes)/parts, len(runes)%parts
		at := 0
		for k := 0; k < parts; k++ {
			n := base
			if k < extra {
				n++
			}
			out = append(out, strings.TrimRight(string(runes[at:at+n]), "，、"))
			at += n
		}
	}
	return out
}

// clauseCaptionsFromCharMap 每个分句一条字幕，时间取分句首尾实字的时间，夹在这镜的区间内。
func clauseCaptionsFromCharMap(line string, startChar int, chars [][2]float64, window [2]float64) []jobCaption {
	out := make([]jobCaption, 0, 2)
	pos := startChar
	for _, c := range splitClauses(line) {
		n := substantiveRunes(c)
		if n == 0 {
			continue
		}
		first, last := pos, pos+n-1
		pos += n
		if first >= len(chars) {
			break
		}
		if last >= len(chars) {
			last = len(chars) - 1
		}
		s, e := chars[first][0], chars[last][1]
		if s < window[0] {
			s = window[0]
		}
		if e > window[1] {
			e = window[1]
		}
		if len(out) > 0 && s < out[len(out)-1].EndS {
			s = out[len(out)-1].EndS
		}
		if e <= s {
			continue
		}
		out = append(out, jobCaption{Text: c, StartS: s, EndS: e})
	}
	return out
}

// spreadClauseCaptions 没有逐字时间时，分句按字数比例铺在这镜的区间上。
func spreadClauseCaptions(line string, start, end float64) []jobCaption {
	clauses := splitClauses(line)
	total := 0
	for _, c := range clauses {
		total += substantiveRunes(c)
	}
	if total == 0 || end <= start {
		return nil
	}
	per := (end - start) / float64(total)
	t := start
	out := make([]jobCaption, 0, len(clauses))
	for _, c := range clauses {
		d := per * float64(substantiveRunes(c))
		out = append(out, jobCaption{Text: c, StartS: t, EndS: t + d})
		t += d
	}
	return out
}

// tightenCaptions 让字幕提前 lead 秒出现、和下一条无缝相接（最多多停 hold 秒），并去掉重叠。
func tightenCaptions(caps []jobCaption, total float64) []jobCaption {
	for i := range caps {
		caps[i].StartS -= captionLeadSeconds
		if caps[i].StartS < 0 {
			caps[i].StartS = 0
		}
	}
	for i := range caps {
		if i+1 < len(caps) {
			next := caps[i+1].StartS
			if next > caps[i].EndS {
				if next-caps[i].EndS <= captionMaxHoldSeconds {
					caps[i].EndS = next
				} else {
					caps[i].EndS += captionMaxHoldSeconds / 2
				}
			} else if next < caps[i].EndS {
				caps[i].EndS = next
			}
		} else if caps[i].EndS > total {
			caps[i].EndS = total
		}
		if i > 0 && caps[i].StartS < caps[i-1].EndS {
			caps[i].StartS = caps[i-1].EndS
		}
	}
	out := caps[:0]
	for _, c := range caps {
		if c.EndS-c.StartS > 0.05 {
			out = append(out, c)
		}
	}
	return out
}

// planSFX 借混剪的音效规则：开头一记"咚"，转折句用"呼"，结论句用"综艺咚"，
// 相邻音效至少隔 12 秒，总数 = 时长/45 + 1（最多 12）。没有对应文件就不放。
func planSFX(shots []Shot, times [][2]float64, total float64, brand BrandKit) []sfxCue {
	if len(brand.SFX) == 0 || len(shots) == 0 {
		return nil
	}
	vol := brand.SFXVolume
	if vol <= 0 {
		vol = 0.3981
	}
	var cues []sfxCue
	lastAt := -100.0
	add := func(role string, at float64) bool {
		p, ok := brand.SFX[role]
		if !ok || at-lastAt < 12 || at > total-1 {
			return false
		}
		cues = append(cues, sfxCue{Role: role, Path: p, AtS: at, Volume: vol})
		lastAt = at
		return true
	}
	add("opening", 0)
	budget := int(total/45) + 1
	if budget > 12 {
		budget = 12
	}
	pivots := []string{"但", "然而", "结果", "所以", "可是", "其实", "问题是", "关键", "换句话说", "第一", "第二", "第三", "现在", "接下来"}
	conclusions := []string{"记住", "说白了", "总结", "一句话", "最后", "所以说", "归根结底", "你要做的"}
	for i, s := range shots {
		if len(cues) >= budget {
			break
		}
		line := strings.TrimSpace(s.Narration)
		at := times[i][0]
		role := ""
		for _, k := range conclusions {
			if strings.HasPrefix(line, k) {
				role = "conclusion"
				break
			}
		}
		if role == "" {
			for _, k := range pivots {
				if strings.HasPrefix(line, k) {
					role = "whoosh"
					break
				}
			}
		}
		if role == "" {
			continue
		}
		add(role, at)
	}
	return cues
}

// preparedNarration 是整篇配音 + 时间信息，落盘缓存，组装与预跑共用。
type preparedNarration struct {
	ScriptSHA string           `json:"script_sha256"`
	AudioPath string           `json:"audio_path"`
	DurationS float64          `json:"duration_s"`
	Words     []narration.Word `json:"words"`      // 供应商的粗时间戳
	CharTimes [][2]float64     `json:"char_times"` // 逐字时间（ASR 对齐）；空表示对齐失败
}

const narrationMetaFile = "narration.meta.json"

func scriptDigest(script string) string {
	sum := sha256.Sum256([]byte(squash(script)))
	return hex.EncodeToString(sum[:])
}

// loadPreparedNarration 读缓存：脚本没变、音频还在才算命中。
func loadPreparedNarration(assetDir, script string) (preparedNarration, bool) {
	raw, err := os.ReadFile(filepath.Join(assetDir, narrationMetaFile))
	if err != nil {
		return preparedNarration{}, false
	}
	var meta preparedNarration
	if json.Unmarshal(raw, &meta) != nil || meta.ScriptSHA != scriptDigest(script) || meta.DurationS <= 0 {
		return preparedNarration{}, false
	}
	if info, err := os.Stat(meta.AudioPath); err != nil || info.Size() == 0 {
		return preparedNarration{}, false
	}
	return meta, true
}

// prepareNarration：整篇 TTS → 落盘 → 语音识别逐字对齐；结果连同脚本指纹一起缓存。
// 生图阶段就会预跑一次，组装时脚本没改就直接复用，省掉 2～4 分钟。
func (a *DraftAssembler) prepareNarration(ctx context.Context, rt Runtime, short *Short, assetDir, script string, progress func(string)) (preparedNarration, error) {
	if meta, ok := loadPreparedNarration(assetDir, script); ok {
		progress("复用已生成的配音与时间轴")
		return meta, nil
	}
	if a.Produce == nil || a.BuildRequest == nil {
		return preparedNarration{}, errors.New("配音服务未接入")
	}
	progress("整篇配音")
	req, err := a.BuildRequest(ctx, script, short.AccountID)
	if err != nil {
		return preparedNarration{}, err
	}
	if formatted, ferr := spokenlines.Format(script); ferr == nil {
		req.SpokenLines = spokenlines.Lines(formatted)
	}
	delivery, err := a.Produce(ctx, req)
	if err != nil {
		var gate *narration.QualityGateError
		if !errors.As(err, &gate) || len(delivery.Audio) == 0 {
			return preparedNarration{}, fmt.Errorf("配音失败：%w", err)
		}
	}
	audioPath := filepath.Join(assetDir, "narration.mp3")
	if err := os.WriteFile(audioPath, delivery.Audio, 0o644); err != nil {
		return preparedNarration{}, err
	}
	// 供应商的原始时间戳留一份，对不上时好查。
	if raw, err := json.MarshalIndent(delivery.Words, "", " "); err == nil {
		_ = os.WriteFile(filepath.Join(assetDir, "narration_words.json"), raw, 0o644)
	}
	// 总长取音频实际长度、字幕末尾、最后一个字的时间戳三者最大，时间轴才不会比配音短。
	total := probeDuration(rt.FFprobePath, audioPath)
	if delivery.Duration > total {
		total = delivery.Duration
	}
	if n := len(delivery.Words); n > 0 && delivery.Words[n-1].EndTime > total {
		total = delivery.Words[n-1].EndTime
	}
	if total <= 0 {
		total = float64(len([]rune(script))) * 0.23
	}
	progress("对齐时间轴（语音识别逐字对齐，约一两分钟）")
	charTimes, alignErr := a.alignByASR(ctx, audioPath, script, assetDir)
	if alignErr != nil {
		slog.Default().Warn("ai short: ASR alignment unavailable, falling back to vendor timings", "error", alignErr)
		charTimes = nil
	}
	meta := preparedNarration{ScriptSHA: scriptDigest(script), AudioPath: audioPath, DurationS: total, Words: delivery.Words, CharTimes: charTimes}
	if raw, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(filepath.Join(assetDir, narrationMetaFile), raw, 0o644)
	}
	return meta, nil
}

// PrewarmNarration 在后台把配音和对齐先跑出来（生图的同时），组装时直接命中缓存。
// 同一条短片同时只跑一次；失败只记日志，组装时会重试。
func (a *DraftAssembler) PrewarmNarration(ctx context.Context, rt Runtime, short *Short, assetDir string) {
	if !short.IsExplainer() {
		return
	}
	ready := make([]Shot, 0, len(short.Shots))
	for _, shot := range short.Shots {
		if strings.TrimSpace(shot.Narration) != "" {
			ready = append(ready, shot)
		}
	}
	ready = mergeFragmentShots(ready)
	if len(ready) == 0 {
		return
	}
	script := joinNarrations(ready)
	if _, ok := loadPreparedNarration(assetDir, script); ok {
		return
	}
	if _, busy := a.prewarm.LoadOrStore(short.ID, true); busy {
		return
	}
	defer a.prewarm.Delete(short.ID)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return
	}
	if _, err := a.prepareNarration(ctx, rt, short, assetDir, script, func(string) {}); err != nil {
		slog.Default().Warn("ai short: narration prewarm failed", "id", short.ID, "error", err)
	}
}

// alignByASR 调 scripts/ai-shorts/align_narration.py：用 faster-whisper 转写配音，
// 和文案逐字对齐，返回每个实字的 [start,end]。脚本或依赖不在就报错让调用方回退。
func (a *DraftAssembler) alignByASR(ctx context.Context, audioPath, script, assetDir string) ([][2]float64, error) {
	scriptDir := filepath.Dir(a.ScriptPath)
	aligner := filepath.Join(scriptDir, "align_narration.py")
	if _, err := os.Stat(aligner); err != nil {
		return nil, fmt.Errorf("aligner missing: %w", err)
	}
	scriptPath := filepath.Join(assetDir, "narration_script.txt")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		return nil, err
	}
	outPath := filepath.Join(assetDir, "narration_align.json")
	python := strings.TrimSpace(a.Python)
	if python == "" {
		python = "python"
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, python, aligner, audioPath, scriptPath, outPath)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("aligner failed: %v: %s %s", err, lastNonEmptyLine(stdout.String()), strings.TrimSpace(stderr.String()))
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	var out struct {
		OK      bool         `json:"ok"`
		Error   string       `json:"error"`
		Chars   int          `json:"chars"`
		Matched int          `json:"matched"`
		Times   [][2]float64 `json:"times"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, errors.New(out.Error)
	}
	if out.Chars != substantiveRunes(script) || len(out.Times) != out.Chars {
		return nil, fmt.Errorf("aligner char count mismatch: %d vs %d", out.Chars, substantiveRunes(script))
	}
	// 识别对上的字太少说明音频和文案对不上（配错了或识别崩了），别拿来当真。
	if out.Chars > 0 && float64(out.Matched)/float64(out.Chars) < 0.6 {
		return nil, fmt.Errorf("aligner matched only %d/%d chars", out.Matched, out.Chars)
	}
	return out.Times, nil
}

// charEnvelope 是供应商粗时间戳给出的"第几个实字一定落在哪段时间里"的硬约束：
// 供应商每段字幕的文字就是原文，边界时间准确，只是段太长（十几秒）。
type charEnvelope struct {
	fromChar, toChar int // [from, to)
	start, end       float64
}

func buildEnvelope(words []narration.Word, scriptChars int) []charEnvelope {
	var env []charEnvelope
	pos := 0
	for _, w := range words {
		n := substantiveRunes(w.Text)
		if n == 0 || w.EndTime < w.StartTime {
			continue
		}
		env = append(env, charEnvelope{fromChar: pos, toChar: pos + n, start: w.StartTime, end: w.EndTime})
		pos += n
	}
	if pos != scriptChars {
		return nil // 供应商回的文字和文案对不上，这个约束不可信
	}
	return env
}

// boundaryWindow 返回"第 p 个实字之前的换图点"允许落在的时间区间。
// p 正好在两段之间：窗口就是两段之间的空隙（几乎是确定的点）；p 在段内：窗口是这一段。
func boundaryWindow(env []charEnvelope, p int) (lo, hi float64, ok bool) {
	for k, e := range env {
		if p == e.fromChar && k > 0 {
			return env[k-1].end, e.start, true
		}
		if p > e.fromChar && p < e.toChar {
			return e.start, e.end, true
		}
	}
	return 0, 0, false
}

// timesFromCharMap 用逐字时间给每镜定起止。换图点放在两镜之间停顿的中点；
// 每个换图点都夹在供应商粗时间戳给出的窗口里（识别漂了也跨不出这一段）；
// 最后按语速把明显失真的镜头和邻镜重新按字数分配。
func timesFromCharMap(shots []Shot, chars [][2]float64, words []narration.Word, total float64) [][2]float64 {
	n := len(shots)
	out := make([][2]float64, n)
	if n == 0 {
		return out
	}
	counts := make([]int, n)
	starts := make([]int, n+1) // 每镜第一个实字的下标；starts[n] = 总字数
	spoken := make([][2]float64, n)
	pos := 0
	for i, s := range shots {
		counts[i] = substantiveRunes(s.Narration)
		starts[i] = pos
		first, last := pos, pos+counts[i]-1
		pos += counts[i]
		if counts[i] == 0 || first >= len(chars) {
			spoken[i] = [2]float64{-1, -1}
			continue
		}
		if last >= len(chars) {
			last = len(chars) - 1
		}
		spoken[i] = [2]float64{chars[first][0], chars[last][1]}
	}
	starts[n] = pos
	env := buildEnvelope(words, pos)

	bounds := make([]float64, n+1)
	bounds[0], bounds[n] = 0, total
	for i := 1; i < n; i++ {
		prevEnd, nextStart := spoken[i-1][1], spoken[i][0]
		switch {
		case prevEnd < 0 && nextStart < 0:
			bounds[i] = bounds[i-1]
		case prevEnd < 0:
			bounds[i] = nextStart
		case nextStart < 0:
			bounds[i] = prevEnd
		default:
			bounds[i] = (prevEnd + nextStart) / 2
		}
		if lo, hi, ok := boundaryWindow(env, starts[i]); ok {
			if bounds[i] < lo {
				bounds[i] = lo
			}
			if bounds[i] > hi {
				bounds[i] = hi
			}
		}
	}
	// 语速修正：某镜每字时长偏离全片均值太多，就把它和前后镜合起来按字数重分（不越过供应商窗口）。
	if pos > 0 {
		avg := total / float64(pos)
		for pass := 0; pass < 6; pass++ {
			changed := false
			for i := 0; i < n; i++ {
				if counts[i] == 0 {
					continue
				}
				rate := (bounds[i+1] - bounds[i]) / float64(counts[i])
				if rate >= 0.55*avg && rate <= 1.8*avg {
					continue
				}
				lo, hi := i, i+1 // 和前后镜一起重分
				if lo > 0 {
					lo--
				}
				if hi < n {
					hi++
				}
				span := bounds[hi] - bounds[lo]
				sum := 0
				for k := lo; k < hi; k++ {
					sum += counts[k]
				}
				if span <= 0 || sum == 0 {
					continue
				}
				t := bounds[lo]
				for k := lo; k < hi-1; k++ {
					t += span * float64(counts[k]) / float64(sum)
					nb := t
					if wlo, whi, ok := boundaryWindow(env, starts[k+1]); ok {
						if nb < wlo {
							nb = wlo
						}
						if nb > whi {
							nb = whi
						}
					}
					if nb != bounds[k+1] {
						bounds[k+1] = nb
						changed = true
					}
				}
			}
			if !changed {
				break
			}
		}
	}
	for i := 0; i < n; i++ {
		out[i] = [2]float64{bounds[i], bounds[i+1]}
	}
	return normalizeTimeline(out, total)
}

// captionsFromCharMap 把一镜的旁白按标点切成短条，每条的时间取它首尾实字的时间，并夹在这镜的区间内。
func captionsFromCharMap(line string, startChar int, chars [][2]float64, window [2]float64) []jobCaption {
	return captionsFromCharMapN(line, startChar, chars, window, captionMaxRunes)
}

func captionsFromCharMapN(line string, startChar int, chars [][2]float64, window [2]float64, maxRunes int) []jobCaption {
	pieces := splitCaptionPiecesN(line, maxRunes)
	out := make([]jobCaption, 0, len(pieces))
	pos := startChar
	for _, p := range pieces {
		n := substantiveRunes(p)
		if n == 0 {
			continue
		}
		first, last := pos, pos+n-1
		pos += n
		if first >= len(chars) {
			break
		}
		if last >= len(chars) {
			last = len(chars) - 1
		}
		s, e := chars[first][0], chars[last][1]
		if s < window[0] {
			s = window[0]
		}
		if e > window[1] {
			e = window[1]
		}
		if len(out) > 0 && s < out[len(out)-1].EndS {
			s = out[len(out)-1].EndS
		}
		if e <= s {
			continue
		}
		out = append(out, jobCaption{Text: p, StartS: s, EndS: e})
	}
	return out
}

// mergeFragmentShots 把实字少于 explainerMinShotRunes 的镜并入前一镜（第一镜并入后一镜），
// 画面沿用被并入那一镜的图。只影响时间轴，不改存储的分镜。
func mergeFragmentShots(shots []Shot) []Shot {
	out := make([]Shot, 0, len(shots))
	for i, shot := range shots {
		if substantiveRunes(shot.Narration) >= explainerMinShotRunes {
			out = append(out, shot)
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].Narration += shot.Narration
			continue
		}
		if i+1 < len(shots) {
			shots[i+1].Narration = shot.Narration + shots[i+1].Narration
			continue
		}
		out = append(out, shot) // 全片只有这一镜
	}
	return out
}

// joinNarrations 把各镜旁白按顺序接成整篇。句末标点后面换行，让 TTS 在句间自然停顿。
func joinNarrations(shots []Shot) string {
	var b strings.Builder
	for _, shot := range shots {
		line := strings.TrimSpace(shot.Narration)
		b.WriteString(line)
		if strings.HasSuffix(line, "。") || strings.HasSuffix(line, "！") || strings.HasSuffix(line, "？") {
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// alignShotsToWords 用配音返回的时间戳给每镜找起止点。
// 供应商的 token 粒度不一（火山逐字，AuraSTD 逐句），所以不按 token 消费，
// 而是先把 token 铺成一条「第几个实字 → 秒」的分段线性映射，再用每镜实字的起止位置查时间。
// token 总字数和分镜总字数不一致（比如数字被念法改写）时按比例缩放对上。
// 没有时间戳时退回按字数比例分。
func alignShotsToWords(shots []Shot, words []narration.Word, total float64) [][2]float64 {
	out := make([][2]float64, len(shots))
	if len(shots) == 0 {
		return out
	}
	type span struct {
		fromChar, toChar int
		start, end       float64
	}
	spans := make([]span, 0, len(words))
	tokenChars := 0
	for _, w := range words {
		n := substantiveRunes(w.Text)
		if n == 0 || w.EndTime < w.StartTime {
			continue
		}
		spans = append(spans, span{fromChar: tokenChars, toChar: tokenChars + n, start: w.StartTime, end: w.EndTime})
		tokenChars += n
	}
	shotChars := 0
	for _, s := range shots {
		shotChars += substantiveRunes(s.Narration)
	}
	if len(spans) == 0 || tokenChars == 0 || shotChars == 0 {
		return proportionalTimes(shots, total)
	}
	scale := float64(tokenChars) / float64(shotChars)
	timeAt := func(shotChar int) float64 {
		c := float64(shotChar) * scale
		for k, sp := range spans {
			if c <= float64(sp.toChar) || k == len(spans)-1 {
				if c < float64(sp.fromChar) {
					// 落在两个 token 的空隙里：取上一个的结尾。
					if k > 0 {
						return spans[k-1].end
					}
					return sp.start
				}
				frac := (c - float64(sp.fromChar)) / float64(sp.toChar-sp.fromChar)
				if frac > 1 {
					frac = 1
				}
				return sp.start + frac*(sp.end-sp.start)
			}
		}
		return spans[len(spans)-1].end
	}
	pos := 0
	for i, s := range shots {
		n := substantiveRunes(s.Narration)
		out[i] = [2]float64{timeAt(pos), timeAt(pos + n)}
		pos += n
	}
	return normalizeTimeline(out, total)
}

// normalizeTimeline 让镜头首尾相接、每镜至少 minShotSeconds，总长正好等于 total。
// 对齐结果偶尔会超出音频长度（时间戳漂移），超了就整体等比压回去，绝不产生零长或重叠的片段。
func normalizeTimeline(out [][2]float64, total float64) [][2]float64 {
	const minShotSeconds = 0.5
	if len(out) == 0 {
		return out
	}
	out[0][0] = 0
	if out[0][1] < minShotSeconds {
		out[0][1] = minShotSeconds
	}
	for i := 1; i < len(out); i++ {
		out[i][0] = out[i-1][1]
		if out[i][1] < out[i][0]+minShotSeconds {
			out[i][1] = out[i][0] + minShotSeconds
		}
	}
	last := out[len(out)-1][1]
	if last > total && last > 0 {
		scale := total / last
		for i := range out {
			out[i][0] *= scale
			out[i][1] *= scale
		}
	}
	out[len(out)-1][1] = total
	return out
}

func proportionalTimes(shots []Shot, total float64) [][2]float64 {
	out := make([][2]float64, len(shots))
	sum := 0
	for _, s := range shots {
		sum += substantiveRunes(s.Narration)
	}
	if sum == 0 {
		sum = len(shots)
	}
	t := 0.0
	for i, s := range shots {
		n := substantiveRunes(s.Narration)
		if n == 0 {
			n = 1
		}
		d := total * float64(n) / float64(sum)
		out[i] = [2]float64{t, t + d}
		t += d
	}
	return out
}

// synthesizeAll 并行给选中的镜头配旁白，返回 index → 片段；任一失败整体失败。
func (a *DraftAssembler) synthesizeAll(ctx context.Context, rt Runtime, short *Short, assetDir string, wanted map[int]bool, progress func(string)) (map[int]ttsClip, error) {
	clips := map[int]ttsClip{}
	if len(wanted) == 0 {
		return clips, nil
	}
	if a.Produce == nil || a.BuildRequest == nil {
		return nil, errors.New("配音服务未接入")
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		done     int
	)
	sem := make(chan struct{}, ttsParallel)
	for i := range short.Shots {
		if !wanted[i] {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, shot Shot) {
			defer wg.Done()
			defer func() { <-sem }()
			clip, err := a.synthesizeOne(ctx, rt, short, assetDir, i, shot)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("第 %d 镜配音失败：%w", i+1, err)
				}
				return
			}
			clips[i] = clip
			done++
			progress(fmt.Sprintf("配音 %d/%d", done, len(wanted)))
		}(i, short.Shots[i])
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return clips, nil
}

func (a *DraftAssembler) synthesizeOne(ctx context.Context, rt Runtime, short *Short, assetDir string, i int, shot Shot) (ttsClip, error) {
	line := strings.TrimSpace(shot.Narration)
	req, err := a.BuildRequest(ctx, line, short.AccountID)
	if err != nil {
		return ttsClip{}, err
	}
	if formatted, ferr := spokenlines.Format(line); ferr == nil {
		req.SpokenLines = spokenlines.Lines(formatted)
	}
	delivery, err := a.Produce(ctx, req)
	if err != nil {
		var gate *narration.QualityGateError
		if !errors.As(err, &gate) || len(delivery.Audio) == 0 {
			return ttsClip{}, err
		}
	}
	clipPath := filepath.Join(assetDir, fmt.Sprintf("voice_%02d.mp3", i+1))
	if err := os.WriteFile(clipPath, delivery.Audio, 0o644); err != nil {
		return ttsClip{}, err
	}
	// 真实音频时长优先；拿不到再用最后一条字幕的结束时间；再兜底按字数估。
	dur := probeDuration(rt.FFprobePath, clipPath)
	if dur <= 0 {
		dur = delivery.Duration
	}
	if dur <= 0 {
		dur = float64(len([]rune(line))) * 0.28
	}
	return ttsClip{path: clipPath, duration: dur, captions: delivery.Captions}, nil
}

// spreadCaptions 没有逐字时间戳时，把一句话按标点切成短条，按字数比例铺在 [start, end] 上。
func spreadCaptions(line string, start, end float64) []jobCaption {
	return spreadCaptionsN(line, start, end, captionMaxRunes)
}

func spreadCaptionsN(line string, start, end float64, maxRunes int) []jobCaption {
	pieces := splitCaptionPiecesN(line, maxRunes)
	total := 0
	for _, p := range pieces {
		total += len([]rune(p))
	}
	if total == 0 || end <= start {
		return nil
	}
	// 两头各留一点，字幕不要贴着镜头切换点出现/消失。
	pad := 0.15
	if span := end - start; span < 1.0 {
		pad = 0
	}
	t := start + pad
	per := (end - start - 2*pad) / float64(total)
	out := make([]jobCaption, 0, len(pieces))
	for _, p := range pieces {
		d := per * float64(len([]rune(p)))
		out = append(out, jobCaption{Text: p, StartS: t, EndS: t + d})
		t += d
	}
	return out
}

// splitCaptionPieces 按中文标点切句，超长的再按字数硬切；标点本身不进字幕。
func splitCaptionPieces(line string) []string {
	return splitCaptionPiecesN(line, captionMaxRunes)
}

// splitCaptionPiecesN 先按标点切成短语，超过 maxRunes 的短语再均分成几段
// （15 字限 10 → 8+7，而不是 10+5），字幕长短才匀。
func splitCaptionPiecesN(line string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = captionMaxRunes
	}
	var phrases []string
	var cur []rune
	flush := func() {
		if s := strings.TrimSpace(string(cur)); s != "" {
			phrases = append(phrases, s)
		}
		cur = cur[:0]
	}
	for _, r := range strings.TrimSpace(line) {
		switch r {
		case '，', '。', '！', '？', '；', '、', '：', ',', '.', '!', '?', ';', '\n', '…', '—', '"', '\u201c', '\u201d', '「', '」', '（', '）', '(', ')':
			flush()
		default:
			cur = append(cur, r)
		}
	}
	flush()
	var pieces []string
	for _, p := range phrases {
		runes := []rune(p)
		if len(runes) <= maxRunes {
			pieces = append(pieces, p)
			continue
		}
		parts := (len(runes) + maxRunes - 1) / maxRunes
		base, extra := len(runes)/parts, len(runes)%parts
		at := 0
		for k := 0; k < parts; k++ {
			n := base
			if k < extra {
				n++
			}
			pieces = append(pieces, string(runes[at:at+n]))
			at += n
		}
	}
	return pieces
}

// probeDuration 用 ffprobe 读时长，失败返回 0。
func probeDuration(ffprobe, path string) float64 {
	for _, bin := range ffprobeCandidates(ffprobe) {
		out, err := exec.Command(bin, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
		if err != nil {
			continue
		}
		var d float64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d); err == nil && d > 0 {
			return d
		}
	}
	return 0
}

// probeHasAudio 判断视频里有没有音轨；探不到 ffprobe 时按「有」处理（模型默认出声）。
func probeHasAudio(ffprobe, path string) bool {
	for _, bin := range ffprobeCandidates(ffprobe) {
		out, err := exec.Command(bin, "-v", "error", "-select_streams", "a", "-show_entries", "stream=codec_type", "-of", "csv=p=0", path).Output()
		if err != nil {
			continue
		}
		return strings.Contains(string(out), "audio")
	}
	return true
}

func ffprobeCandidates(configured string) []string {
	if strings.TrimSpace(configured) != "" {
		return []string{configured, "ffprobe"}
	}
	return []string{"ffprobe"}
}

func renderSRT(caps []jobCaption) string {
	var b strings.Builder
	for i, c := range caps {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(c.StartS), srtTime(c.EndS), c.Text)
	}
	return b.String()
}

func srtTime(s float64) string {
	ms := int64(s*1000 + 0.5)
	h := ms / 3600000
	ms -= h * 3600000
	m := ms / 60000
	ms -= m * 60000
	sec := ms / 1000
	ms -= sec * 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, sec, ms)
}

func (a *DraftAssembler) draftName(short *Short) string {
	prefix := "AI短片"
	if a.AccountName != nil {
		if n := strings.TrimSpace(a.AccountName(short.AccountID)); n != "" {
			prefix = n
		}
	}
	title := strings.TrimSpace(short.Headline)
	if title == "" {
		title = short.Title
	}
	title = strings.NewReplacer(`\`, "_", "/", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_").Replace(title)
	if runes := []rune(title); len(runes) > 20 {
		title = string(runes[:20])
	}
	return fmt.Sprintf("%s_%s_%s", prefix, title, time.Now().Format("0102-1504"))
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}
