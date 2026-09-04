package remixlab

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"video-production-console/internal/remixproducer"
	"video-production-console/internal/store"
)

// 生产段编排：二创定稿之后的确认闸门与全自动放行。真正的执行在
// remixproducer 驱动器里（回环调用现有混剪 API）；这里只管状态行的
// 创建、确认、失败重试和视图。

var (
	ErrProductionUnavailable     = errors.New("production driver unavailable")
	ErrProductionAccountRequired = errors.New("production account required")
	ErrProductionActive          = errors.New("production already running")
	ErrProductionDone            = errors.New("production already completed")
	ErrRunNotProducible          = errors.New("run is not producible")
	ErrInvalidAutoProduce        = errors.New("auto produce requires account and run_count=1")
	ErrProductionStepInvalid     = errors.New("production step is not redoable")
)

// ProductionLauncher 由 remixproducer.Driver 实现；接口隔离避免依赖倒挂。
type ProductionLauncher interface {
	Launch(runID string)
}

// SetProducer 在应用装配时注入驱动器。
func (s *Service) SetProducer(p ProductionLauncher) {
	s.producer = p
}

// ProductionView 是 run 的生产状态视图（工作台与画布共用）。
type ProductionView struct {
	Status        string `json:"status"` // waiting_confirm / running / completed / failed
	Step          string `json:"step"`
	AccountID     string `json:"account_id"`
	Auto          bool   `json:"auto"`
	ProjectID     string `json:"project_id,omitempty"`
	SpokenTaskID  string `json:"spoken_task_id,omitempty"`
	CaptionTaskID string `json:"caption_task_id,omitempty"`
	MontageTaskID string `json:"montage_task_id,omitempty"`
	Error         string `json:"error,omitempty"`
}

// ProjectProductionLink 把混剪项目反查到工作流实验，历史项目据此打开画布。
type ProjectProductionLink struct {
	ExperimentID string `json:"experiment_id"`
	RunID        string `json:"run_id"`
	ProjectID    string `json:"project_id"`
	Status       string `json:"status,omitempty"`
}

func (s *Service) ProductionByProject(ctx context.Context, projectID string) (ProjectProductionLink, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectProductionLink{}, store.ErrRemixLabNotFound
	}
	rec, err := s.repo.GetProductionByProjectID(ctx, projectID)
	if err == nil {
		return ProjectProductionLink{
			ExperimentID: rec.ExperimentID, RunID: rec.RunID, ProjectID: rec.ProjectID, Status: rec.Status,
		}, nil
	}
	if !errors.Is(err, store.ErrRemixLabNotFound) {
		return ProjectProductionLink{}, err
	}
	run, err := s.repo.GetLatestRunByAdoptedProject(ctx, projectID)
	if err != nil {
		return ProjectProductionLink{}, err
	}
	return ProjectProductionLink{
		ExperimentID: run.ExperimentID, RunID: run.ID, ProjectID: projectID, Status: run.Status,
	}, nil
}

func productionView(rec store.RemixLabProductionRecord) *ProductionView {
	return &ProductionView{
		Status: rec.Status, Step: rec.Step, AccountID: rec.AccountID, Auto: rec.Auto,
		ProjectID: rec.ProjectID, SpokenTaskID: rec.SpokenTaskID,
		CaptionTaskID: rec.CaptionTaskID, MontageTaskID: rec.MontageTaskID, Error: rec.Error,
	}
}

// StartProduction 是确认闸门与失败重试的统一入口：
//   - 等待确认/无记录 → 需要账号（参数或实验默认），从建项目开始；
//   - 失败 → 保留已完成的步骤与任务 ID 原地续跑（agent 产物、项目、任务都复用）；
//   - 运行中/已完成 → 拒绝。
func (s *Service) StartProduction(ctx context.Context, runID, accountID string) error {
	if s.producer == nil {
		return ErrProductionUnavailable
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != "completed" || strings.TrimSpace(run.ContinuousScript) == "" {
		return ErrRunNotProducible
	}
	exp, _, _, err := s.repo.GetExperiment(ctx, run.ExperimentID)
	if err != nil {
		return err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		accountID = strings.TrimSpace(exp.ProduceAccountID)
	}

	now := s.now()
	existing, getErr := s.repo.GetProduction(ctx, runID)
	switch {
	case getErr == nil && existing.Status == "running":
		return ErrProductionActive
	case getErr == nil && existing.Status == "completed":
		return ErrProductionDone
	case getErr == nil && existing.Status == "failed":
		// 断点续跑：步骤和任务 ID 保留，驱动器会跳过已完成的部分。
		if accountID == "" {
			accountID = existing.AccountID
		}
		if accountID == "" {
			return ErrProductionAccountRequired
		}
		existing.AccountID = accountID
		existing.Status = "running"
		existing.Error = ""
		existing.UpdatedAt = now
		if err := s.repo.UpdateProduction(ctx, existing); err != nil {
			return err
		}
	case getErr == nil:
		// waiting_confirm：闸门放行，从头开始。
		if accountID == "" {
			return ErrProductionAccountRequired
		}
		existing.AccountID = accountID
		existing.Status = "running"
		existing.Step = remixproducer.StepProject
		existing.Error = ""
		existing.UpdatedAt = now
		if err := s.repo.UpdateProduction(ctx, existing); err != nil {
			return err
		}
	case errors.Is(getErr, store.ErrRemixLabNotFound):
		if accountID == "" {
			return ErrProductionAccountRequired
		}
		rec := store.RemixLabProductionRecord{
			RunID: runID, ExperimentID: exp.ID, AccountID: accountID, Auto: exp.ProduceAuto,
			Status: "running", Step: remixproducer.StepProject,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := s.repo.UpsertProduction(ctx, rec); err != nil {
			return err
		}
	default:
		return getErr
	}
	s.producer.Launch(runID)
	return nil
}

// RedoProductionStep 在画布上重做某个生产步骤：从该步起清掉任务 ID 续跑到
// 草稿。重做口播会连带重标关键词、强制重新配音、重出草稿；重做配音会重出
// 草稿；重做混剪只出一份新草稿（旧草稿保留）。运行中不允许重做。
func (s *Service) RedoProductionStep(ctx context.Context, runID, step string) error {
	if s.producer == nil {
		return ErrProductionUnavailable
	}
	rec, err := s.repo.GetProduction(ctx, runID)
	if err != nil {
		return err
	}
	switch rec.Status {
	case "running":
		return ErrProductionActive
	case "completed", "failed":
	default:
		// waiting_confirm：什么都还没生产，没有可重做的步骤。
		return ErrRunNotProducible
	}
	if strings.TrimSpace(rec.ProjectID) == "" || strings.TrimSpace(rec.AccountID) == "" {
		return ErrRunNotProducible
	}
	switch step {
	case remixproducer.StepSpoken:
		rec.SpokenTaskID, rec.CaptionTaskID, rec.MontageTaskID = "", "", ""
		rec.ForceNarration = true
	case remixproducer.StepCaptions:
		exp, _, _, expErr := s.repo.GetExperiment(ctx, rec.ExperimentID)
		if expErr != nil {
			return expErr
		}
		if remixproducer.ParseConfig(exp.WorkflowJSON).CaptionsDisabled {
			return ErrProductionStepInvalid
		}
		rec.CaptionTaskID, rec.MontageTaskID = "", ""
	case remixproducer.StepNarration:
		rec.MontageTaskID = ""
		rec.ForceNarration = true
	case remixproducer.StepMontage:
		rec.MontageTaskID = ""
	default:
		return ErrProductionStepInvalid
	}
	rec.Step = step
	rec.Status = "running"
	rec.Error = ""
	rec.UpdatedAt = s.now()
	if err := s.repo.UpdateProduction(ctx, rec); err != nil {
		return err
	}
	s.producer.Launch(runID)
	return nil
}

// afterWorkflowRunCompleted 在工作流 run 完成时挂生产状态：全自动直接放行，
// 否则落一条等待确认的闸门记录。已有记录（打回重做、断点重试）不动。
func (s *Service) afterWorkflowRunCompleted(ctx context.Context, exp store.RemixLabExperimentRecord, run store.RemixLabRunRecord) {
	if strings.TrimSpace(exp.WorkflowJSON) == "" {
		return
	}
	if _, err := s.repo.GetProduction(ctx, run.ID); err == nil {
		return
	}
	now := s.now()
	rec := store.RemixLabProductionRecord{
		RunID: run.ID, ExperimentID: exp.ID,
		AccountID: strings.TrimSpace(exp.ProduceAccountID), Auto: exp.ProduceAuto,
		CreatedAt: now, UpdatedAt: now,
	}
	if exp.ProduceAuto && rec.AccountID != "" && s.producer != nil {
		rec.Status = "running"
		rec.Step = remixproducer.StepProject
		if err := s.repo.UpsertProduction(ctx, rec); err != nil {
			slog.Default().Error("auto production upsert failed", "run_id", run.ID, "error", err)
			return
		}
		slog.Default().Info("auto production started", "run_id", run.ID, "account_id", rec.AccountID)
		s.producer.Launch(run.ID)
		return
	}
	rec.Status = "waiting_confirm"
	rec.Step = remixproducer.StepConfirm
	if err := s.repo.UpsertProduction(ctx, rec); err != nil {
		slog.Default().Error("confirm gate upsert failed", "run_id", run.ID, "error", err)
	}
}

// validateProduceOptions 校验开跑时的生产参数。
func validateProduceOptions(accountID string, auto bool, runCount int) error {
	accountID = strings.TrimSpace(accountID)
	if accountID != "" {
		if _, err := uuid.Parse(accountID); err != nil {
			return ErrProductionAccountRequired
		}
	}
	if auto && (accountID == "" || runCount != 1) {
		return ErrInvalidAutoProduce
	}
	return nil
}
