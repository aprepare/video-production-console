package montageplan

import (
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
// image shots, requires media_mix_policy targets for all three kinds, caps
// one caption at 4s, and expects qc_expectations exactly as encoded here.

// CaptionMode selects the caption policy of a v2 plan.
type CaptionMode string

const (
	CaptionOff            CaptionMode = "off"
	CaptionHighlightsOnly CaptionMode = "highlights_only"
)

// Known media_mix_policy presets; unknown names fail instead of defaulting.
const (
	mixPresetMovieMix   = "movie_mix"
	mixPresetImageVideo = "image_video"
)

// The only transition on the conservative whitelist is the verified
// cross dissolve from montage-style-policy.v2.json.
const (
	v2TransitionPreset    = "cross_dissolve"
	v2TransitionDurationS = 0.466666
)

// v2OpeningTitleEndS keeps the opening title inside the 3-5s window.
const v2OpeningTitleEndS = 4.0

// v2NeutralMatchReason is the P1 template reason; Task 9 replaces it with
// real recall evidence. The structure is already parser-complete.
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

type GraphicsV2 struct {
	OpeningTitle  TextOverlayV2   `json:"opening_title"`
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
	Text   string      `json:"text"`
	StartS float64     `json:"start_s"`
	EndS   float64     `json:"end_s"`
	Kind   CaptionKind `json:"kind"`
	Style  string      `json:"style"`
	Intro  string      `json:"intro"`
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

// BuildV2 writes an approved typed production_plan.json (plan_version 2.0).
// Build (v1) stays the production path until Task 12 wires this in.
func BuildV2(opts Options) error {
	ctx, err := loadPlanContext(opts)
	if err != nil {
		return err
	}
	policy, presetName, err := mixPolicyForPreset(opts.MixPreset)
	if err != nil {
		return err
	}
	mode := opts.CaptionMode
	if mode == "" {
		mode = CaptionHighlightsOnly
	}
	if mode != CaptionOff && mode != CaptionHighlightsOnly {
		return fmt.Errorf("unknown caption mode %q", mode)
	}

	clips, err := sampleMedia(ctx.mediaIndex, ctx.mediaRoot, ctx.limit, ctx.manifest.TaskID, false)
	if err != nil {
		return err
	}
	if len(clips) == 0 {
		return fmt.Errorf("media index produced no usable clips")
	}
	candidates := make([]rankedCandidate, 0, len(clips))
	for _, clip := range clips {
		candidates = append(candidates, rankedCandidate{Item: clip})
	}
	selection, quotaWarnings, err := selectTimelineV2(candidates, ctx.duration, ctx.manifest.TaskID, policy)
	if err != nil {
		return err
	}
	timeline := buildV2Timeline(selection)

	title, _ := titlePair(onScreenTitleSource(ctx.manifest.NonSecretSettings.DraftDisplayName))
	openingTitle := TextOverlayV2{
		Text:   title,
		StartS: 0,
		EndS:   v2OpeningTitleEndS,
		Style:  "opening_title_v1",
		Intro:  "none",
	}

	captions := CaptionPolicy{Mode: mode, Items: []CaptionItem{}}
	var captionWarnings []string
	if mode == CaptionHighlightsOnly {
		captions.TargetCoverageMin = captionCoverageMin
		captions.TargetCoverageMax = captionCoverageMax
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
		// The parser rejects captions overlapping the opening title, so
		// sentences that start under the title never become candidates.
		titleEndMS := int64(math.Round(openingTitle.EndS * 1000))
		eligible := make([]TimedSentence, 0, len(sentences))
		for _, sentence := range sentences {
			if sentence.StartMS >= titleEndMS {
				eligible = append(eligible, sentence)
			}
		}
		narrationMS := int64(math.Round(ctx.duration * 1000))
		captions.Items, captionWarnings = selectHighlightCaptions(eligible, narrationMS, mode)
	}

	notes := []string{"deterministic console planner v2"}
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
			OpeningTitle:  openingTitle,
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
		ExecutionActions: []string{
			"核验全部文件路径",
			"按timeline创建静音画面片段并按motion设置缩放与关键帧",
			"图片使用首尾uniform_scale关键帧实现Ken Burns",
			"添加每个相邻镜头之间0.467秒 verified 叠化",
			"片头标题仅3-5秒；章节标签短暂出现；重点字幕逐条独立可编辑",
			"添加用户旁白、verified BGM和稀疏verified SFX并设置目标dB",
			"不创建文稿匹配轨或自动听写轨",
			"运行草稿和方案验证器",
		},
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

func nilIfEmptyString(path string) *string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return &path
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
			ShotNo:     i + 1,
			StartS:     segment.StartS,
			EndS:       segment.EndS,
			MediaKind:  string(segment.Item.Kind),
			SourceID:   segment.Item.ID,
			ShotID:     segment.Item.ShotID,
			SourcePath: segment.Item.AbsPath,
			Match: MatchEvidence{
				Level:    "neutral",
				Score:    0,
				IntentID: fmt.Sprintf("seg-%03d", i+1),
				Reason:   v2NeutralMatchReason,
			},
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

// v2SourceWindow places the slot inside the clip's real source range at 1.0x.
// Whole-source clips keep a 1s lead-in when there is room, like v1.
func v2SourceWindow(item mediaItem, slot float64) (in, out float64) {
	if item.hasShotRange() {
		in = roundSFXStart(item.SourceInSeconds)
		out = roundSFXStart(in + slot)
		if limit := roundSFXStart(item.SourceOutSeconds); out > limit {
			out = limit
		}
		return in, out
	}
	if item.DurationSeconds >= slot+2 {
		in = 1.0
	}
	out = roundSFXStart(in + slot)
	if item.DurationSeconds > 0 && out > item.DurationSeconds {
		out = roundSFXStart(item.DurationSeconds)
		in = roundSFXStart(out - slot)
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
		return MotionPlan{Preset: "kenburns_in", ScaleFrom: 1.00, ScaleTo: 1.06, XFrom: 0, XTo: 0.02}
	}
	return MotionPlan{Preset: "kenburns_out", ScaleFrom: 1.06, ScaleTo: 1.00, XFrom: 0, XTo: -0.02}
}

// movieMotionPlan keeps movie footage steady inside the 1.00-1.05 scale
// window with full opacity, replacing v1's uniform 1.4x/0.5 treatment.
func movieMotionPlan() MotionPlan {
	return MotionPlan{Preset: "steady", ScaleFrom: 1.02, ScaleTo: 1.02}
}

// brollMotionCycle rotates steady/push/pull so no preset ever runs more than
// twice in a row across consecutive B-roll shots.
var brollMotionCycle = [3]MotionPlan{
	{Preset: "steady", ScaleFrom: 1.03, ScaleTo: 1.03},
	{Preset: "push", ScaleFrom: 1.00, ScaleTo: 1.05},
	{Preset: "pull", ScaleFrom: 1.05, ScaleTo: 1.00},
}

func brollMotionPlan(ordinal int) MotionPlan {
	return brollMotionCycle[ordinal%len(brollMotionCycle)]
}
