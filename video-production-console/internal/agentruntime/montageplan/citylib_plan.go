package montageplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const cityOpeningRetries = 3

func cityProjectID(taskID string, attempt int) string {
	if attempt == 1 {
		return "console-" + taskID
	}
	return fmt.Sprintf("console-%s-r%d", taskID, attempt)
}

// Recover the latest revision even if the process stopped before writing its receipt.
func latestCityAttempt(ctx context.Context, pc *planContext) (*CityLibraryReservation, int, error) {
	db, err := openCityCatalog(ctx, cityCatalogPath(pc.mediaRoot), true)
	if err != nil {
		return nil, 1, err
	}
	defer db.Close()
	var latest *CityLibraryReservation
	var evidence []CityOpeningMotionEvidence
	seen := map[string]bool{}
	attempt := 1
	for i := 1; i <= cityOpeningRetries+1; i++ {
		var status, raw string
		err := db.QueryRowContext(ctx, "SELECT status,plan FROM projects WHERE project_id=?", cityProjectID(pc.manifest.TaskID, i)).Scan(&status, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, i, err
		}
		if status == "completed" {
			return nil, i, fmt.Errorf("该任务的素材已提交，请新建任务重出草稿")
		}
		var plan CityLibraryReservation
		if err := json.Unmarshal([]byte(raw), &plan); err != nil {
			return nil, i, err
		}
		plan.Status = status
		for _, item := range plan.OpeningMotionReview.Evidence {
			key := item.ProjectID + "\x00" + item.AssetID
			if !seen[key] {
				evidence = append(evidence, item)
				seen[key] = true
			}
		}
		latest, attempt = &plan, i
	}
	if latest != nil {
		latest.OpeningMotionReview.Evidence = evidence
	}
	return latest, attempt, nil
}

func releaseCityPlan(plan *CityLibraryReservation) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return finalizeCityReservation(ctx, plan, false)
}

func saveCityReview(ctx context.Context, plan *CityLibraryReservation, receiptPath string) error {
	if err := writeScenicJSON(receiptPath, plan); err != nil {
		return err
	}
	db, err := openCityCatalog(ctx, plan.CatalogPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return withCityTransaction(ctx, db, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, "UPDATE projects SET plan=? WHERE project_id=? AND status='reserved'", string(raw), plan.ProjectID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("保存开头检测时，预占项目状态已经改变")
		}
		return nil
	})
}

func recordCityWarning(receiptPath, message string) {
	log.Printf("citylib warning: %s", message)
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		return
	}
	var receipt CityLibraryReservation
	if json.Unmarshal(raw, &receipt) != nil {
		return
	}
	receipt.Warnings = append(receipt.Warnings, message)
	if err := writeScenicJSON(receiptPath, receipt); err != nil {
		log.Printf("citylib warning receipt: %v", err)
	}
}

func buildCityLibraryPlan(opts Options, pc *planContext, p ScenicPolicy) (shots []TimelineShotV2, notes []string, resultErr error) {
	ctx := plannerContext(opts)
	output := filepath.Dir(opts.PlanPath)
	receiptPath := filepath.Join(output, "citylib_reservation.json")
	if err := writeScenicJSON(filepath.Join(output, "scenic_policy_snapshot.json"), scenicSnapshot{Policy: p, History: map[string]int{}}); err != nil {
		return nil, nil, err
	}
	previous, attempt, err := latestCityAttempt(ctx, pc)
	if err != nil {
		return nil, nil, err
	}
	excluded := []string{}
	evidence := []CityOpeningMotionEvidence{}
	if previous != nil {
		excluded = append(excluded, previous.SelectionPolicy.OpeningExcluded...)
		for _, e := range previous.OpeningMotionReview.Evidence {
			if e.ProjectID != previous.ProjectID {
				evidence = append(evidence, e)
			}
		}
		if previous.Status == "cancelled" && !previous.OpeningMotionReview.Passed {
			static := false
			for _, e := range previous.OpeningMotionReview.Evidence {
				if e.ProjectID == previous.ProjectID && e.Static {
					excluded = append(excluded, e.AssetID)
					static = true
				}
			}
			if static {
				attempt++
				evidence = previous.OpeningMotionReview.Evidence
			}
		}
	}
	var plan *CityLibraryReservation
	defer func() {
		if plan != nil && (resultErr != nil || pc.manifest.Action == "montage.plan") {
			if err := releaseCityPlan(plan); err != nil {
				warning := "城市素材预占释放失败: " + err.Error()
				recordCityWarning(receiptPath, warning)
				notes = append(notes, warning)
			}
		}
	}()
	for ; attempt <= cityOpeningRetries+1; attempt++ {
		reserved, err := reserveCityLibrary(ctx, pc, p, cityProjectID(pc.manifest.TaskID, attempt), excluded)
		if err != nil {
			return nil, nil, err
		}
		plan = reserved
		review, reviewErr := reviewCityOpening(ctx, pc.manifest.NonSecretSettings.FFmpegPath, plan)
		review.Evidence = append(evidence, review.Evidence...)
		plan.OpeningMotionReview = review
		if err := saveCityReview(ctx, plan, receiptPath); err != nil {
			return nil, nil, err
		}
		if reviewErr != nil {
			return nil, nil, reviewErr
		}
		if review.Passed {
			shots, qc := cityTimeline(pc, p, plan)
			if err := writeScenicJSON(filepath.Join(output, "scenic_selection_qc.json"), qc); err != nil {
				return nil, nil, err
			}
			return shots, []string{fmt.Sprintf("citylib: architecture_city_catalog_reservation project=%s speed=1.0", plan.ProjectID)}, nil
		}
		for _, e := range review.Evidence {
			if e.ProjectID == plan.ProjectID && e.Static {
				excluded = append(excluded, e.AssetID)
			}
		}
		evidence = review.Evidence
		if err := releaseCityPlan(plan); err != nil {
			recordCityWarning(receiptPath, "城市素材预占释放失败: "+err.Error())
			return nil, nil, fmt.Errorf("开头画面几乎静止，本次预占未能释放，已停止重选")
		}
	}
	return nil, nil, fmt.Errorf("开头画面几乎静止，重试 %d 次后仍未通过，请补充有明显运动的住宅楼群或城市航拍素材", cityOpeningRetries)
}

func cityTimeline(pc *planContext, p ScenicPolicy, plan *CityLibraryReservation) ([]TimelineShotV2, ScenicSelectionQC) {
	shots := make([]TimelineShotV2, 0, len(plan.Clips))
	qc := ScenicSelectionQC{Version: 1, TaskID: pc.manifest.TaskID, Basis: "architecture_city_catalog_reservation", ModelStatus: "disabled_catalog_rules", PlaybackSpeed: 1, Warnings: []string{}, SourceURLs: []string{}}
	if pc.manifest.Project != nil {
		qc.AccountID = pc.manifest.Project.AccountID
	}
	for i, c := range plan.Clips {
		in, out := c.SourceIn, c.SourceOut
		shot := TimelineShotV2{ShotNo: i + 1, StartS: c.TimelineIn, EndS: c.TimelineOut, MediaKind: "broll", SourceID: c.AssetID, SourcePath: c.Path, SourceInS: &in, SourceOutS: &out, Motion: MotionPlan{Preset: "steady", ScaleFrom: 1.20, ScaleTo: 1.20}, Opacity: 1, SourceAudioMuted: true, Look: "none",
			Match: MatchEvidence{Level: "neutral", IntentID: fmt.Sprintf("citylib-%03d", i+1), Reason: "城市台账按引用层级随机预占；画面为示意素材"}}
		if p.EditorVersion > 0 && i%3 != 2 {
			if i%2 == 0 {
				shot.Motion = MotionPlan{Preset: "push", ScaleFrom: 1.20, ScaleTo: 1.24}
			} else {
				shot.Motion = MotionPlan{Preset: "pull", ScaleFrom: 1.24, ScaleTo: 1.20}
			}
		}
		shots = append(shots, shot)
		qc.SourceKeys = append(qc.SourceKeys, c.AssetID)
	}
	// Match the China editor's dissolve ordering without reranking catalog choices.
	type boundary struct {
		i     int
		score float64
	}
	var boundaries []boundary
	first := 2
	if p.EditorVersion > 0 {
		first = 1
	}
	for i := first; i < len(shots); i++ {
		c := plan.Clips[i]
		score := scenicHash(pc.manifest.TaskID, mediaItem{SHA256: c.AssetID}, i+3000)
		if p.EditorVersion > 0 && i == 1 {
			score += 100
		}
		if c.Category != plan.Clips[i-1].Category {
			score++
		}
		boundaries = append(boundaries, boundary{i, score})
	}
	sort.Slice(boundaries, func(i, j int) bool {
		if boundaries[i].score != boundaries[j].score {
			return boundaries[i].score > boundaries[j].score
		}
		return boundaries[i].i < boundaries[j].i
	})
	wanted := int(math.Round(float64(len(shots)-1) * p.DissolveRatio))
	applied := map[int]bool{}
	for _, b := range boundaries {
		if len(applied) >= wanted {
			break
		}
		if applied[b.i-1] || applied[b.i+1] {
			continue
		}
		shots[b.i].Transition = &TransitionV2{Preset: v2TransitionPreset, DurationS: v2TransitionDurationS}
		applied[b.i] = true
	}
	qc.ShotCount = len(shots)
	qc.UniqueSources = len(shots)
	qc.MeanShotSeconds = plan.Duration / float64(len(shots))
	if len(shots) > 1 {
		qc.DissolveRatio = float64(len(applied)) / float64(len(shots)-1)
	}
	return shots, qc
}
