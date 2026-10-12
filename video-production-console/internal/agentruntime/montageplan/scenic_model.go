package montageplan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type scenicModelAudit struct {
	Version        int              `json:"version"`
	InputHash      string           `json:"input_hash"`
	RequestedModel string           `json:"requested_model"`
	Status         string           `json:"status"`
	Jobs           int              `json:"jobs"`
	Applied        int              `json:"applied"`
	Rejected       int              `json:"rejected"`
	BatchRequests  int              `json:"batch_requests"`
	Picks          []ShotSelectPick `json:"picks"`
	Warnings       []string         `json:"warnings"`
}

// ModelIdentity names the requested provider model, not a claim about the
// actual model behind a third-party gateway. Secrets never enter this report.
func (a HTTPIntentAnalyzer) ModelIdentity() string { return a.model }

// rerankScenicShots retains the deterministic timing/rights/duplicate guards.
// The model may ONLY choose an enumerated shortlist ID. It cannot set paths,
// timestamps, resource IDs, commands, or weaken any source eligibility check.
func rerankScenicShots(opts Options, ctx *planContext, sentences []TimedSentence, shots []TimelineShotV2, selected, pool []mediaItem) ([]TimelineShotV2, []mediaItem, scenicModelAudit, error) {
	audit := scenicModelAudit{Version: 1, Status: "rules_fallback", Jobs: len(shots), Picks: []ShotSelectPick{}, Warnings: []string{}}
	if opts.ShotSelector == nil {
		audit.Warnings = append(audit.Warnings, "model_not_configured")
		return shots, selected, audit, nil
	}
	if p, ok := opts.ShotSelector.(interface{ ModelIdentity() string }); ok {
		audit.RequestedModel = p.ModelIdentity()
	} else {
		audit.RequestedModel = "injected_selector"
	}
	jobs := make([]ShotSelectJob, 0, len(shots))
	lookup := map[string]mediaItem{}
	// Reserve ALL original sources, then allocate alternative scene groups to
	// only one shortlist. Model choices cannot steal a later slot's fallback.
	reserved := map[string]bool{}
	for _, it := range selected {
		reserved[scenicGroupKey(it)] = true
		lookup[it.ID] = it
	}
	for i, shot := range shots {
		text := scenicTextAt(sentences, shot.StartS, shot.EndS-shot.StartS)
		job := ShotSelectJob{Intent: NarrativeIntent{SegmentID: fmt.Sprintf("scenic-%03d", i+1), Text: text, StartMS: int64(math.Round(shot.StartS * 1000)), EndMS: int64(math.Round(shot.EndS * 1000))}}
		add := func(it mediaItem) {
			score, _, _ := scenicFit(text, it)
			job.Candidates = append(job.Candidates, ShotSelectCandidate{ShotID: it.ID, Tags: it.Tags, Setting: it.Category, Summary: it.Summary, Score: score})
			lookup[it.ID] = it
			reserved[scenicGroupKey(it)] = true
		}
		add(selected[i])
		type candidate struct {
			item  mediaItem
			score float64
		}
		var candidates []candidate
		for _, it := range pool {
			if reserved[scenicGroupKey(it)] || (it.Kind != mediaKindImage && it.availableSeconds() < shot.EndS-shot.StartS+.04) {
				continue
			}
			score, _, _ := scenicFit(text, it)
			score += scenicHash(ctx.manifest.TaskID, it, i) * .15
			if strings.Contains(scenicMetadata(it), "timelapse") || strings.Contains(scenicMetadata(it), "hyperlapse") {
				score -= .25
			}
			candidates = append(candidates, candidate{it, score})
		}
		sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].score > candidates[b].score })
		categoryCount := map[string]int{selected[i].Category: 1}
		for pass := 0; pass < 2; pass++ {
			for _, c := range candidates {
				if len(job.Candidates) >= 6 {
					break
				}
				if reserved[scenicGroupKey(c.item)] {
					continue
				}
				if pass == 0 && categoryCount[c.item.Category] >= 2 {
					continue
				}
				add(c.item)
				categoryCount[c.item.Category]++
			}
		}
		jobs = append(jobs, job)
	}
	payload, _ := json.Marshal(struct {
		Model string
		Jobs  []ShotSelectJob
	}{audit.RequestedModel, jobs})
	digest := sha256.Sum256(payload)
	audit.InputHash = fmt.Sprintf("%x", digest)
	dir := filepath.Dir(opts.PlanPath)
	cachePath := filepath.Join(dir, "scenic_model_selection.json")
	if raw, e := os.ReadFile(cachePath); e == nil {
		var cached scenicModelAudit
		if json.Unmarshal(raw, &cached) == nil && cached.Version == 1 && cached.InputHash == audit.InputHash {
			audit = cached
			audit.Status = "cache_replayed"
		} else {
			return nil, nil, audit, fmt.Errorf("model selection snapshot changed: create a new montage task")
		}
	} else {
		parent := opts.Context
		if parent == nil {
			parent = context.Background()
		}
		callCtx, cancel := context.WithTimeout(parent, 360*time.Second)
		defer cancel()
		type result struct {
			picks []ShotSelectPick
			err   error
		}
		batches := (len(jobs) + 7) / 8
		results := make([]result, batches)
		sem := make(chan struct{}, 3)
		var wg sync.WaitGroup
		for b := 0; b < batches; b++ {
			wg.Add(1)
			go func(b int) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-callCtx.Done():
					results[b].err = callCtx.Err()
					return
				}
				defer func() { <-sem }()
				end := min((b+1)*8, len(jobs))
				results[b].picks, results[b].err = opts.ShotSelector.SelectShots(callCtx, jobs[b*8:end])
			}(b)
		}
		wg.Wait()
		audit.BatchRequests = batches
		audit.Status = "model_completed"
		for b, res := range results {
			audit.Picks = append(audit.Picks, res.picks...)
			if res.err != nil {
				audit.Status = "model_partial_fallback"
				audit.Warnings = append(audit.Warnings, fmt.Sprintf("model_batch_%d_failed", b+1))
			}
		}
		if parent.Err() != nil {
			return nil, nil, audit, parent.Err()
		}
	}
	// Revalidate even cached or injected picks. SDK/LLM output is untrusted data.
	allowed := map[string]map[string]bool{}
	for _, j := range jobs {
		allowed[j.Intent.SegmentID] = map[string]bool{}
		for _, c := range j.Candidates {
			allowed[j.Intent.SegmentID][c.ShotID] = true
		}
	}
	picks := map[string]ShotSelectPick{}
	for _, pick := range audit.Picks {
		if allowed[pick.IntentID][pick.ShotID] {
			if _, exists := picks[pick.IntentID]; !exists {
				picks[pick.IntentID] = pick
			}
		}
	}
	audit.Applied = 0
	audit.Rejected = 0
	for i := range shots {
		id := jobs[i].Intent.SegmentID
		pick, ok := picks[id]
		if !ok {
			continue
		}
		it := lookup[pick.ShotID]
		collision := false
		for j, old := range selected {
			if j != i && (scenicGroupKey(old) == scenicGroupKey(it) || scenicSourceKey(old) == scenicSourceKey(it) || nearSame(old, it)) {
				collision = true
				break
			}
		}
		if collision {
			audit.Rejected++
			continue
		}
		shot := &shots[i]
		duration := shot.EndS - shot.StartS
		if it.Kind != mediaKindImage && it.availableSeconds() < duration {
			audit.Rejected++
			continue
		}
		shot.SourceID = it.ID
		shot.ShotID = it.ShotID
		shot.SourcePath = it.AbsPath
		shot.MediaKind = string(it.Kind)
		if it.Kind == mediaKindImage {
			shot.SourceInS = nil
			shot.SourceOutS = nil
			shot.Motion = imageMotionPlan(i)
		} else {
			base := 0.0
			limit := it.DurationSeconds
			if it.hasShotRange() {
				base = it.SourceInSeconds
				limit = it.SourceOutSeconds
			}
			in := base + math.Max(0, limit-base-duration)*scenicHash(ctx.manifest.TaskID, it, i+2000)
			out := in + duration
			shot.SourceInS = &in
			shot.SourceOutS = &out
		}
		fit, level, _ := scenicFit(jobs[i].Intent.Text, it)
		why := []rune(strings.TrimSpace(pick.Why))
		if len(why) > 80 {
			why = why[:80]
		}
		shot.Match = MatchEvidence{Level: level, Score: fit, IntentID: id, Reason: "model_shortlist: " + string(why) + "；依据素材描述，非现场证明"}
		selected[i] = it
		audit.Applied++
	}
	if audit.Applied == 0 {
		audit.Status = "rules_fallback"
		audit.Warnings = append(audit.Warnings, "no_usable_model_picks")
	}
	if err := writeScenicJSON(cachePath, audit); err != nil {
		return nil, nil, audit, err
	}
	// Debug input carries only supplied narration and candidate metadata.
	if err := writeScenicJSON(filepath.Join(dir, "scenic_model_candidates.json"), jobs); err != nil {
		return nil, nil, audit, err
	}
	return shots, selected, audit, nil
}
