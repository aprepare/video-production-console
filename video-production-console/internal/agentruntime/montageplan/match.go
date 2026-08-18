package montageplan

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"video-production-console/internal/mediacatalog"
)

const (
	matchLevelDirect   = "direct"
	matchLevelMetaphor = "metaphor"
	matchLevelEmotion  = "emotion"
	matchLevelNeutral  = "neutral"

	thresholdDirect   = 0.72
	thresholdMetaphor = 0.62
	thresholdEmotion  = 0.54

	weightSemantic     = 0.40
	weightTopic        = 0.18
	weightEntity       = 0.12
	weightMood         = 0.10
	weightMotion       = 0.08
	weightComposition  = 0.07
	weightUnderuse     = 0.05
	penaltyNearby      = 0.35
	penaltyRepeat      = 0.25
	penaltyVisibleText = 0.20
	penaltyQuota       = 0.20

	// semanticNeighborLimit is how many cosine-nearest shots (outside the
	// tag pool) are merged into each intent. Tag recall alone only sees
	// exact English labels, so housing narration would miss cityscape.
	semanticNeighborLimit = 32
)

// ErrCatalogUnavailable is returned when a requested catalog cannot be opened.
var ErrCatalogUnavailable = errors.New("media_catalog_unavailable")

// CatalogReader is the planner-facing recall surface. Tests inject fakes;
// production wraps mediacatalog.Repository.
type CatalogReader interface {
	RecallByTags(ctx context.Context, tags []string, limit int) ([]matchShot, error)
	RecallByMoodSetting(ctx context.Context, mood, setting string, limit int) ([]matchShot, error)
	RecallReadyShots(ctx context.Context, limit int) ([]matchShot, error)
}

type matchShot struct {
	item        mediaItem
	tags        []string
	mood        string
	setting     string
	motion      string
	hasText     bool
	peopleCount int
	embedding   []float32
}

type scoreContext struct {
	usedShots     map[string]bool
	prevSource    string
	kindShare     float64
	kindTargetMid float64
}

type repositoryCatalog struct {
	repo *mediacatalog.Repository
}

func (c repositoryCatalog) RecallByTags(ctx context.Context, tags []string, limit int) ([]matchShot, error) {
	rows, err := c.repo.RecallByTags(ctx, tags, limit)
	if err != nil {
		return nil, err
	}
	return convertRecalled(rows), nil
}

func (c repositoryCatalog) RecallByMoodSetting(ctx context.Context, mood, setting string, limit int) ([]matchShot, error) {
	rows, err := c.repo.RecallByMoodSetting(ctx, mood, setting, limit)
	if err != nil {
		return nil, err
	}
	return convertRecalled(rows), nil
}

func (c repositoryCatalog) RecallReadyShots(ctx context.Context, limit int) ([]matchShot, error) {
	rows, err := c.repo.RecallReadyShots(ctx, limit)
	if err != nil {
		return nil, err
	}
	return convertRecalled(rows), nil
}

func convertRecalled(rows []mediacatalog.RecalledShot) []matchShot {
	out := make([]matchShot, 0, len(rows))
	for _, row := range rows {
		out = append(out, recalledToMatchShot(row))
	}
	return out
}

func recalledToMatchShot(row mediacatalog.RecalledShot) matchShot {
	kind := mediaKind(row.Source.Kind)
	switch kind {
	case mediaKindMovie, mediaKindBroll, mediaKindImage:
	default:
		kind = mediaKindBroll
	}
	item := mediaItem{
		ID:               row.Source.ID,
		Kind:             kind,
		Category:         row.Shot.Setting,
		RelativePath:     row.Source.RelativePath,
		DurationSeconds:  float64(row.Source.DurationMS) / 1000,
		SourceInSeconds:  float64(row.Shot.SourceInMS) / 1000,
		SourceOutSeconds: float64(row.Shot.SourceOutMS) / 1000,
		ShotID:           row.Shot.ID,
		Summary:          row.Shot.Summary,
		Mood:             row.Shot.Mood,
		Subtype:          string(row.Source.Subtype),
		Origin:           string(row.Source.Origin),
	}
	tags := make([]string, 0, len(row.Tags))
	for _, tag := range row.Tags {
		tags = append(tags, tag.Value)
		item.Tags = append(item.Tags, tag.Value)
	}
	return matchShot{
		item: item, tags: tags, mood: row.Shot.Mood, setting: row.Shot.Setting,
		motion: row.Shot.MotionLevel, hasText: row.Shot.HasText,
		peopleCount: row.Shot.PeopleCount, embedding: row.Embedding,
	}
}

func openCatalogReader(path string) (CatalogReader, func(), error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, func() {}, fmt.Errorf("%w: catalog path is empty", ErrCatalogUnavailable)
	}
	root := path
	if strings.EqualFold(filepath.Base(path), mediacatalog.CatalogFileName) {
		root = filepath.Dir(path)
	}
	repo, err := mediacatalog.Open(root)
	if err != nil {
		return nil, func() {}, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	return repositoryCatalog{repo: repo}, func() { _ = repo.Close() }, nil
}

func matchCandidates(ctx context.Context, intent NarrativeIntent, pool []matchShot, embedder Embedder, scoreCtx scoreContext) []rankedCandidate {
	intentEmbedding := embedIntent(ctx, embedder, intent)
	ranked := make([]rankedCandidate, 0, len(pool))
	for _, shot := range pool {
		level, hit := classifyMatchLevel(intent, shot)
		evidence, score := scoreShot(intent, shot, intentEmbedding, level, hit, scoreCtx)
		if !passesThreshold(level, score) {
			continue
		}
		ranked = append(ranked, rankedCandidate{Item: shot.item, Score: score, Match: evidence})
	}
	sortRanked(ranked)
	if len(ranked) > 50 {
		ranked = ranked[:50]
	}
	return ranked
}

func classifyMatchLevel(intent NarrativeIntent, shot matchShot) (string, string) {
	haystack := shotHaystack(shot)
	for _, entity := range intent.Entities {
		if containsFold(haystack, entity) {
			return matchLevelDirect, entity
		}
	}
	for _, topic := range intent.Topics {
		if containsFold(haystack, topic) {
			return matchLevelDirect, topic
		}
	}
	for _, metaphor := range intent.Metaphors {
		if containsFold(haystack, metaphor) {
			return matchLevelMetaphor, metaphor
		}
	}
	for _, concept := range intent.VisualConcepts {
		if !containsFold(haystack, concept) {
			continue
		}
		if looksMetaphor(concept) {
			return matchLevelMetaphor, concept
		}
		return matchLevelDirect, concept
	}
	if moodMatches(intent.Mood, shot.mood) {
		return matchLevelEmotion, intent.Mood
	}
	return matchLevelNeutral, ""
}

func catalogMoodsFor(mood string) []string {
	switch fold(mood) {
	case "warning":
		return []string{"tense", "serious", "intense"}
	case "anxious":
		return []string{"stressed", "tense", "serious"}
	case "resolute":
		return []string{"confident", "professional", "focused"}
	case "neutral", "":
		return []string{"neutral", "focused", "professional", "analytical"}
	default:
		return []string{mood}
	}
}

func moodMatches(intentMood, shotMood string) bool {
	if strings.TrimSpace(intentMood) == "" || strings.TrimSpace(shotMood) == "" {
		return false
	}
	if fold(intentMood) == fold(shotMood) {
		return true
	}
	hay := fold(shotMood)
	for _, candidate := range catalogMoodsFor(intentMood) {
		if strings.Contains(hay, fold(candidate)) {
			return true
		}
	}
	return false
}

func looksMetaphor(value string) bool {
	for _, entry := range metaphorLexicon {
		if fold(value) == fold(entry.metaphor) || fold(value) == fold(entry.concept) {
			return true
		}
	}
	return strings.Contains(value, "水位") || strings.Contains(value, "关门") || strings.Contains(value, "绳索")
}

func scoreShot(intent NarrativeIntent, shot matchShot, intentEmbedding []float32, level, hit string, scoreCtx scoreContext) (MatchEvidence, float64) {
	semantic := cosine(intentEmbedding, shot.embedding)
	topic := overlapScore(append(append(append([]string{}, intent.Topics...), intent.VisualConcepts...), intent.Metaphors...), append(append([]string{}, shot.tags...), shot.setting, shot.item.Summary))
	entity := overlapScore(intent.Entities, shot.tags)
	mood := 0.0
	if moodMatches(intent.Mood, shot.mood) {
		mood = 1
	}
	motion := motionFit(shot.motion, intent.Importance)
	composition := 0.65
	if shot.hasText {
		composition = 0.35
	} else if shot.peopleCount > 6 {
		composition = 0.45
	}
	underuse := 1.0
	if scoreCtx.usedShots[shot.item.shotKey()] {
		underuse = 0
	}
	base := weightSemantic*semantic + weightTopic*topic + weightEntity*entity +
		weightMood*mood + weightMotion*motion + weightComposition*composition + weightUnderuse*underuse

	nearby := 0.0
	if scoreCtx.prevSource != "" && scoreCtx.prevSource == shot.item.sourceKey() {
		nearby = 1
	}
	repeated := 0.0
	if scoreCtx.usedShots[shot.item.shotKey()] {
		repeated = 1
	}
	textRisk := 0.0
	if shot.hasText {
		textRisk = 1
	}
	quota := 0.0
	if scoreCtx.kindTargetMid > 0 && scoreCtx.kindShare > scoreCtx.kindTargetMid {
		quota = math.Min(1, (scoreCtx.kindShare-scoreCtx.kindTargetMid)/scoreCtx.kindTargetMid)
	}
	penalty := penaltyNearby*nearby + penaltyRepeat*repeated + penaltyVisibleText*textRisk + penaltyQuota*quota
	score := clamp01(base - penalty)
	reason := formatMatchReason(level, hit, intent)
	return MatchEvidence{Level: level, Score: score, IntentID: intent.SegmentID, Reason: reason}, score
}

func formatMatchReason(level, hit string, intent NarrativeIntent) string {
	switch level {
	case matchLevelDirect:
		if hit == "" {
			hit = firstNonEmpty(intent.Entities, intent.Topics)
		}
		return fmt.Sprintf("direct: 命中标签「%s」对应实体「%s」", hit, hit)
	case matchLevelMetaphor:
		if hit == "" {
			hit = firstNonEmpty(intent.Metaphors, intent.VisualConcepts)
		}
		concept := firstNonEmpty(intent.Topics, intent.Entities)
		if concept == "" {
			concept = hit
		}
		return fmt.Sprintf("metaphor: 隐喻「%s」对应概念「%s」", hit, concept)
	case matchLevelEmotion:
		mood := intent.Mood
		if mood == "" {
			mood = hit
		}
		return fmt.Sprintf("emotion: 情绪「%s」对应画面「%s」", mood, firstNonEmpty(intent.VisualConcepts, []string{mood}))
	default:
		return "neutral: 中性过渡镜头补足节奏与配额"
	}
}

func firstNonEmpty(parts ...[]string) string {
	for _, part := range parts {
		for _, value := range part {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return "画面"
}

func passesThreshold(level string, score float64) bool {
	switch level {
	case matchLevelDirect, matchLevelMetaphor, matchLevelEmotion:
		// Lexical tag/mood hits must survive when embeddings are offline.
		// The 0.72/0.62/0.54 floors assume a semantic term; tag overlap
		// alone tops out around 0.60 and would otherwise discard every hit.
		return true
	default:
		return true
	}
}

// recallForIntent is the lexical four-level pool: entity/topic tags,
// metaphor/visual-concept tags, mood, then a small ready-shot fill.
// It is capped at 80. Full-library neighbors are appended later by
// rankLibrary when an embedder is configured — do not raise this cap
// to scan the catalog; that path is RecallReadyShots(2000).
func recallForIntent(ctx context.Context, catalog CatalogReader, intent NarrativeIntent) ([]matchShot, error) {
	if catalog == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]matchShot, 0, 50)
	add := func(rows []matchShot) {
		for _, row := range rows {
			key := row.item.shotKey()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, row)
		}
	}
	directTerms := append(append([]string{}, intent.Entities...), intent.Topics...)
	rows, err := catalog.RecallByTags(ctx, directTerms, 50)
	if err != nil {
		return nil, wrapCatalogError(err)
	}
	add(rows)
	metaTerms := append(append([]string{}, intent.Metaphors...), intent.VisualConcepts...)
	rows, err = catalog.RecallByTags(ctx, metaTerms, 50)
	if err != nil {
		return nil, wrapCatalogError(err)
	}
	add(rows)
	for _, mood := range catalogMoodsFor(intent.Mood) {
		rows, err = catalog.RecallByMoodSetting(ctx, mood, "", 50)
		if err != nil {
			return nil, wrapCatalogError(err)
		}
		add(rows)
	}
	rows, err = catalog.RecallReadyShots(ctx, 50)
	if err != nil {
		return nil, wrapCatalogError(err)
	}
	add(rows)
	if len(out) > 80 {
		out = out[:80]
	}
	return out, nil
}

// appendSemanticNeighbors ranks universe by cosine(query, shot.embedding)
// and appends unseen neighbors. Cosine never promotes match level to
// direct — that would make fixture vectors look like lexical hits.
func appendSemanticNeighbors(pool, universe []matchShot, query []float32, limit int) []matchShot {
	if len(query) == 0 || len(universe) == 0 || limit <= 0 {
		return pool
	}
	type scored struct {
		shot matchShot
		sim  float64
	}
	ranked := make([]scored, 0, len(universe))
	for _, shot := range universe {
		sim := cosine(query, shot.embedding)
		if sim <= 0 {
			continue
		}
		ranked = append(ranked, scored{shot: shot, sim: sim})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].sim != ranked[j].sim {
			return ranked[i].sim > ranked[j].sim
		}
		return ranked[i].shot.item.ShotID < ranked[j].shot.item.ShotID
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	seen := map[string]bool{}
	for _, shot := range pool {
		seen[shot.item.shotKey()] = true
	}
	for _, row := range ranked {
		key := row.shot.item.shotKey()
		if seen[key] {
			continue
		}
		seen[key] = true
		pool = append(pool, row.shot)
	}
	return pool
}

// rankLibrary scores every intent against a tag pool plus optional
// embedding neighbors. planner_notes record either
// "embedding_pool: scanned N catalog shots" or
// "embedding_disabled: embedder not configured".
// Changing embedding URL/model/key in settings requires a console restart
// before the child process sees them.
func rankLibrary(ctx context.Context, intents []NarrativeIntent, catalog CatalogReader, embedder Embedder) ([]rankedCandidate, []string, error) {
	best := map[string]rankedCandidate{}
	warnings := []string{}
	if len(intents) == 0 {
		warnings = append(warnings, "intent_fallback_empty: no narrative intents; using neutral catalog pool")
	}
	var semanticPool []matchShot
	if embedder == nil {
		warnings = append(warnings, "embedding_disabled: embedder not configured")
	} else if catalog != nil {
		pool, err := catalog.RecallReadyShots(ctx, 2000)
		if err != nil {
			return nil, nil, err
		}
		semanticPool = pool
		warnings = append(warnings, fmt.Sprintf("embedding_pool: scanned %d catalog shots", len(semanticPool)))
	}
	embedMemo := map[string][]float32{}
	scoreCtx := scoreContext{usedShots: map[string]bool{}}
	emptyQueries := 0
	for _, intent := range intents {
		pool, err := recallForIntent(ctx, catalog, intent)
		if err != nil {
			return nil, nil, err
		}
		if len(semanticPool) > 0 {
			query := cachedEmbedIntent(ctx, embedder, embedMemo, intent)
			if len(query) == 0 {
				emptyQueries++
			}
			pool = appendSemanticNeighbors(pool, semanticPool, query, semanticNeighborLimit)
		}
		for _, candidate := range matchCandidates(ctx, intent, pool, embedder, scoreCtx) {
			key := candidate.Item.shotKey() + "\x00" + candidate.Match.IntentID
			if existing, ok := best[key]; !ok || candidate.Score > existing.Score ||
				(candidate.Score == existing.Score && candidate.Item.ShotID < existing.Item.ShotID) {
				best[key] = candidate
			}
		}
	}
	if emptyQueries > 0 {
		warnings = append(warnings, fmt.Sprintf("embedding_query_empty: %d intents", emptyQueries))
	}
	if len(best) == 0 && catalog != nil {
		pool, err := catalog.RecallReadyShots(ctx, 50)
		if err != nil {
			return nil, nil, wrapCatalogError(err)
		}
		neutral := NarrativeIntent{SegmentID: "seg-001", Mood: "neutral", Importance: 0.4}
		for _, candidate := range matchCandidates(ctx, neutral, pool, embedder, scoreCtx) {
			best[candidate.Item.shotKey()+"\x00"+candidate.Match.IntentID] = candidate
		}
		warnings = append(warnings, "match_candidates_insufficient: falling back to neutral catalog shots")
	}
	out := make([]rankedCandidate, 0, len(best))
	for _, candidate := range best {
		out = append(out, candidate)
	}
	sortRanked(out)
	return out, warnings, nil
}

func catalogFillNeed(duration float64) int {
	if duration <= 0 {
		return 0
	}
	return int(math.Ceil(duration/5.0)) + 8
}

// padCatalogCandidates appends unused ready catalog shots when narration
// recall is too small to cover the timeline without repeating a ShotID.
func padCatalogCandidates(ctx context.Context, catalog CatalogReader, ranked []rankedCandidate, duration float64) ([]rankedCandidate, []string, error) {
	if catalog == nil {
		return ranked, nil, nil
	}
	need := catalogFillNeed(duration)
	if need == 0 {
		return ranked, nil, nil
	}
	seen := map[string]bool{}
	usable := 0
	for _, candidate := range ranked {
		key := candidate.Item.shotKey()
		if seen[key] {
			continue
		}
		seen[key] = true
		if candidate.Item.Kind == mediaKindImage || v2AvailableSeconds(candidate.Item) >= 5 {
			usable++
		}
	}
	if usable >= need {
		return ranked, nil, nil
	}
	limit := need * 2
	if limit < 100 {
		limit = 100
	}
	pool, err := catalog.RecallReadyShots(ctx, limit)
	if err != nil {
		return nil, nil, wrapCatalogError(err)
	}
	added := 0
	for _, shot := range pool {
		key := shot.item.shotKey()
		if seen[key] {
			continue
		}
		if shot.item.Kind != mediaKindImage && v2AvailableSeconds(shot.item) < 5 {
			continue
		}
		seen[key] = true
		ranked = append(ranked, rankedCandidate{
			Item:  shot.item,
			Score: 0,
			Match: MatchEvidence{Level: matchLevelNeutral, IntentID: "catalog-fill", Reason: "catalog_fill: unused ready shot"},
		})
		usable++
		added++
		if usable >= need {
			break
		}
	}
	if added == 0 {
		return ranked, nil, nil
	}
	return ranked, []string{fmt.Sprintf("catalog_fill: added %d unused ready shots to cover %.1fs", added, duration)}, nil
}

func sortRanked(ranked []rankedCandidate) {
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		aShot := ranked[i].Item.ShotID
		bShot := ranked[j].Item.ShotID
		if aShot != bShot {
			return aShot < bShot
		}
		aHash := stableShotHash(ranked[i].Item)
		bHash := stableShotHash(ranked[j].Item)
		if cmp := strings.Compare(aHash, bHash); cmp != 0 {
			return cmp < 0
		}
		return ranked[i].Item.ID < ranked[j].Item.ID
	})
}

func stableShotHash(item mediaItem) string {
	sum := sha256.Sum256([]byte(item.shotKey() + "\x00" + item.sourceKey()))
	return fmt.Sprintf("%x", sum[:8])
}

func cachedEmbedIntent(ctx context.Context, embedder Embedder, memo map[string][]float32, intent NarrativeIntent) []float32 {
	key := strings.TrimSpace(intent.VisualQuery)
	if key == "" {
		key = strings.TrimSpace(intent.Text + " " + strings.Join(intent.VisualConcepts, " "))
	}
	if key == "" {
		return nil
	}
	if vector, ok := memo[key]; ok {
		return vector
	}
	vector := embedIntent(ctx, embedder, intent)
	memo[key] = vector
	return vector
}

// embedIntent embeds VisualQuery, or spoken text + visual concepts.
// Errors return nil so lexical scoring still works offline.
func embedIntent(ctx context.Context, embedder Embedder, intent NarrativeIntent) []float32 {
	if embedder == nil {
		return nil
	}
	query := strings.TrimSpace(intent.VisualQuery)
	if query == "" {
		query = strings.TrimSpace(intent.Text + " " + strings.Join(intent.VisualConcepts, " "))
	}
	vector, err := embedder.Embed(ctx, query)
	if err != nil {
		return nil
	}
	return vector
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return clamp01(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

func overlapScore(left, right []string) float64 {
	if len(left) == 0 {
		return 0
	}
	rightSet := map[string]bool{}
	for _, value := range right {
		if folded := fold(value); folded != "" {
			rightSet[folded] = true
		}
	}
	hits := 0
	for _, value := range left {
		folded := fold(value)
		if folded == "" {
			continue
		}
		if rightSet[folded] {
			hits++
			continue
		}
		for right := range rightSet {
			if strings.Contains(right, folded) || strings.Contains(folded, right) {
				hits++
				break
			}
		}
	}
	return float64(hits) / float64(len(left))
}

func motionFit(motion string, importance float64) float64 {
	switch strings.ToLower(strings.TrimSpace(motion)) {
	case "high":
		return clamp01(0.55 + 0.4*importance)
	case "medium":
		return 0.7
	case "low":
		return 0.6
	case "static":
		return 0.5
	default:
		return 0.55
	}
}

func shotHaystack(shot matchShot) string {
	parts := []string{shot.item.Summary, shot.setting, shot.mood}
	parts = append(parts, shot.tags...)
	return strings.Join(parts, " ")
}

func containsFold(haystack, needle string) bool {
	needle = fold(needle)
	if needle == "" {
		return false
	}
	return strings.Contains(fold(haystack), needle)
}

func fold(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func wrapCatalogError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCatalogUnavailable) || errors.Is(err, mediacatalog.ErrCatalogUnavailable) {
		return fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	return err
}

func resolveCatalog(opts Options) (CatalogReader, func(), error) {
	if opts.Catalog != nil {
		return opts.Catalog, func() {}, nil
	}
	path := strings.TrimSpace(opts.CatalogPath)
	if path == "" {
		return nil, func() {}, nil
	}
	return openCatalogReader(path)
}
