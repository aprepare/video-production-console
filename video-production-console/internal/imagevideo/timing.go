package imagevideo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unicode"

	"video-production-console/internal/domain"
	"video-production-console/internal/narration"
)

const (
	firstThirtySecondsUS = int64(30_000_000)
	slideshowEarlyMinUS  = int64(4_300_000)
	slideshowEarlyMaxUS  = int64(4_600_000)
	slideshowEarlyAimUS  = int64(4_450_000)
	slideshowLaterMinUS  = int64(6_000_000)
	MaxTimedScenes       = 60
)

var sceneMotionCycle = []string{
	"subtle_zoom_in",
	"subtle_zoom_out",
	"subtle_pan_left",
	"subtle_pan_right",
}

// Scene is one continuous, editable timeline segment. Title is descriptive
// metadata only; the draft builder must not create an editable title track.
type Scene struct {
	Ordinal                  int      `json:"ordinal"`
	ImageProjectItemID       string   `json:"image_project_item_id"`
	SourceItemIDs            []string `json:"source_item_ids"`
	InputImageSHA256         string   `json:"input_image_sha256"`
	Text                     string   `json:"text"`
	Title                    string   `json:"title,omitempty"`
	Motion                   string   `json:"motion"`
	StartUS                  int64    `json:"start_us"`
	EndUS                    int64    `json:"end_us"`
	TimelineDurationUS       int64    `json:"timeline_duration_us"`
	RequestedDurationSeconds *int     `json:"requested_duration_seconds,omitempty"`
	ActualDurationUS         *int64   `json:"actual_duration_us,omitempty"`
}

type timedSource struct {
	item   domain.ImageProjectItem
	sha256 string
	start  int
	end    int
}

type sceneCut struct {
	startWord int
	endWord   int
	itemIndex int
}

// ChooseVideoTier returns the smallest supported generated-video duration
// that fully covers the narration segment.
func ChooseVideoTier(durationUS int64) (int, error) {
	switch {
	case durationUS <= 0:
		return 0, fmt.Errorf("scene duration must be positive")
	case durationUS <= 6_000_000:
		return 6, nil
	case durationUS <= 10_000_000:
		return 10, nil
	case durationUS <= 15_000_000:
		return 15, nil
	default:
		return 0, fmt.Errorf("scene duration exceeds the 15 second video tier")
	}
}

// BuildScenesFromTiming cuts the final narration into one image scene per
// duration window: 4.3–4.6s in the first 30 seconds, then at least 6s.
// Scene count follows the spoken duration, not the ZIP 18-image cap, and is
// hard-capped at MaxTimedScenes to match image_projects.image_count.
func BuildScenesFromTiming(timing narration.WordTimingDocument) ([]Scene, error) {
	if len(timing.Words) == 0 || timing.Duration <= 0 {
		return nil, fmt.Errorf("word timings are required")
	}
	validatedTiming, err := narration.NewWordTimingDocument(timing.Script, timing.Provider, timing.Hash, timing.Words)
	if err != nil {
		return nil, fmt.Errorf("invalid word timing document: %w", err)
	}
	if timing.ScriptHash != "" && timing.ScriptHash != validatedTiming.ScriptHash {
		return nil, fmt.Errorf("word timing script hash does not match its script")
	}
	if secondsToUS(timing.Duration) != secondsToUS(validatedTiming.Duration) {
		return nil, fmt.Errorf("word timing duration does not match its final word")
	}
	timing = validatedTiming
	wordItems := make([]int, len(timing.Words))
	cuts := buildSlideshowCuts(timing.Words, wordItems)
	if len(cuts) == 0 {
		return nil, fmt.Errorf("scene planner returned no scenes")
	}
	cuts = mergeCutsToMax(cuts, MaxTimedScenes)
	return scenesFromCuts(ModeSlideshow, timing, cuts, nil)
}

// BuildTimedScenes aligns ImageProject source text to the final word timings.
// It only cuts at word boundaries, covers the full narration timeline, and
// never places captions or editable titles in the output contract.
func BuildTimedScenes(mode OutputMode, items []domain.ImageProjectItem, timing narration.WordTimingDocument) ([]Scene, error) {
	if mode != ModeSlideshow && mode != ModeImageToVideo {
		return nil, fmt.Errorf("invalid image video output mode %q", mode)
	}
	if len(items) == 0 || len(timing.Words) == 0 || timing.Duration <= 0 {
		return nil, fmt.Errorf("image items and word timings are required")
	}
	validatedTiming, err := narration.NewWordTimingDocument(timing.Script, timing.Provider, timing.Hash, timing.Words)
	if err != nil {
		return nil, fmt.Errorf("invalid word timing document: %w", err)
	}
	if timing.ScriptHash != "" && timing.ScriptHash != validatedTiming.ScriptHash {
		return nil, fmt.Errorf("word timing script hash does not match its script")
	}
	if secondsToUS(timing.Duration) != secondsToUS(validatedTiming.Duration) {
		return nil, fmt.Errorf("word timing duration does not match its final word")
	}
	timing = validatedTiming
	sources, cover, err := prepareTimedSources(items, timing.Words)
	if err != nil {
		return nil, err
	}
	wordItems := make([]int, len(timing.Words))
	for index := range wordItems {
		wordItems[index] = -1
	}
	for sourceIndex, source := range sources {
		if cover != nil && sourceIndex == *cover {
			continue
		}
		for wordIndex := source.start; wordIndex <= source.end; wordIndex++ {
			wordItems[wordIndex] = sourceIndex
		}
	}
	for index, sourceIndex := range wordItems {
		if sourceIndex < 0 {
			return nil, fmt.Errorf("word timing %d is not covered by an image source", index)
		}
	}

	var cuts []sceneCut
	if mode == ModeSlideshow {
		cuts = buildSlideshowCuts(timing.Words, wordItems)
	} else {
		cuts, err = buildImageToVideoCuts(timing.Words, sources, cover)
		if err != nil {
			return nil, err
		}
	}
	if len(cuts) == 0 {
		return nil, fmt.Errorf("scene planner returned no scenes")
	}
	return scenesFromCuts(mode, timing, cuts, &timedSceneSources{sources: sources, cover: cover, wordItems: wordItems})
}

type timedSceneSources struct {
	sources   []timedSource
	cover     *int
	wordItems []int
}

func scenesFromCuts(mode OutputMode, timing narration.WordTimingDocument, cuts []sceneCut, bound *timedSceneSources) ([]Scene, error) {
	durationUS := secondsToUS(timing.Duration)
	scenes := make([]Scene, 0, len(cuts))
	previousEnd := int64(0)
	for index, cut := range cuts {
		endUS := durationUS
		if cut.endWord+1 < len(timing.Words) {
			endUS = secondsToUS(timing.Words[cut.endWord+1].StartTime)
		}
		if endUS <= previousEnd {
			endUS = secondsToUS(timing.Words[cut.endWord].EndTime)
		}
		if endUS <= previousEnd {
			return nil, fmt.Errorf("scene %d has a non-positive timeline duration", index+1)
		}
		text := joinTimedWords(timing.Words[cut.startWord : cut.endWord+1])
		scene := Scene{
			Ordinal:            index + 1,
			Text:               text,
			Title:              fallbackSceneTitle(text, index+1),
			Motion:             sceneMotionCycle[index%len(sceneMotionCycle)],
			StartUS:            previousEnd,
			EndUS:              endUS,
			TimelineDurationUS: endUS - previousEnd,
		}
		if bound != nil {
			sourceIndex := cut.itemIndex
			if index == 0 && bound.cover != nil {
				sourceIndex = *bound.cover
			}
			source := bound.sources[sourceIndex]
			sourceIDs := uniqueSourceIDs(cut, bound.wordItems, bound.sources)
			if index == 0 && bound.cover != nil {
				sourceIDs = prependUnique(bound.sources[*bound.cover].item.ID, sourceIDs)
			}
			scene.ImageProjectItemID = source.item.ID
			scene.SourceItemIDs = sourceIDs
			scene.InputImageSHA256 = source.sha256
			scene.Title = source.item.Title
		}
		if mode == ModeImageToVideo {
			tier, err := ChooseVideoTier(scene.TimelineDurationUS)
			if err != nil {
				return nil, err
			}
			scene.RequestedDurationSeconds = &tier
			scene.Motion = "none"
		}
		scenes = append(scenes, scene)
		previousEnd = endUS
	}
	if previousEnd != durationUS {
		return nil, fmt.Errorf("scene timeline ends at %d, narration ends at %d", previousEnd, durationUS)
	}
	if alignmentText(joinSceneText(scenes)) != alignmentText(joinTimedWords(timing.Words)) {
		return nil, fmt.Errorf("scene planning did not preserve all spoken text")
	}
	return scenes, nil
}

func mergeCutsToMax(cuts []sceneCut, max int) []sceneCut {
	if max < 1 || len(cuts) <= max {
		return cuts
	}
	for len(cuts) > max {
		last := cuts[len(cuts)-1]
		cuts[len(cuts)-2].endWord = last.endWord
		cuts = cuts[:len(cuts)-1]
	}
	return cuts
}

func fallbackSceneTitle(text string, sequence int) string {
	value := strings.TrimSpace(strings.TrimRight(text, "。！？!?；;，,、. "))
	runes := []rune(value)
	if len(runes) > 12 {
		value = string(runes[:12])
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Sprintf("镜头%d", sequence)
	}
	return value
}

func prepareTimedSources(items []domain.ImageProjectItem, words []narration.Word) ([]timedSource, *int, error) {
	content := make([]domain.ImageProjectItem, 0, len(items))
	var coverItem *domain.ImageProjectItem
	for index := range items {
		item := items[index]
		if item.Role == "cover" && len(items) > 1 && coverItem == nil {
			copyItem := item
			coverItem = &copyItem
			continue
		}
		content = append(content, item)
	}
	if len(content) == 0 {
		content = append(content, items...)
		coverItem = nil
	}
	allItemText := strings.Builder{}
	for _, item := range content {
		allItemText.WriteString(alignmentText(item.SourceText))
	}
	if allItemText.String() != alignmentText(joinTimedWords(words)) {
		return nil, nil, fmt.Errorf("image item source text does not align with final narration timings")
	}
	sources := make([]timedSource, 0, len(content)+1)
	wordIndex := 0
	for _, item := range content {
		target := alignmentText(item.SourceText)
		if target == "" {
			return nil, nil, fmt.Errorf("image item %s has no spoken source text", item.ID)
		}
		start := wordIndex
		accumulated := ""
		for wordIndex < len(words) && accumulated != target {
			accumulated += alignmentText(words[wordIndex].Text)
			if !strings.HasPrefix(target, accumulated) {
				return nil, nil, fmt.Errorf("word timing diverges inside image item %s", item.ID)
			}
			wordIndex++
		}
		if accumulated != target || wordIndex == start {
			return nil, nil, fmt.Errorf("word timing does not fully cover image item %s", item.ID)
		}
		for wordIndex < len(words) && alignmentText(words[wordIndex].Text) == "" {
			wordIndex++
		}
		sha, err := imageItemSHA256(item)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, timedSource{item: item, sha256: sha, start: start, end: wordIndex - 1})
	}
	if wordIndex != len(words) {
		return nil, nil, fmt.Errorf("word timings contain uncovered trailing text")
	}
	var coverIndex *int
	if coverItem != nil {
		sha, err := imageItemSHA256(*coverItem)
		if err != nil {
			return nil, nil, err
		}
		index := len(sources)
		sources = append(sources, timedSource{item: *coverItem, sha256: sha, start: sources[0].start, end: sources[0].end})
		coverIndex = &index
	}
	return sources, coverIndex, nil
}

func buildImageToVideoCuts(words []narration.Word, sources []timedSource, cover *int) ([]sceneCut, error) {
	cuts := []sceneCut{}
	for sourceIndex, source := range sources {
		if cover != nil && sourceIndex == *cover {
			continue
		}
		if source.start < 0 || source.end >= len(words) || source.start > source.end {
			continue
		}
		start := source.start
		for start <= source.end {
			cut := chooseCutAtMost(words, start, source.end, 15_000_000)
			if cut < start {
				return nil, fmt.Errorf("a single timed word exceeds the 15 second video limit")
			}
			cuts = append(cuts, sceneCut{startWord: start, endWord: cut, itemIndex: sourceIndex})
			start = cut + 1
		}
	}
	return cuts, nil
}

func buildSlideshowCuts(words []narration.Word, wordItems []int) []sceneCut {
	cuts := []sceneCut{}
	for start := 0; start < len(words); {
		startUS := int64(0)
		if start > 0 {
			startUS = secondsToUS(words[start].StartTime)
		}
		minimum, maximum, aim := slideshowLaterMinUS, int64(0), slideshowLaterMinUS
		if startUS < firstThirtySecondsUS {
			minimum, maximum, aim = slideshowEarlyMinUS, slideshowEarlyMaxUS, slideshowEarlyAimUS
		}
		cut := chooseSlideshowCut(words, wordItems, start, minimum, maximum, aim)
		cuts = append(cuts, sceneCut{startWord: start, endWord: cut, itemIndex: wordItems[start]})
		start = cut + 1
	}
	if len(cuts) > 1 {
		last := cuts[len(cuts)-1]
		previous := &cuts[len(cuts)-2]
		lastStartUS := secondsToUS(words[last.startWord].StartTime)
		lastDuration := secondsToUS(words[last.endWord].EndTime) - lastStartUS
		minimum := slideshowLaterMinUS
		if lastStartUS < firstThirtySecondsUS {
			minimum = slideshowEarlyMinUS
		}
		if lastDuration < minimum {
			previous.endWord = last.endWord
			cuts = cuts[:len(cuts)-1]
		}
	}
	return cuts
}

func chooseSlideshowCut(words []narration.Word, wordItems []int, start int, minimum, maximum, aim int64) int {
	if start >= len(words)-1 {
		return start
	}
	startUS := secondsToUS(words[start].StartTime)
	best, bestDistance := -1, int64(math.MaxInt64)
	firstAfterMinimum := -1
	for index := start; index < len(words); index++ {
		boundaryUS := secondsToUS(words[index].EndTime)
		if index+1 < len(words) {
			boundaryUS = secondsToUS(words[index+1].StartTime)
		}
		duration := boundaryUS - startUS
		semantic := isSemanticBoundary(words, wordItems, index)
		if duration >= minimum && semantic && firstAfterMinimum < 0 {
			firstAfterMinimum = index
		}
		if duration >= minimum && (maximum == 0 || duration <= maximum) && semantic {
			distance := duration - aim
			if distance < 0 {
				distance = -distance
			}
			if distance < bestDistance {
				best, bestDistance = index, distance
			}
		}
		if maximum > 0 && duration > maximum && best >= 0 {
			return best
		}
		if maximum == 0 && duration >= aim && firstAfterMinimum >= 0 {
			return firstAfterMinimum
		}
	}
	if best >= 0 {
		return best
	}
	if firstAfterMinimum >= 0 {
		return firstAfterMinimum
	}
	return len(words) - 1
}

func chooseCutAtMost(words []narration.Word, start, end int, maximumUS int64) int {
	startUS := secondsToUS(words[start].StartTime)
	lastSafe, lastSemantic := -1, -1
	for index := start; index <= end; index++ {
		boundaryUS := secondsToUS(words[index].EndTime)
		if index+1 <= end {
			boundaryUS = secondsToUS(words[index+1].StartTime)
		}
		duration := boundaryUS - startUS
		if duration > maximumUS {
			break
		}
		lastSafe = index
		if endsAtPunctuation(words[index].Text) {
			lastSemantic = index
		}
	}
	if lastSemantic >= start {
		return lastSemantic
	}
	return lastSafe
}

func isSemanticBoundary(words []narration.Word, wordItems []int, index int) bool {
	if index >= len(words)-1 {
		return true
	}
	return endsAtPunctuation(words[index].Text) || wordItems[index] != wordItems[index+1]
}

func endsAtPunctuation(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	last, _ := utf8LastRune(text)
	return strings.ContainsRune("。！？；，、.!?;,:：…", last)
}

func utf8LastRune(value string) (rune, int) {
	runes := []rune(value)
	if len(runes) == 0 {
		return 0, 0
	}
	return runes[len(runes)-1], len(runes)
}

func alignmentText(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func imageItemSHA256(item domain.ImageProjectItem) (string, error) {
	if item.ImagePath == nil || strings.TrimSpace(*item.ImagePath) == "" {
		return "", fmt.Errorf("image item %s has no ready image path", item.ID)
	}
	file, err := os.Open(*item.ImagePath)
	if err != nil {
		return "", fmt.Errorf("open image item %s: %w", item.ID, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash image item %s: %w", item.ID, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func uniqueSourceIDs(cut sceneCut, wordItems []int, sources []timedSource) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for wordIndex := cut.startWord; wordIndex <= cut.endWord; wordIndex++ {
		itemID := sources[wordItems[wordIndex]].item.ID
		if _, exists := seen[itemID]; exists {
			continue
		}
		seen[itemID] = struct{}{}
		result = append(result, itemID)
	}
	return result
}

func prependUnique(value string, values []string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append([]string{value}, values...)
}

func joinTimedWords(words []narration.Word) string {
	var builder strings.Builder
	for _, word := range words {
		builder.WriteString(word.Text)
	}
	return builder.String()
}

func joinSceneText(scenes []Scene) string {
	var builder strings.Builder
	for _, scene := range scenes {
		builder.WriteString(scene.Text)
	}
	return builder.String()
}

func secondsToUS(seconds float64) int64 {
	return int64(math.Round(seconds * 1_000_000))
}
