package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

type TaskTimingRepository struct{ db *sql.DB }

type StartPhase struct {
	TaskID      string
	Attempt     int
	Key         string
	DisplayName string
	Source      domain.TaskPhaseSource
	State       domain.TaskPhaseState
	ExternalID  string
	DetailJSON  string
	At          time.Time
}

type FinishPhase struct {
	ID    string
	State domain.TaskPhaseState
	At    time.Time
}

func NewTaskTimingRepository(db *sql.DB) *TaskTimingRepository { return &TaskTimingRepository{db: db} }

func (r *TaskTimingRepository) StartPhase(ctx context.Context, input StartPhase) (domain.TaskPhaseRun, error) {
	input.TaskID, input.Key, input.DisplayName = strings.TrimSpace(input.TaskID), strings.TrimSpace(input.Key), strings.TrimSpace(input.DisplayName)
	if input.TaskID == "" || input.Key == "" || input.DisplayName == "" || input.Attempt <= 0 || input.At.IsZero() {
		return domain.TaskPhaseRun{}, fmt.Errorf("task, phase, display name, positive attempt, and start time are required")
	}
	if input.State == "" {
		input.State = domain.PhaseRunning
	}
	if input.State != domain.PhaseQueued && input.State != domain.PhaseRunning {
		return domain.TaskPhaseRun{}, fmt.Errorf("phase initial state must be queued or running")
	}
	if input.Source != domain.PhaseSourceHost && input.Source != domain.PhaseSourceAppServer && input.Source != domain.PhaseSourceSkill {
		return domain.TaskPhaseRun{}, fmt.Errorf("invalid phase source %q", input.Source)
	}
	if input.DetailJSON == "" {
		input.DetailJSON = `{}`
	}
	if !json.Valid([]byte(input.DetailJSON)) {
		return domain.TaskPhaseRun{}, fmt.Errorf("phase detail must be valid JSON")
	}
	if input.ExternalID != "" {
		var existingID string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM task_phase_runs WHERE task_id=? AND attempt=? AND phase_key=? AND source=? AND external_id=?`, input.TaskID, input.Attempt, input.Key, input.Source, input.ExternalID).Scan(&existingID)
		if err == nil {
			return r.phase(ctx, existingID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.TaskPhaseRun{}, err
		}
	}
	id := uuid.NewString()
	var runningAt any
	if input.State == domain.PhaseRunning {
		runningAt = input.At
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, input.TaskID, input.Attempt, input.Key, input.DisplayName, input.Source, input.State, input.At, runningAt, nullable(input.ExternalID), input.DetailJSON, input.At)
	if err != nil && input.ExternalID != "" {
		var existingID string
		if lookupErr := r.db.QueryRowContext(ctx, `SELECT id FROM task_phase_runs WHERE task_id=? AND attempt=? AND phase_key=? AND source=? AND external_id=?`, input.TaskID, input.Attempt, input.Key, input.Source, input.ExternalID).Scan(&existingID); lookupErr == nil {
			return r.phase(ctx, existingID)
		}
	}
	if err != nil {
		return domain.TaskPhaseRun{}, err
	}
	return r.phase(ctx, id)
}

func (r *TaskTimingRepository) MarkPhaseRunning(ctx context.Context, id string, at time.Time) (domain.TaskPhaseRun, error) {
	if strings.TrimSpace(id) == "" || at.IsZero() {
		return domain.TaskPhaseRun{}, fmt.Errorf("phase and running time are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE task_phase_runs SET state='running',running_at=? WHERE id=? AND state='queued' AND ?>=started_at`, at, id, at)
	if err != nil {
		return domain.TaskPhaseRun{}, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return domain.TaskPhaseRun{}, err
	} else if affected == 0 {
		phase, readErr := r.phase(ctx, id)
		if readErr != nil {
			return domain.TaskPhaseRun{}, readErr
		}
		if phase.State != domain.PhaseRunning {
			return domain.TaskPhaseRun{}, fmt.Errorf("phase %q cannot transition from %s to running", id, phase.State)
		}
		return phase, nil
	}
	return r.phase(ctx, id)
}

func (r *TaskTimingRepository) FinishPhase(ctx context.Context, input FinishPhase) (domain.TaskPhaseRun, error) {
	if input.State != domain.PhaseCompleted && input.State != domain.PhaseFailed && input.State != domain.PhaseCanceled && input.State != domain.PhaseInterrupted {
		return domain.TaskPhaseRun{}, fmt.Errorf("invalid terminal phase state %q", input.State)
	}
	phase, err := r.phase(ctx, input.ID)
	if err != nil {
		return domain.TaskPhaseRun{}, err
	}
	if input.At.Before(phase.StartedAt) {
		return domain.TaskPhaseRun{}, fmt.Errorf("phase finish precedes start")
	}
	if phase.FinishedAt != nil {
		return phase, nil
	}
	duration := input.At.Sub(phase.StartedAt).Milliseconds()
	result, err := r.db.ExecContext(ctx, `UPDATE task_phase_runs SET state=?,finished_at=?,duration_ms=? WHERE id=? AND finished_at IS NULL AND state IN ('queued','running')`, input.State, input.At, duration, input.ID)
	if err != nil {
		return domain.TaskPhaseRun{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return domain.TaskPhaseRun{}, err
		}
		return domain.TaskPhaseRun{}, fmt.Errorf("phase %q is not active", input.ID)
	}
	return r.phase(ctx, input.ID)
}

func (r *TaskTimingRepository) InterruptRunning(ctx context.Context, at time.Time) (int, error) {
	if at.IsZero() {
		return 0, fmt.Errorf("interruption time is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE task_phase_runs SET state='interrupted',finished_at=?,duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER) WHERE state='running' AND ?>=started_at`, at, at, at)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func (r *TaskTimingRepository) ForTask(ctx context.Context, taskID string) ([]domain.TaskPhaseRun, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,finished_at,duration_ms,COALESCE(external_id,''),detail_json,created_at FROM task_phase_runs WHERE task_id=? ORDER BY attempt,started_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var phases []domain.TaskPhaseRun
	for rows.Next() {
		phase, err := scanPhase(rows)
		if err != nil {
			return nil, err
		}
		phases = append(phases, phase)
	}
	return phases, rows.Err()
}

func (r *TaskTimingRepository) SummaryForTask(ctx context.Context, taskID string, now time.Time) (domain.TaskTimingSummary, error) {
	var created time.Time
	var queued, started, finished sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT created_at,queued_at,started_at,finished_at FROM codex_tasks WHERE id=?`, taskID).Scan(&created, &queued, &started, &finished); err != nil {
		return domain.TaskTimingSummary{}, err
	}
	phases, err := r.ForTask(ctx, taskID)
	if err != nil {
		return domain.TaskTimingSummary{}, err
	}
	terminal := now
	if finished.Valid {
		terminal = finished.Time
	}
	firstExecution := started.Time
	if !started.Valid {
		firstExecution = terminal
	}
	queueBoundary, estimated := created, !queued.Valid
	if queued.Valid {
		queueBoundary = queued.Time
	}
	summary := domain.TaskTimingSummary{TaskID: taskID, Phases: phases, QueueEstimated: estimated, LegacyWithoutPhases: len(phases) == 0}
	summary.TotalMS = nonNegativeMillis(created, terminal)
	summary.PreparationMS = nonNegativeMillis(created, queueBoundary)
	summary.QueueMS = nonNegativeMillis(queueBoundary, firstExecution)
	summary.ExecutionMS = nonNegativeMillis(firstExecution, terminal)
	for i := range phases {
		phase := &phases[i]
		if started.Valid && !phase.StartedAt.Before(firstExecution) && phase.State == domain.PhaseCompleted && phase.DurationMS != nil && (summary.SlowestPhase == nil || *phase.DurationMS > *summary.SlowestPhase.DurationMS) {
			copy := *phase
			summary.SlowestPhase = &copy
		}
	}
	if summary.SlowestPhase != nil && summary.ExecutionMS > 0 {
		summary.SlowestPhasePercent = float64(*summary.SlowestPhase.DurationMS) * 100 / float64(summary.ExecutionMS)
	}
	return summary, nil
}

func (r *TaskTimingRepository) ProjectSummary(ctx context.Context, projectID string, limit int) ([]domain.TaskTimingAggregate, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `SELECT id,action FROM codex_tasks WHERE action IS NOT NULL`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type taskAction struct {
		id     string
		action domain.TaskAction
	}
	var tasks []taskAction
	for rows.Next() {
		var task taskAction
		if err := rows.Scan(&task.id, &task.action); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	type phaseValues struct {
		displayName string
		durations   []int64
	}
	type values struct {
		totals, executions []int64
		phases             map[string]*phaseValues
	}
	groups := map[domain.TaskAction]*values{}
	for _, task := range tasks {
		summary, err := r.SummaryForTask(ctx, task.id, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		group := groups[task.action]
		if group == nil {
			group = &values{phases: map[string]*phaseValues{}}
			groups[task.action] = group
		}
		group.totals = append(group.totals, summary.TotalMS)
		group.executions = append(group.executions, summary.ExecutionMS)
		for _, phase := range summary.Phases {
			if phase.State != domain.PhaseCompleted || phase.DurationMS == nil {
				continue
			}
			values := group.phases[phase.PhaseKey]
			if values == nil {
				values = &phaseValues{displayName: phase.DisplayName}
				group.phases[phase.PhaseKey] = values
			}
			values.durations = append(values.durations, *phase.DurationMS)
		}
	}
	result := make([]domain.TaskTimingAggregate, 0, len(groups))
	for action, group := range groups {
		sort.Slice(group.totals, func(i, j int) bool { return group.totals[i] < group.totals[j] })
		sort.Slice(group.executions, func(i, j int) bool { return group.executions[i] < group.executions[j] })
		aggregate := domain.TaskTimingAggregate{Action: action, TaskCount: len(group.totals), MedianTotalMS: median(group.totals), MaxTotalMS: group.totals[len(group.totals)-1], MedianExecutionMS: median(group.executions), MaxExecutionMS: group.executions[len(group.executions)-1]}
		for key, values := range group.phases {
			sort.Slice(values.durations, func(i, j int) bool { return values.durations[i] < values.durations[j] })
			aggregate.Phases = append(aggregate.Phases, domain.TaskPhaseTimingAggregate{PhaseKey: key, DisplayName: values.displayName, Samples: len(values.durations), MedianDurationMS: median(values.durations), MaxDurationMS: values.durations[len(values.durations)-1]})
		}
		sort.Slice(aggregate.Phases, func(i, j int) bool { return aggregate.Phases[i].PhaseKey < aggregate.Phases[j].PhaseKey })
		result = append(result, aggregate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Action < result[j].Action })
	return result, nil
}

func (r *TaskTimingRepository) phase(ctx context.Context, id string) (domain.TaskPhaseRun, error) {
	return scanPhase(r.db.QueryRowContext(ctx, `SELECT id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,finished_at,duration_ms,COALESCE(external_id,''),detail_json,created_at FROM task_phase_runs WHERE id=?`, id))
}

type phaseScanner interface{ Scan(...any) error }

func scanPhase(scanner phaseScanner) (domain.TaskPhaseRun, error) {
	var phase domain.TaskPhaseRun
	err := scanner.Scan(&phase.ID, &phase.TaskID, &phase.Attempt, &phase.PhaseKey, &phase.DisplayName, &phase.Source, &phase.State, &phase.StartedAt, &phase.RunningAt, &phase.FinishedAt, &phase.DurationMS, &phase.ExternalID, &phase.DetailJSON, &phase.CreatedAt)
	return phase, err
}

func nonNegativeMillis(start, finish time.Time) int64 {
	if finish.Before(start) {
		return 0
	}
	return finish.Sub(start).Milliseconds()
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	middle := len(values) / 2
	if len(values)%2 != 0 {
		return values[middle]
	}
	return values[middle-1] + (values[middle]-values[middle-1])/2
}
