package montageplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// quotaRange bounds one kind's share of the timeline duration.
type quotaRange struct{ Min, Max float64 }

// mixPolicy is the deterministic selection contract from plan §5.4.
type mixPolicy struct {
	Targets                 map[mediaKind]quotaRange
	MaxSourceUses           int
	MinSegmentsBetweenReuse int
}

// quotaWarning reports a typed deviation from the mix policy. Warnings are
// returned to the caller (and may be logged); they never enter the v1 plan JSON.
type quotaWarning struct {
	Kind, Code     string
	Wanted, Actual float64
}

// rankedCandidate carries one selectable media item and its recall score.
// Score comes from rankLibrary (tag + optional embedding). Equal scores
// fall back to category diversity, underuse and the seeded stable hash.
type rankedCandidate struct {
	Item  mediaItem
	Score float64
	Match MatchEvidence
}

// plannedMedia is one timeline slot with the media chosen for it.
type plannedMedia struct {
	Item   mediaItem     `json:"item"`
	StartS float64       `json:"start_s"`
	EndS   float64       `json:"end_s"`
	Match  MatchEvidence `json:"-"`
}

// kindOrder fixes every per-kind iteration; map iteration is never used to
// decide selection order.
var kindOrder = []mediaKind{mediaKindBroll, mediaKindMovie, mediaKindImage}

// substituteKinds is the movie→broll→image degradation matrix from §5.4:
// each kind lists, in order, what may stand in when it runs out.
var substituteKinds = map[mediaKind][]mediaKind{
	mediaKindMovie: {mediaKindBroll, mediaKindImage},
	mediaKindBroll: {mediaKindMovie, mediaKindImage},
	mediaKindImage: {mediaKindBroll, mediaKindMovie},
}

// movieMixPolicy returns the default movie_mix preset from §5.4.
func movieMixPolicy() mixPolicy {
	return mixPolicy{
		Targets: map[mediaKind]quotaRange{
			mediaKindBroll: {Min: 0.35, Max: 0.45},
			mediaKindMovie: {Min: 0.25, Max: 0.35},
			mediaKindImage: {Min: 0.20, Max: 0.30},
		},
		MaxSourceUses:           2,
		MinSegmentsBetweenReuse: 5,
	}
}

// movieCatalogPolicy is method three: one local movie library fills the
// timeline. Source reuse is high because a single film is the expected input.
func movieCatalogPolicy() mixPolicy {
	return mixPolicy{
		Targets: map[mediaKind]quotaRange{
			mediaKindMovie: {Min: 0.70, Max: 1.00},
			mediaKindBroll: {Min: 0, Max: 0.20},
			mediaKindImage: {Min: 0, Max: 0.10},
		},
		MaxSourceUses:           80,
		MinSegmentsBetweenReuse: 1,
	}
}

// imageVideoPolicy is the image_video preset: stills first, no movie footage.
func imageVideoPolicy() mixPolicy {
	return mixPolicy{
		Targets: map[mediaKind]quotaRange{
			mediaKindImage: {Min: 0.85, Max: 1.00},
			mediaKindBroll: {Min: 0, Max: 0.15},
			mediaKindMovie: {Min: 0, Max: 0},
		},
		MaxSourceUses:           2,
		MinSegmentsBetweenReuse: 5,
	}
}

// timelineSlot mirrors the v1 shot rules in buildTimeline: 7s slots for the
// first 30 seconds, 8s afterwards, and a final slot that absorbs any tail
// shorter than 1.5s. Keeping both loops identical means the selector plans
// exactly one item per timeline shot.
type timelineSlot struct{ start, seconds float64 }

func timelineSlots(duration float64) []timelineSlot {
	slots := make([]timelineSlot, 0, 40)
	cursor := 0.0
	for cursor < duration-0.01 {
		remaining := duration - cursor
		if remaining < 1.5 && len(slots) > 0 {
			slots[len(slots)-1].seconds += remaining
			break
		}
		length := 8.0
		if cursor < 30 {
			length = 7.0
		}
		if remaining < length {
			length = remaining
		}
		slots = append(slots, timelineSlot{start: cursor, seconds: length})
		cursor += length
	}
	return slots
}

// quotaCandidate is the selector's internal bookkeeping for one candidate.
type quotaCandidate struct {
	item  mediaItem
	score float64
	match MatchEvidence
	rank  [sha256.Size]byte
	order int
}

// Constraint relaxation ladder. Levels are tried in order so a sparse library
// still fills the whole narration instead of failing; shot repetition is never
// allowed before the last-resort level, and adjacency of the same source is
// only allowed there.
const (
	levelStrict       = iota // max uses + reuse spacing
	levelRelaxUses           // spacing only
	levelRelaxSpacing        // never the same source back to back
	levelLastResort          // anything, least-used first
)

// preferCandidate reports whether a should beat b for the current slot.
// Last-resort prefers the least-used source and avoids the previous source
// so a spent Nature_Landscape pool rotates instead of replaying one file.
func preferCandidate(a, b *quotaCandidate, level int, prevCategory, prevSource string, sourceUses map[string]int, overlap map[string]float64, knownIntents map[string]bool) bool {
	if level < levelLastResort && len(knownIntents) > 0 {
		aHit := overlap[a.match.IntentID]
		bHit := overlap[b.match.IntentID]
		aLocal := aHit > 0
		bLocal := bHit > 0
		if aLocal != bLocal {
			return aLocal
		}
		if aLocal && aHit != bHit {
			return aHit > bHit
		}
		aReserved := knownIntents[a.match.IntentID] && !aLocal
		bReserved := knownIntents[b.match.IntentID] && !bLocal
		if aReserved != bReserved {
			return !aReserved
		}
	}
	if level >= levelLastResort {
		aUses := sourceUses[a.item.sourceKey()]
		bUses := sourceUses[b.item.sourceKey()]
		if aUses != bUses {
			return aUses < bUses
		}
		aDiff := a.item.sourceKey() != prevSource
		bDiff := b.item.sourceKey() != prevSource
		if aDiff != bDiff {
			return aDiff
		}
	}
	if a.score != b.score {
		return a.score > b.score
	}
	aCat := normalizeCategory(a.item.Category) != prevCategory
	bCat := normalizeCategory(b.item.Category) != prevCategory
	if aCat != bCat {
		return aCat
	}
	if cmp := bytes.Compare(a.rank[:], b.rank[:]); cmp != 0 {
		return cmp < 0
	}
	return a.order < b.order
}

// selectTimeline deterministically assigns one candidate to every timeline
// slot: generate the slots, pick the kind with the largest duration deficit,
// pick the best candidate inside that kind, apply shot/source/adjacency
// constraints, degrade via the substitution matrix, and let the final slot
// absorb the tail. Same inputs and seed always produce the same output.
func selectTimeline(candidates []rankedCandidate, duration float64, seed string, policy mixPolicy) ([]plannedMedia, []quotaWarning, error) {
	if duration <= 0 {
		return nil, nil, fmt.Errorf("timeline duration must be positive")
	}
	byKind := map[mediaKind][]*quotaCandidate{}
	total := 0
	for i, candidate := range candidates {
		if candidate.Item.ID == "" {
			continue
		}
		entry := &quotaCandidate{
			item:  candidate.Item,
			score: candidate.Score,
			match: candidate.Match,
			rank:  mediaRank(seed, candidate.Item),
			order: i,
		}
		byKind[entry.item.Kind] = append(byKind[entry.item.Kind], entry)
		total++
	}
	if total == 0 {
		return nil, nil, fmt.Errorf("no usable media candidates")
	}

	slots := timelineSlots(duration)
	totalSeconds := 0.0
	for _, slot := range slots {
		totalSeconds += slot.seconds
	}

	assigned := map[mediaKind]float64{}
	shotUsed := map[string]bool{}
	sourceUses := map[string]int{}
	sourceLastSlot := map[string]int{}
	prevCategory := ""
	prevSource := ""

	eligible := func(c *quotaCandidate, slotIdx, level int) bool {
		// Every index row is one shot and never repeats; only the last-resort
		// level may replay a shotless row so a tiny library can still cover
		// the whole narration. An explicit shot_id never repeats at any level.
		if shotUsed[c.item.shotKey()] {
			if c.item.ShotID != "" || level < levelLastResort {
				return false
			}
		}
		src := c.item.sourceKey()
		switch level {
		case levelStrict:
			if sourceUses[src] >= policy.MaxSourceUses {
				return false
			}
			if last, ok := sourceLastSlot[src]; ok && slotIdx-last <= policy.MinSegmentsBetweenReuse {
				return false
			}
		case levelRelaxUses:
			if last, ok := sourceLastSlot[src]; ok && slotIdx-last <= policy.MinSegmentsBetweenReuse {
				return false
			}
		case levelRelaxSpacing:
			if src == prevSource {
				return false
			}
		}
		return true
	}

	pickFromKind := func(kind mediaKind, slotIdx, level int) *quotaCandidate {
		var best *quotaCandidate
		for _, c := range byKind[kind] {
			if !eligible(c, slotIdx, level) {
				continue
			}
			if best == nil || preferCandidate(c, best, level, prevCategory, prevSource, sourceUses, nil, nil) {
				best = c
			}
		}
		return best
	}

	// deficitKind returns the kind whose assigned duration is farthest below
	// its target-midpoint budget; ties resolve in fixed kindOrder. Kinds
	// without a target or with a zero Max (movie under image_video) are never
	// requested directly; they may still stand in via the substitution matrix.
	deficitKind := func() mediaKind {
		bestKind := mediaKind("")
		bestDeficit := 0.0
		for _, kind := range kindOrder {
			target, ok := policy.Targets[kind]
			if !ok || target.Max <= 0 {
				continue
			}
			mid := (target.Min + target.Max) / 2
			deficit := mid*totalSeconds - assigned[kind]
			if bestKind == "" || deficit > bestDeficit+1e-9 {
				bestKind, bestDeficit = kind, deficit
			}
		}
		if bestKind == "" {
			bestKind = kindOrder[0]
		}
		return bestKind
	}

	selection := make([]plannedMedia, 0, len(slots))
	for slotIdx, slot := range slots {
		wanted := deficitKind()
		chain := append([]mediaKind{wanted}, substituteKinds[wanted]...)
		var chosen *quotaCandidate
		for level := levelStrict; level <= levelLastResort && chosen == nil; level++ {
			for _, kind := range chain {
				if chosen = pickFromKind(kind, slotIdx, level); chosen != nil {
					break
				}
			}
		}
		if chosen == nil {
			return nil, nil, fmt.Errorf("no candidate can fill slot %d without repeating a shot", slotIdx)
		}
		src := chosen.item.sourceKey()
		shotUsed[chosen.item.shotKey()] = true
		sourceUses[src]++
		sourceLastSlot[src] = slotIdx
		prevCategory = normalizeCategory(chosen.item.Category)
		prevSource = src
		assigned[chosen.item.Kind] += slot.seconds
		selection = append(selection, plannedMedia{Item: chosen.item, StartS: slot.start, EndS: slot.start + slot.seconds, Match: chosen.match})
	}

	warnings := make([]quotaWarning, 0, 2)
	for _, kind := range kindOrder {
		target, ok := policy.Targets[kind]
		if !ok {
			continue
		}
		share := assigned[kind] / totalSeconds
		if share < target.Min-1e-9 {
			warnings = append(warnings, quotaWarning{
				Kind: string(kind), Code: "quota_below_min", Wanted: target.Min, Actual: share,
			})
		} else if share > target.Max+1e-9 {
			warnings = append(warnings, quotaWarning{
				Kind: string(kind), Code: "quota_above_max", Wanted: target.Max, Actual: share,
			})
		}
	}
	return selection, warnings, nil
}

// --- v2 selection ------------------------------------------------------
//
// selectTimelineV2 keeps selectTimeline's constraint ladder and stable
// ordering but paces slots per plan §5.4: 4-7s during the first 30 seconds,
// then movie 3-6s, broll 5-9s and image 4-7s, with the final slot absorbing
// the tail without exceeding the chosen clip's source duration. v1's
// selectTimeline/timelineSlots stay untouched.

// v2TailAbsorbSeconds is the largest leftover the final slot swallows instead
// of leaving a fragment shorter than any allowed slot.
const v2TailAbsorbSeconds = 3.0

// v2PlaybackSpeed is the Jianying speed for movie/B-roll. The executor
// derives speed as (source_out-source_in)/slot, so each slot consumes
// 1.5x as much source as it occupies on the timeline.
const v2PlaybackSpeed = 1.5

func v2SourceNeed(timelineSeconds float64) float64 {
	return timelineSeconds * v2PlaybackSpeed
}

func v2MaxTimeline(avail float64) float64 {
	if math.IsInf(avail, 1) {
		return math.Inf(1)
	}
	return avail / v2PlaybackSpeed
}

type v2SlotRangeSpec struct{ lo, hi float64 }

// v2SlotRange is the per-kind slot length contract from plan §5.4.
func v2SlotRange(kind mediaKind, startS float64) v2SlotRangeSpec {
	if startS < 30 {
		return v2SlotRangeSpec{lo: 4, hi: 7}
	}
	switch kind {
	case mediaKindMovie:
		return v2SlotRangeSpec{lo: 3, hi: 6}
	case mediaKindBroll:
		return v2SlotRangeSpec{lo: 5, hi: 9}
	default:
		return v2SlotRangeSpec{lo: 4, hi: 7}
	}
}

// v2AvailableSeconds is the physical source budget of a candidate before
// applying v2PlaybackSpeed; stills have no intrinsic duration and can fill
// any slot.
func v2AvailableSeconds(item mediaItem) float64 {
	if item.Kind == mediaKindImage {
		return math.Inf(1)
	}
	return item.availableSeconds()
}

// v2SlotLength derives a deterministic slot length inside the range from the
// candidate's seeded rank hash, rounded to 0.1s.
func v2SlotLength(rank [sha256.Size]byte, spec v2SlotRangeSpec) float64 {
	frac := float64(binary.BigEndian.Uint16(rank[:2])) / 65535.0
	return math.Round((spec.lo+frac*(spec.hi-spec.lo))*10) / 10
}

// selectTimelineV2 deterministically fills the narration with kind-dependent
// slot lengths. Same inputs and seed always produce the same output.
func knownIntentIDs(intents []NarrativeIntent) map[string]bool {
	out := map[string]bool{}
	for _, intent := range intents {
		id := strings.TrimSpace(intent.SegmentID)
		if id != "" {
			out[id] = true
		}
	}
	return out
}

func intentImportanceAt(intents []NarrativeIntent, at float64) map[string]float64 {
	out := map[string]float64{}
	for _, intent := range intents {
		id := strings.TrimSpace(intent.SegmentID)
		if id == "" {
			continue
		}
		start := float64(intent.StartMS) / 1000
		end := float64(intent.EndMS) / 1000
		if at >= start && at < end {
			out[id] = intent.Importance
			if out[id] <= 0 {
				out[id] = 0.4
			}
		}
	}
	return out
}

func selectTimelineV2(candidates []rankedCandidate, duration float64, seed string, policy mixPolicy, intents []NarrativeIntent) ([]plannedMedia, []quotaWarning, error) {
	if duration <= 0 {
		return nil, nil, fmt.Errorf("timeline duration must be positive")
	}
	byKind := map[mediaKind][]*quotaCandidate{}
	total := 0
	for i, candidate := range candidates {
		if candidate.Item.ID == "" {
			continue
		}
		entry := &quotaCandidate{
			item:  candidate.Item,
			score: candidate.Score,
			match: candidate.Match,
			rank:  mediaRank(seed, candidate.Item),
			order: i,
		}
		byKind[entry.item.Kind] = append(byKind[entry.item.Kind], entry)
		total++
	}
	if total == 0 {
		return nil, nil, fmt.Errorf("no usable media candidates")
	}
	knownIntents := knownIntentIDs(intents)

	assigned := map[mediaKind]float64{}
	shotUsed := map[string]bool{}
	sourceUses := map[string]int{}
	sourceLastSlot := map[string]int{}
	prevCategory := ""
	prevSource := ""

	eligible := func(c *quotaCandidate, slotIdx, level int, minAvail float64) bool {
		// The physical source budget is never relaxed: a v2 shot plays at
		// 1.5x, so a slot can only consume what the clip really has after speed.
		if v2AvailableSeconds(c.item) < v2SourceNeed(minAvail) {
			return false
		}
		if shotUsed[c.item.shotKey()] {
			if c.item.ShotID != "" || level < levelLastResort {
				return false
			}
		}
		src := c.item.sourceKey()
		switch level {
		case levelStrict:
			if sourceUses[src] >= policy.MaxSourceUses {
				return false
			}
			if last, ok := sourceLastSlot[src]; ok && slotIdx-last <= policy.MinSegmentsBetweenReuse {
				return false
			}
		case levelRelaxUses:
			if last, ok := sourceLastSlot[src]; ok && slotIdx-last <= policy.MinSegmentsBetweenReuse {
				return false
			}
		case levelRelaxSpacing:
			if src == prevSource {
				return false
			}
		}
		return true
	}

	pickFromKind := func(kind mediaKind, slotIdx, level int, minAvail float64, overlap map[string]float64) *quotaCandidate {
		var best *quotaCandidate
		for _, c := range byKind[kind] {
			if !eligible(c, slotIdx, level, minAvail) {
				continue
			}
			if best == nil || preferCandidate(c, best, level, prevCategory, prevSource, sourceUses, overlap, knownIntents) {
				best = c
			}
		}
		return best
	}

	deficitKind := func() mediaKind {
		bestKind := mediaKind("")
		bestDeficit := 0.0
		for _, kind := range kindOrder {
			target, ok := policy.Targets[kind]
			if !ok || target.Max <= 0 {
				continue
			}
			mid := (target.Min + target.Max) / 2
			deficit := mid*duration - assigned[kind]
			if bestKind == "" || deficit > bestDeficit+1e-9 {
				bestKind, bestDeficit = kind, deficit
			}
		}
		if bestKind == "" {
			bestKind = kindOrder[0]
		}
		return bestKind
	}

	selection := make([]plannedMedia, 0, 48)
	cursor := 0.0
	for slotIdx := 0; cursor < duration-0.001; slotIdx++ {
		remaining := duration - cursor
		wanted := deficitKind()
		chain := append([]mediaKind{wanted}, substituteKinds[wanted]...)
		overlap := intentImportanceAt(intents, cursor)
		var chosen *quotaCandidate
		for level := levelStrict; level <= levelLastResort && chosen == nil; level++ {
			for _, kind := range chain {
				spec := v2SlotRange(kind, cursor)
				need := math.Min(spec.lo, remaining)
				if chosen = pickFromKind(kind, slotIdx, level, need, overlap); chosen != nil {
					break
				}
			}
		}
		if chosen == nil {
			return nil, nil, fmt.Errorf("no candidate can fill v2 slot %d without repeating a shot", slotIdx)
		}
		spec := v2SlotRange(chosen.item.Kind, cursor)
		avail := v2AvailableSeconds(chosen.item)
		maxTimeline := v2MaxTimeline(avail)
		length := v2SlotLength(chosen.rank, spec)
		if length > maxTimeline {
			length = maxTimeline
		}
		if length > remaining {
			length = remaining
		}
		if remaining-length < v2TailAbsorbSeconds {
			// Absorb the tail into this slot when the source allows it
			// at 1.5x; otherwise spend the whole source and let the next
			// slot finish.
			if remaining <= maxTimeline {
				length = remaining
			} else {
				length = maxTimeline
			}
		}
		length = roundSFXStart(length)
		if length <= 0 {
			return nil, nil, fmt.Errorf("v2 slot %d cannot make progress at %.3fs", slotIdx, cursor)
		}
		end := roundSFXStart(cursor + length)
		if math.Abs(duration-end) < 0.001 {
			end = duration
		}
		src := chosen.item.sourceKey()
		shotUsed[chosen.item.shotKey()] = true
		sourceUses[src]++
		sourceLastSlot[src] = slotIdx
		prevCategory = normalizeCategory(chosen.item.Category)
		prevSource = src
		assigned[chosen.item.Kind] += end - cursor
		selection = append(selection, plannedMedia{Item: chosen.item, StartS: cursor, EndS: end, Match: chosen.match})
		cursor = end
	}

	warnings := make([]quotaWarning, 0, 2)
	for _, kind := range kindOrder {
		target, ok := policy.Targets[kind]
		if !ok {
			continue
		}
		share := assigned[kind] / duration
		if share < target.Min-1e-9 {
			warnings = append(warnings, quotaWarning{
				Kind: string(kind), Code: "quota_below_min", Wanted: target.Min, Actual: share,
			})
		} else if share > target.Max+1e-9 {
			warnings = append(warnings, quotaWarning{
				Kind: string(kind), Code: "quota_above_max", Wanted: target.Max, Actual: share,
			})
		}
	}
	return selection, warnings, nil
}
