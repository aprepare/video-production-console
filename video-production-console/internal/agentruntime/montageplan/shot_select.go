package montageplan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	shotSelectBatchSize      = 8
	shotSelectCandidateLimit = 8
	shotSelectBoost          = 0.9
)

// ShotSelector is a shortlist reranker, not a full-library search.
// rankLibrary already pulled tag hits plus embedding neighbors; this
// only asks a chat model to pick 1 of the top N per spoken line.
// Failure must fall back to the ranked scores (see plan_v2.go).
type ShotSelector interface {
	SelectShots(ctx context.Context, jobs []ShotSelectJob) ([]ShotSelectPick, error)
}

type ShotSelectJob struct {
	Intent     NarrativeIntent
	Candidates []ShotSelectCandidate
}

type ShotSelectCandidate struct {
	ShotID  string
	Tags    []string
	Mood    string
	Setting string
	Summary string
	Score   float64
}

type ShotSelectPick struct {
	IntentID string `json:"segment_id"`
	ShotID   string `json:"shot_id"`
	Why      string `json:"why"`
}

func (a HTTPIntentAnalyzer) SelectShots(ctx context.Context, jobs []ShotSelectJob) ([]ShotSelectPick, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	out := make([]ShotSelectPick, 0, len(jobs))
	for i := 0; i < len(jobs); i += shotSelectBatchSize {
		end := i + shotSelectBatchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		picks, err := a.selectShotBatch(ctx, jobs[i:end])
		if err != nil {
			return out, err
		}
		out = append(out, picks...)
	}
	return out, nil
}

func (a HTTPIntentAnalyzer) selectShotBatch(ctx context.Context, jobs []ShotSelectJob) ([]ShotSelectPick, error) {
	content, err := a.postChat(ctx, shotSelectSystemPrompt(), shotSelectUserPrompt(jobs))
	if err != nil {
		return nil, err
	}
	return parseShotSelectJSON(content, jobs)
}

func shotSelectSystemPrompt() string {
	return strings.TrimSpace(`你是财经短视频剪辑师。素材库几乎没有真实住宅或法拍现场，只有城市、办公、硬币、钱包、计算器、K线、握手、室内。
为每一句口播从候选里选 1 个最贴近的 shot_id。只能用候选里的 id，禁止编造。
优先：住房/法拍/楼市 → 城市天际线或室内；月供/房贷/首付 → 硬币、计算器、钱包；危险/没人接 → 空钱包或冷清城市；合同/中介 → 签字、握手。同一 shot_id 不要重复用。
只输出 JSON 数组：[{"segment_id":"seg-001","shot_id":"...","why":"不超过16字"}]`)
}

func shotSelectUserPrompt(jobs []ShotSelectJob) string {
	type row struct {
		SegmentID  string                `json:"segment_id"`
		Text       string                `json:"text"`
		Mood       string                `json:"mood"`
		Concepts   []string              `json:"visual_concepts"`
		Candidates []ShotSelectCandidate `json:"candidates"`
	}
	payload := make([]row, 0, len(jobs))
	for _, job := range jobs {
		cands := job.Candidates
		if len(cands) > shotSelectCandidateLimit {
			cands = cands[:shotSelectCandidateLimit]
		}
		for i := range cands {
			if len(cands[i].Summary) > 220 {
				cands[i].Summary = cands[i].Summary[:220]
			}
		}
		payload = append(payload, row{
			SegmentID:  job.Intent.SegmentID,
			Text:       job.Intent.Text,
			Mood:       job.Intent.Mood,
			Concepts:   job.Intent.VisualConcepts,
			Candidates: cands,
		})
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func parseShotSelectJSON(content string, jobs []ShotSelectJob) ([]ShotSelectPick, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var picks []ShotSelectPick
	if err := json.Unmarshal([]byte(content), &picks); err != nil {
		return nil, err
	}
	allowed := map[string]map[string]bool{}
	for _, job := range jobs {
		ids := map[string]bool{}
		for _, candidate := range job.Candidates {
			ids[candidate.ShotID] = true
		}
		allowed[job.Intent.SegmentID] = ids
	}
	out := make([]ShotSelectPick, 0, len(picks))
	seenShot := map[string]bool{}
	for _, pick := range picks {
		ids := allowed[strings.TrimSpace(pick.IntentID)]
		if !ids[strings.TrimSpace(pick.ShotID)] || seenShot[pick.ShotID] {
			continue
		}
		seenShot[pick.ShotID] = true
		out = append(out, pick)
	}
	return out, nil
}

func applyShotSelections(ranked []rankedCandidate, picks []ShotSelectPick) ([]rankedCandidate, []string) {
	if len(picks) == 0 {
		return ranked, nil
	}
	type key struct{ shot, intent string }
	index := map[key]int{}
	for i, candidate := range ranked {
		index[key{candidate.Item.ShotID, candidate.Match.IntentID}] = i
	}
	applied := 0
	for _, pick := range picks {
		i, ok := index[key{pick.ShotID, pick.IntentID}]
		if !ok {
			continue
		}
		if ranked[i].Score < shotSelectBoost {
			ranked[i].Score = shotSelectBoost
		}
		why := strings.TrimSpace(pick.Why)
		if why == "" {
			why = pick.ShotID
		}
		ranked[i].Match.Level = matchLevelDirect
		ranked[i].Match.Score = ranked[i].Score
		ranked[i].Match.Reason = "llm: " + why
		applied++
	}
	if applied == 0 {
		return ranked, nil
	}
	sortRanked(ranked)
	return ranked, []string{fmt.Sprintf("llm_shot_select: picked %d segment shots", applied)}
}

func buildShotSelectJobs(intents []NarrativeIntent, ranked []rankedCandidate) []ShotSelectJob {
	byIntent := map[string][]rankedCandidate{}
	for _, candidate := range ranked {
		id := strings.TrimSpace(candidate.Match.IntentID)
		if id == "" || id == "catalog-fill" || candidate.Item.ShotID == "" {
			continue
		}
		byIntent[id] = append(byIntent[id], candidate)
	}
	jobs := make([]ShotSelectJob, 0, len(intents))
	for _, intent := range intents {
		pool := byIntent[intent.SegmentID]
		if len(pool) == 0 {
			continue
		}
		sortRanked(pool)
		if len(pool) > shotSelectCandidateLimit {
			pool = pool[:shotSelectCandidateLimit]
		}
		cands := make([]ShotSelectCandidate, 0, len(pool))
		seen := map[string]bool{}
		for _, candidate := range pool {
			if seen[candidate.Item.ShotID] {
				continue
			}
			seen[candidate.Item.ShotID] = true
			cands = append(cands, ShotSelectCandidate{
				ShotID:  candidate.Item.ShotID,
				Tags:    candidate.Item.Tags,
				Mood:    candidate.Item.Mood,
				Setting: candidate.Item.Category,
				Summary: candidate.Item.Summary,
				Score:   candidate.Score,
			})
		}
		if len(cands) == 0 {
			continue
		}
		jobs = append(jobs, ShotSelectJob{Intent: intent, Candidates: cands})
	}
	return jobs
}
