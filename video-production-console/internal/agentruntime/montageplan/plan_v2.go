package montageplan

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// The typed v2 contract below mirrors the authoritative skill parser
// (jianying-montage-draft/scripts/production_plan_v2.py) field by field.
// That parser rejects unknown fields, forbids source_in_s/source_out_s on
// image shots, requires media_mix_policy targets for all three kinds, and
// expects qc_expectations exactly as encoded here. Highlight captions stay
// 2-4s. Spoken captions stay off until typography is ready.

// CaptionMode selects the caption policy of a v2 plan.
type CaptionMode string

const (
	CaptionOff            CaptionMode = "off"
	CaptionHighlightsOnly CaptionMode = "highlights_only"
	CaptionSpoken         CaptionMode = "spoken"
)

// SpokenCaptionsEnabled paints SRT lines on the 字幕 track.
//
// 2026-08-18：口播字幕排版不好，先整轨屏蔽。板上标题/副标题仍保留。
// 实现和单测留着，改回 true 即可恢复默认 spoken。
const SpokenCaptionsEnabled = false

// Known media_mix_policy presets; unknown names fail instead of defaulting.
const (
	mixPresetMovieMix     = "movie_mix"
	mixPresetImageVideo   = "image_video"
	mixPresetMovieCatalog = "movie_catalog"

	SelectModeLandscape    = "landscape"
	SelectModeMovieCatalog = "movie_catalog"
)

// The only transition on the conservative whitelist is the verified
// cross dissolve from montage-style-policy.v2.json.
const (
	v2TransitionPreset    = "cross_dissolve"
	v2TransitionDurationS = 0.466666
)

// v2NeutralMatchReason is the fallback when a shot has no recall evidence.
const v2NeutralMatchReason = "P1中性过渡镜头：语义匹配接入前使用模板理由"

// ProductionPlanV2 is the typed v2 plan written to production_plan.json.
type ProductionPlanV2 struct {
	PlanVersion        string           `json:"plan_version"`
	Status             string           `json:"status"`
	ModelRole          string           `json:"model_role"`
	ProjectName        string           `json:"project_name"`
	ProjectDurationS   float64          `json:"project_duration_s"`
	Concurrency        ConcurrencyV2    `json:"concurrency"`
	Inputs             InputsV2         `json:"inputs"`
	MediaMixPolicy     MediaMixPolicyV2 `json:"media_mix_policy"`
	Timeline           []TimelineShotV2 `json:"timeline"`
	Graphics           GraphicsV2       `json:"graphics"`
	Audio              AudioV2          `json:"audio"`
	QCExpectations     QCExpectations   `json:"qc_expectations"`
	ExecutionActions   []string         `json:"execution_actions"`
	KnownMissingAssets []any            `json:"known_missing_assets"`
	PlannerNotes       []string         `json:"planner_notes"`
	Approval           ApprovalV2       `json:"approval"`
}

type ConcurrencyV2 struct {
	JobID              string `json:"job_id"`
	WorkspacePath      string `json:"workspace_path"`
	MediaIndexLock     string `json:"media_index_lock"`
	RegistrationLock   string `json:"registration_lock"`
	RegistrationStatus string `json:"registration_status"`
}

type InputsV2 struct {
	ContextText     *string `json:"context_text"`
	Narration       string  `json:"narration"`
	SRTContextOnly  *string `json:"srt_context_only"`
	BackgroundBoard string  `json:"background_board"`
	MediaIndex      string  `json:"media_index"`
}

type MediaMixPolicyV2 struct {
	Preset                  string       `json:"preset"`
	Basis                   string       `json:"basis"`
	Targets                 MixTargetsV2 `json:"targets"`
	MaxSourceUses           int          `json:"max_source_uses"`
	MinSegmentsBetweenReuse int          `json:"min_segments_between_reuse"`
}

// MixTargetsV2 always encodes all three kinds; the skill parser requires
// every kind even when a preset zeroes one of them out.
type MixTargetsV2 struct {
	Broll QuotaRangeV2 `json:"broll"`
	Movie QuotaRangeV2 `json:"movie"`
	Image QuotaRangeV2 `json:"image"`
}

type QuotaRangeV2 struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// TimelineShotV2 is one planned shot. SourceInS/SourceOutS are pointers so
// image shots omit the keys entirely while a movie in-point of 0 still
// serializes; the parser rejects image shots that declare a source window.
type TimelineShotV2 struct {
	ShotNo           int            `json:"shot_no"`
	StartS           float64        `json:"start_s"`
	EndS             float64        `json:"end_s"`
	MediaKind        string         `json:"media_kind"`
	SourceID         string         `json:"source_id"`
	ShotID           string         `json:"shot_id,omitempty"`
	SourcePath       string         `json:"source_path"`
	SourceInS        *float64       `json:"source_in_s,omitempty"`
	SourceOutS       *float64       `json:"source_out_s,omitempty"`
	Match            MatchEvidence  `json:"match"`
	Motion           MotionPlan     `json:"motion"`
	Opacity          float64        `json:"opacity"`
	SourceAudioMuted bool           `json:"source_audio_muted"`
	Look             string         `json:"look"`
	Transition       *TransitionV2  `json:"transition"`
	EmphasisEffect   map[string]any `json:"emphasis_effect"`
}

type MatchEvidence struct {
	Level    string  `json:"level"`
	Score    float64 `json:"score"`
	IntentID string  `json:"intent_id"`
	Reason   string  `json:"reason"`
}

type MotionPlan struct {
	Preset    string  `json:"preset"`
	ScaleFrom float64 `json:"scale_from"`
	ScaleTo   float64 `json:"scale_to"`
	XFrom     float64 `json:"x_from"`
	XTo       float64 `json:"x_to"`
	YFrom     float64 `json:"y_from"`
	YTo       float64 `json:"y_to"`
}

type TransitionV2 struct {
	Preset    string  `json:"preset"`
	DurationS float64 `json:"duration_s"`
}

type BrandTextV2 struct {
	Text         string  `json:"text"`
	CharsMin     int     `json:"chars_min"`
	CharsMax     int     `json:"chars_max"`
	SizeMin      float64 `json:"size_min"`
	Y            float64 `json:"y"`
	FullDuration bool    `json:"full_duration"`
}

type BoundaryLinesV2 struct {
	AssetWidthPx  int     `json:"asset_width_px"`
	AssetHeightPx int     `json:"asset_height_px"`
	TopY          float64 `json:"top_y"`
	BottomY       float64 `json:"bottom_y"`
	FullDuration  bool    `json:"full_duration"`
}

type GraphicsV2 struct {
	OpeningTitle  *TextOverlayV2  `json:"opening_title,omitempty"`
	Title         BrandTextV2     `json:"title"`
	Subtitle      BrandTextV2     `json:"subtitle"`
	BoundaryLines BoundaryLinesV2 `json:"boundary_lines"`
	ChapterLabels []TextOverlayV2 `json:"chapter_labels"`
	Captions      CaptionPolicy   `json:"captions"`
}

type TextOverlayV2 struct {
	Text   string  `json:"text"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
	Style  string  `json:"style"`
	Intro  string  `json:"intro"`
}

type CaptionPolicy struct {
	Mode              CaptionMode   `json:"mode"`
	TargetCoverageMin float64       `json:"target_coverage_min"`
	TargetCoverageMax float64       `json:"target_coverage_max"`
	Items             []CaptionItem `json:"items"`
}

type CaptionItem struct {
	Text   string        `json:"text"`
	StartS float64       `json:"start_s"`
	EndS   float64       `json:"end_s"`
	Kind   CaptionKind   `json:"kind"`
	Style  string        `json:"style"`
	Intro  string        `json:"intro"`
	Spans  []CaptionSpan `json:"spans,omitempty"`
}

// CaptionSpan enlarges one keyword inside a caption. Offsets count runes of the
// caption text and never overlap.
type CaptionSpan struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Style string `json:"style"`
}

type QCExpectations struct {
	MaxObviousEffectsPer30S   int  `json:"max_obvious_effects_per_30s"`
	FullCaptionTrackForbidden bool `json:"full_caption_track_forbidden"`
}

type AudioV2 struct {
	Narration map[string]any   `json:"narration"`
	BGM       map[string]any   `json:"bgm"`
	SFX       []map[string]any `json:"sfx"`
}

type ApprovalV2 struct {
	ApprovedBy string `json:"approved_by"`
	ApprovedAt string `json:"approved_at"`
}

// mixPolicyForPreset resolves a task-level preset name. Unknown presets fail
// outright per plan §5.4 instead of silently using the default.
func mixPolicyForPreset(preset string) (mixPolicy, string, error) {
	switch strings.TrimSpace(preset) {
	case "", mixPresetMovieMix:
		return movieMixPolicy(), mixPresetMovieMix, nil
	case mixPresetImageVideo:
		return imageVideoPolicy(), mixPresetImageVideo, nil
	case mixPresetMovieCatalog:
		return movieCatalogPolicy(), mixPresetMovieCatalog, nil
	default:
		return mixPolicy{}, "", fmt.Errorf("unknown media mix preset %q", preset)
	}
}

func mixTargetsV2(policy mixPolicy) MixTargetsV2 {
	rangeFor := func(kind mediaKind) QuotaRangeV2 {
		target := policy.Targets[kind]
		return QuotaRangeV2{Min: target.Min, Max: target.Max}
	}
	return MixTargetsV2{
		Broll: rangeFor(mediaKindBroll),
		Movie: rangeFor(mediaKindMovie),
		Image: rangeFor(mediaKindImage),
	}
}

func movieCatalogSelect(opts Options) bool {
	mode := strings.TrimSpace(opts.SelectMode)
	preset := strings.TrimSpace(opts.MixPreset)
	return mode == SelectModeMovieCatalog || preset == mixPresetMovieCatalog
}

// BuildV2 writes production_plan.json (plan_version 2.0) with catalog recall
// when media_catalog_path is set. Scenic and movie_catalog tasks both follow
// narration; scenic tasks fall back to media_index landscape only when the
// catalog has no usable shots. CaptionMode defaults to off.
func BuildV2(opts Options) error {
	ctx, err := loadPlanContext(opts)
	if err != nil {
		return err
	}
	if movieCatalogSelect(opts) && strings.TrimSpace(opts.MixPreset) == "" {
		opts.MixPreset = mixPresetMovieCatalog
	}
	policy, presetName, err := mixPolicyForPreset(opts.MixPreset)
	if err != nil {
		return err
	}
	mode := opts.CaptionMode
	if mode == "" {
		if SpokenCaptionsEnabled {
			mode = CaptionSpoken
		} else {
			mode = CaptionOff
		}
	}
	if mode != CaptionOff && mode != CaptionHighlightsOnly && mode != CaptionSpoken {
		return fmt.Errorf("unknown caption mode %q", mode)
	}

	if opts.CatalogPath == "" {
		opts.CatalogPath = strings.TrimSpace(ctx.manifest.NonSecretSettings.MediaCatalogPath)
	}
	catalog, closer, err := resolveCatalog(opts)
	if err != nil {
		return err
	}
	defer closer()

	notes := []string{"deterministic console planner v2"}
	var candidates []rankedCandidate
	var intents []NarrativeIntent
	if catalog != nil {
		sentences, sentErr := loadPlanSentences(ctx)
		if sentErr != nil {
			return sentErr
		}
		analyzer := opts.Analyzer
		if analyzer == nil {
			analyzer = LocalIntentAnalyzer{}
		}
		var intentErr error
		intents, intentErr = analyzer.Analyze(context.Background(), sentences)
		if intentErr != nil || len(intents) == 0 {
			intents, _ = LocalIntentAnalyzer{}.Analyze(context.Background(), sentences)
		}
		intents = enrichIntentsWithSpokenCues(intents, false)
		ranked, matchWarnings, rankErr := rankLibrary(context.Background(), intents, catalog, opts.Embedder)
		if rankErr != nil {
			return rankErr
		}
		// Optional chat rerank of the already-scored shortlist. The
		// embedding scan has already happened inside rankLibrary.
		if opts.ShotSelector != nil {
			jobs := buildShotSelectJobs(intents, ranked)
			if picks, pickErr := opts.ShotSelector.SelectShots(context.Background(), jobs); pickErr == nil {
				var pickNotes []string
				ranked, pickNotes = applyShotSelections(ranked, picks)
				notes = append(notes, pickNotes...)
			} else {
				notes = append(notes, "llm_shot_select_fallback: "+pickErr.Error())
			}
		}
		candidates = resolveCandidatePaths(ranked, ctx.mediaRoot)
		notes = append(notes, matchWarnings...)
		padded, padNotes, padErr := padCatalogCandidates(context.Background(), catalog, candidates, ctx.duration)
		if padErr != nil {
			return padErr
		}
		candidates = resolveCandidatePaths(padded, ctx.mediaRoot)
		notes = append(notes, padNotes...)
	}
	if movieCatalogSelect(opts) {
		if len(candidates) == 0 {
			return fmt.Errorf("movie catalog produced no usable shots")
		}
	} else if len(candidates) == 0 {
		clips, sampleErr := sampleMediaMatching(ctx.mediaIndex, ctx.mediaRoot, ctx.limit, ctx.manifest.TaskID, false, isLandscapeItem)
		if sampleErr != nil {
			return sampleErr
		}
		candidates = rankedFromIndex(clips)
		if catalog != nil {
			notes = append(notes, "match_candidates_insufficient: catalog recall empty, falling back to media index")
		}
		if len(candidates) == 0 {
			return fmt.Errorf("media index produced no 风景/景观 clips")
		}
	}
	selection, quotaWarnings, err := selectTimelineV2(candidates, ctx.duration, ctx.manifest.TaskID, policy, intents)
	if err != nil {
		return err
	}
	timeline := buildV2Timeline(selection)

	captions := CaptionPolicy{Mode: mode, Items: []CaptionItem{}}
	var captionWarnings []string
	var captionPack CaptionPack
	if mode == CaptionHighlightsOnly || mode == CaptionSpoken {
		var sentences []TimedSentence
		if ctx.srtPath != "" {
			file, err := os.Open(ctx.srtPath)
			if err != nil {
				return fmt.Errorf("open subtitle srt: %w", err)
			}
			sentences, err = parseSRTSentences(file)
			file.Close()
			if err != nil {
				return fmt.Errorf("parse subtitle srt: %w", err)
			}
		}
		narrationMS := int64(math.Round(ctx.duration * 1000))
		if mode == CaptionSpoken {
			// Spoken captions still come from the user's SRT timings. Line
			// breaks use the local splitter until CaptionLLMLineBreakerEnabled
			// is turned back on; keyword highlighting stays off with it.
			captions.TargetCoverageMin = 0
			captions.TargetCoverageMax = 1
			captions.Items, captionPack, captionWarnings = spokenCaptionsFromSentences(
				sentences, narrationMS, opts.LineBreaker, captionLinesCachePath(ctx))
		} else {
			captions.TargetCoverageMin = captionCoverageMin
			captions.TargetCoverageMax = captionCoverageMax
			captions.Items, captionWarnings = selectHighlightCaptions(sentences, narrationMS, mode)
		}
	}
	title, subtitle := boardTitles(ctx, captionPack.BoardTitle, captionPack.BoardSubtitle)
	if strings.TrimSpace(ctx.manifest.NonSecretSettings.BoardTitle) == "" &&
		loadProjectPublishingPackage(ctx) == nil &&
		fitModelBoardTitle(captionPack.BoardTitle) != "" {
		captionWarnings = append(captionWarnings, "board_titles_from_caption_model")
	}
	if err := captionLineBreakerFailure(captionWarnings); err != nil {
		return err
	}

	for _, warning := range quotaWarnings {
		notes = append(notes, fmt.Sprintf("%s: kind=%s wanted=%.4f actual=%.4f",
			warning.Code, warning.Kind, warning.Wanted, warning.Actual))
	}
	notes = append(notes, captionWarnings...)

	workspace := filepath.Join(ctx.manifest.OutputDir, "workspace", ctx.manifest.JobID)
	plan := ProductionPlanV2{
		PlanVersion:      "2.0",
		Status:           "approved",
		ModelRole:        "planner",
		ProjectName:      title,
		ProjectDurationS: ctx.duration,
		Concurrency: ConcurrencyV2{
			JobID:              ctx.manifest.JobID,
			WorkspacePath:      workspace,
			MediaIndexLock:     "media-index-write",
			RegistrationLock:   "jianying-registration",
			RegistrationStatus: "not_started",
		},
		Inputs: InputsV2{
			ContextText:     nilIfEmptyString(ctx.scriptPath),
			Narration:       ctx.narration,
			SRTContextOnly:  nilIfEmptyString(ctx.srtPath),
			BackgroundBoard: ctx.background,
			MediaIndex:      ctx.mediaIndex,
		},
		MediaMixPolicy: MediaMixPolicyV2{
			Preset:                  presetName,
			Basis:                   "timeline_duration",
			Targets:                 mixTargetsV2(policy),
			MaxSourceUses:           policy.MaxSourceUses,
			MinSegmentsBetweenReuse: policy.MinSegmentsBetweenReuse,
		},
		Timeline: timeline,
		Graphics: GraphicsV2{
			Title: BrandTextV2{
				Text: title, CharsMin: boardTitleMinRunes, CharsMax: boardTitleMaxRunes,
				SizeMin: boardTextSize(len([]rune(title)), boardTitleSize), Y: 0.6, FullDuration: true,
			},
			Subtitle: BrandTextV2{
				Text: subtitle, CharsMin: boardTitleMinRunes, CharsMax: boardTitleMaxRunes,
				SizeMin: boardTextSize(len([]rune(subtitle)), boardSubtitleSize), Y: 0.49, FullDuration: true,
			},
			BoundaryLines: BoundaryLinesV2{
				AssetWidthPx: 1080, AssetHeightPx: 6, TopY: 0.38, BottomY: -0.38, FullDuration: true,
			},
			ChapterLabels: []TextOverlayV2{},
			Captions:      captions,
		},
		Audio: AudioV2{
			Narration: map[string]any{"path": ctx.narration, "db": 5, "start_s": 0},
			BGM:       bgmPlacement(ctx.resources.BGM),
			SFX:       buildSFXPlacements(ctx.duration, ctx.resources.SFX),
		},
		QCExpectations: QCExpectations{
			MaxObviousEffectsPer30S:   1,
			FullCaptionTrackForbidden: true,
		},
		ExecutionActions:   v2ExecutionActions(mode),
		KnownMissingAssets: []any{},
		PlannerNotes:       notes,
		Approval:           ApprovalV2{ApprovedBy: "video-console-script-runtime", ApprovedAt: "auto"},
	}

	if err := os.MkdirAll(filepath.Dir(opts.PlanPath), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(opts.PlanPath, encoded, 0o644)
}

func v2ExecutionActions(mode CaptionMode) []string {
	actions := []string{
		"核验全部文件路径",
		"按timeline创建静音画面片段并按motion设置缩放与关键帧",
		"图片使用首尾uniform_scale关键帧实现Ken Burns",
		"添加每个相邻镜头之间0.467秒 verified 叠化",
		"添加贯穿全片的背景框架、标题、副标题和上下红线",
	}
	if mode == CaptionSpoken || mode == CaptionHighlightsOnly {
		actions = append(actions, "按口播时间轴添加画面文字")
	}
	return append(actions,
		"添加用户旁白、verified BGM和稀疏verified SFX并设置目标dB",
		"运行草稿和方案验证器",
	)
}

func nilIfEmptyString(path string) *string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return &path
}

// captionLineBreakerFailure used to abort the plan when the HTTP caption
// model was unreachable. While CaptionLLMLineBreakerEnabled is false this
// should not fire on the production path.
func captionLineBreakerFailure(notes []string) error {
	for _, note := range notes {
		if strings.HasPrefix(note, "caption_lines_unavailable:") {
			return fmt.Errorf("%s", note)
		}
	}
	return nil
}

// spokenCaptionsFromSentences paints the narration as caption lines from the
// user's SRT timings. The HTTP line breaker is currently disabled; production
// uses the deterministic splitter. See CaptionLLMLineBreakerEnabled.
func spokenCaptionsFromSentences(
	sentences []TimedSentence,
	narrationMS int64,
	breaker LineBreaker,
	cachePath string,
) ([]CaptionItem, CaptionPack, []string) {
	if narrationMS <= 0 || len(sentences) == 0 {
		return []CaptionItem{}, CaptionPack{}, []string{"spoken_captions_empty: subtitle timings produced no on-screen lines"}
	}
	const minPieceUS int64 = 50_000
	pack, notes := resolveModelLines(breaker, sentences, cachePath)
	narrationUS := narrationMS * 1000
	items := make([]CaptionItem, 0, len(sentences)*2)
	var prevEndUS int64
	for index, sentence := range sentences {
		text := strings.TrimSpace(sentence.Text)
		if text == "" {
			continue
		}
		startUS := sentence.StartMS * 1000
		endUS := sentence.EndMS * 1000
		if endUS <= startUS {
			continue
		}
		span := float64(endUS - startUS)
		pieces := piecesFromModelLines(pack, index)
		if pieces == nil {
			pieces = splitSpokenLine(text, spokenLineMaxRunes)
		}
		for _, piece := range pieces {
			pieceStart := startUS + int64(math.Round(span*piece.startFrac))
			pieceEnd := startUS + int64(math.Round(span*piece.endFrac))
			if pieceStart < prevEndUS {
				pieceStart = prevEndUS
			}
			if pieceEnd > narrationUS {
				pieceEnd = narrationUS
			}
			if pieceEnd-pieceStart < minPieceUS {
				continue
			}
			items = append(items, CaptionItem{
				Text:   piece.text,
				StartS: float64(pieceStart) / 1_000_000,
				EndS:   float64(pieceEnd) / 1_000_000,
				Kind:   CaptionSpokenLine,
				Style:  "spoken_v1",
				Intro:  "none",
				Spans:  piece.spans,
			})
			prevEndUS = pieceEnd
		}
	}
	if len(items) == 0 {
		return items, pack, append(notes, "spoken_captions_empty: subtitle timings produced no on-screen lines")
	}
	return items, pack, notes
}

// piecesFromModelLines spreads one sentence's window across the model's lines in
// proportion to their length, which is the same mapping the deterministic
// splitter uses.
func piecesFromModelLines(pack CaptionPack, index int) []spokenPiece {
	if index >= len(pack.Groups) || len(pack.Groups[index]) == 0 {
		return nil
	}
	lines := pack.Groups[index]
	total := 0
	for _, line := range lines {
		total += len([]rune(line.Text))
	}
	if total == 0 {
		return nil
	}
	pieces := make([]spokenPiece, 0, len(lines))
	cursor := 0
	for _, line := range lines {
		runes := []rune(line.Text)
		startFrac := float64(cursor) / float64(total)
		cursor += len(runes)
		pieces = append(pieces, spokenPiece{
			text:      line.Text,
			startFrac: startFrac,
			endFrac:   float64(cursor) / float64(total),
			spans:     spokenKeywordSpans(runes, line.Keywords),
		})
	}
	return pieces
}

type spokenPiece struct {
	text               string
	startFrac, endFrac float64
	spans              []CaptionSpan
}

// buildV2Timeline turns the selected slots into typed shots with per-kind
// motion, a verified transition, template match evidence and a null
// emphasis effect (nothing on the conservative whitelist is verified yet).
func buildV2Timeline(selection []plannedMedia) []TimelineShotV2 {
	shots := make([]TimelineShotV2, 0, len(selection))
	imageOrdinal, brollOrdinal := 0, 0
	for i, segment := range selection {
		slot := segment.EndS - segment.StartS
		shot := TimelineShotV2{
			ShotNo:           i + 1,
			StartS:           segment.StartS,
			EndS:             segment.EndS,
			MediaKind:        string(segment.Item.Kind),
			SourceID:         segment.Item.ID,
			ShotID:           segment.Item.ShotID,
			SourcePath:       segment.Item.AbsPath,
			Match:            matchEvidenceOrTemplate(segment, i),
			Opacity:          1.0,
			SourceAudioMuted: true,
			Look:             "none",
			Transition:       &TransitionV2{Preset: v2TransitionPreset, DurationS: v2TransitionDurationS},
			EmphasisEffect:   nil,
		}
		switch segment.Item.Kind {
		case mediaKindImage:
			shot.Motion = imageMotionPlan(imageOrdinal)
			imageOrdinal++
		case mediaKindMovie:
			in, out := v2SourceWindow(segment.Item, slot)
			shot.SourceInS, shot.SourceOutS = &in, &out
			shot.Motion = movieMotionPlan()
		default:
			in, out := v2SourceWindow(segment.Item, slot)
			shot.SourceInS, shot.SourceOutS = &in, &out
			shot.Motion = brollMotionPlan(brollOrdinal)
			brollOrdinal++
		}
		shots = append(shots, shot)
	}
	return shots
}

// v2SourceWindow places the slot inside the clip's real source range at 1.5x.
// Whole-source clips keep a 1s lead-in when there is room, like v1.
func v2SourceWindow(item mediaItem, slot float64) (in, out float64) {
	source := roundSFXStart(v2SourceNeed(slot))
	if item.hasShotRange() {
		in = roundSFXStart(item.SourceInSeconds)
		out = roundSFXStart(in + source)
		if limit := roundSFXStart(item.SourceOutSeconds); out > limit {
			out = limit
		}
		return in, out
	}
	if item.DurationSeconds >= source+2 {
		in = 1.0
	}
	out = roundSFXStart(in + source)
	if item.DurationSeconds > 0 && out > item.DurationSeconds {
		out = roundSFXStart(item.DurationSeconds)
		in = roundSFXStart(out - source)
		if in < 0 {
			in = 0
		}
	}
	return in, out
}

// imageMotionPlan alternates seek-safe Ken Burns directions so consecutive
// stills do not all push the same way; pans stay within 3% of the frame.
func imageMotionPlan(ordinal int) MotionPlan {
	if ordinal%2 == 0 {
		return MotionPlan{Preset: "kenburns_in", ScaleFrom: 1.20, ScaleTo: 1.26, XFrom: 0, XTo: 0.02}
	}
	return MotionPlan{Preset: "kenburns_out", ScaleFrom: 1.26, ScaleTo: 1.20, XFrom: 0, XTo: -0.02}
}

// movieMotionPlan keeps movie footage at 1.2x so 16:9 fills the 1080×730 window.
func movieMotionPlan() MotionPlan {
	return MotionPlan{Preset: "steady", ScaleFrom: 1.20, ScaleTo: 1.20}
}

// brollMotionCycle rotates steady/push/pull so no preset ever runs more than
// twice in a row across consecutive B-roll shots.
var brollMotionCycle = [3]MotionPlan{
	{Preset: "steady", ScaleFrom: 1.20, ScaleTo: 1.20},
	{Preset: "push", ScaleFrom: 1.20, ScaleTo: 1.26},
	{Preset: "pull", ScaleFrom: 1.26, ScaleTo: 1.20},
}

func brollMotionPlan(ordinal int) MotionPlan {
	return brollMotionCycle[ordinal%len(brollMotionCycle)]
}

func matchEvidenceOrTemplate(segment plannedMedia, index int) MatchEvidence {
	intentID := segment.Match.IntentID
	if intentID == "" {
		intentID = fmt.Sprintf("seg-%03d", index+1)
	}
	if segment.Match.Level != "" && segment.Match.Reason != "" {
		match := segment.Match
		match.IntentID = intentID
		return match
	}
	return MatchEvidence{
		Level:    "neutral",
		Score:    segment.Match.Score,
		IntentID: intentID,
		Reason:   v2NeutralMatchReason,
	}
}

func loadPlanSentences(ctx *planContext) ([]TimedSentence, error) {
	if ctx.srtPath == "" {
		return nil, nil
	}
	file, err := os.Open(ctx.srtPath)
	if err != nil {
		return nil, fmt.Errorf("open subtitle srt: %w", err)
	}
	defer file.Close()
	sentences, err := parseSRTSentences(file)
	if err != nil {
		return nil, fmt.Errorf("parse subtitle srt: %w", err)
	}
	return sentences, nil
}

func resolveCandidatePaths(candidates []rankedCandidate, mediaRoot string) []rankedCandidate {
	for i := range candidates {
		if path, err := resolveMediaPath(mediaRoot, candidates[i].Item.RelativePath); err == nil {
			candidates[i].Item.AbsPath = path
		}
	}
	return candidates
}
