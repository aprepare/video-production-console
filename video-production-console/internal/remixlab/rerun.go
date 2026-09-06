package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

var ErrInvalidRerun = errors.New("invalid rerun model settings")

type RerunModel struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	ServiceTier     string `json:"service_tier"`
}
type RerunInput struct {
	References *[]RerunModel `json:"references,omitempty"`
	Planner    *RerunModel   `json:"planner,omitempty"`
	Writer     RerunModel    `json:"writer"`
	Reviewer   RerunModel    `json:"reviewer"`
}
type RerunResult struct {
	Experiment Experiment `json:"experiment"`
	RunID      string     `json:"run_id"`
}

func (s *Service) rerunContext(ctx context.Context, runID string) (store.RemixLabExperimentRecord, store.RemixLabSlotRecord, Workflow, error) {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return store.RemixLabExperimentRecord{}, store.RemixLabSlotRecord{}, Workflow{}, err
	}
	if isManualDraft(run) || (run.Status != "completed" && run.Status != "failed") {
		return store.RemixLabExperimentRecord{}, store.RemixLabSlotRecord{}, Workflow{}, ErrRunNotRetryable
	}
	exp, slots, _, err := s.repo.GetExperiment(ctx, run.ExperimentID)
	if err != nil {
		return exp, store.RemixLabSlotRecord{}, Workflow{}, err
	}
	// A new round uses current account prompts; existing rounds keep their snapshots.
	wf, err := s.WorkflowForAccount(exp.ProduceAccountID)
	if err != nil {
		return exp, store.RemixLabSlotRecord{}, wf, err
	}
	for _, slot := range slots {
		if slot.ID == run.SlotID {
			return exp, slot, wf, nil
		}
	}
	return exp, store.RemixLabSlotRecord{}, wf, store.ErrRemixLabNotFound
}

func (s *Service) RerunOptions(ctx context.Context, runID string) (RerunInput, error) {
	_, slot, wf, err := s.rerunContext(ctx, runID)
	if err != nil {
		return RerunInput{}, err
	}
	in := RerunInput{Writer: RerunModel{slot.Model, slot.ReasoningEffort, slot.ServiceTier}}
	references := make([]RerunModel, 0)
	for _, node := range wf.Nodes {
		if node.Type == WorkflowNodeAgent && node.Config.Role == openaicompat.ReferenceDraftRole {
			references = append(references, RerunModel{node.Config.Model, node.Config.ReasoningEffort, node.Config.ServiceTier})
		}
	}
	in.References = &references
	if node := workflowWriter(wf); node != nil {
		if node.Config.Model != "" {
			in.Writer.Model = node.Config.Model
		}
		if node.Config.ReasoningEffort != "" {
			in.Writer.ReasoningEffort = node.Config.ReasoningEffort
		}
		if node.Config.ServiceTier != "" {
			in.Writer.ServiceTier = node.Config.ServiceTier
		}
	}
	for _, node := range wf.Nodes {
		if node.ID == "hook" && node.Type == WorkflowNodeAgent && node.Config.Role != openaicompat.ReferenceDraftRole {
			in.Planner = &RerunModel{node.Config.Model, node.Config.ReasoningEffort, node.Config.ServiceTier}
			if in.Planner.Model == "" {
				in.Planner.Model = in.Writer.Model
				if strings.EqualFold(strings.TrimSpace(node.Config.Channel), "search") {
					rt, runtimeErr := s.runtime.Runtime(ctx)
					if runtimeErr != nil {
						return in, runtimeErr
					}
					if rt.GrokBaseURL != "" && rt.GrokAPIKey != "" && rt.GrokModel != "" {
						in.Planner.Model = rt.GrokModel
					}
				}
			}
			if in.Planner.ReasoningEffort == "" {
				in.Planner.ReasoningEffort = in.Writer.ReasoningEffort
			}
			if in.Planner.ServiceTier == "" {
				in.Planner.ServiceTier = "default"
			}
			break
		}
	}
	if in.Planner != nil && len(references) == 0 {
		in.References = nil
	}
	if node := workflowReviewer(wf); node != nil {
		in.Reviewer = RerunModel{node.Config.Model, node.Config.ReasoningEffort, node.Config.ServiceTier}
	}
	if in.Reviewer.Model == "" || in.Reviewer.ReasoningEffort == "" {
		rt, err := s.runtime.Runtime(ctx)
		if err != nil {
			return in, err
		}
		model, effort := s.remixDefaults(ctx, rt, nil)
		if in.Reviewer.Model == "" {
			in.Reviewer.Model = model
		}
		if in.Reviewer.ReasoningEffort == "" {
			in.Reviewer.ReasoningEffort = effort
		}
	}
	if in.Writer.ServiceTier == "" {
		in.Writer.ServiceTier = "default"
	}
	if in.Reviewer.ServiceTier == "" {
		in.Reviewer.ServiceTier = "default"
	}
	return in, nil
}

func validateRerunModel(in *RerunModel) error {
	in.Model = strings.TrimSpace(in.Model)
	in.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	if in.Model == "" || len(in.Model) > 200 || strings.ContainsAny(in.Model, "\r\n") {
		return ErrInvalidRerun
	}
	switch in.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return ErrInvalidRerun
	}
	tier, err := openaicompat.NormalizeServiceTier(in.ServiceTier)
	if err != nil {
		return ErrInvalidRerun
	}
	in.ServiceTier = tier
	return nil
}

func (s *Service) Rerun(ctx context.Context, runID string, in RerunInput) (RerunResult, error) {
	if in.References == nil && in.Planner != nil {
		if err := validateRerunModel(in.Planner); err != nil {
			return RerunResult{}, err
		}
	}
	if err := validateRerunModel(&in.Writer); err != nil {
		return RerunResult{}, err
	}
	if err := validateRerunModel(&in.Reviewer); err != nil {
		return RerunResult{}, err
	}
	exp, slot, wf, err := s.rerunContext(ctx, runID)
	if err != nil {
		return RerunResult{}, err
	}
	if in.References != nil {
		wf, err = withReferenceModels(wf, *in.References)
		if err != nil {
			return RerunResult{}, err
		}
	}
	for i := range wf.Nodes {
		var choice *RerunModel
		switch wf.Nodes[i].Type {
		case WorkflowNodeWriter:
			choice = &in.Writer
		case WorkflowNodeReviewer:
			choice = &in.Reviewer
		case WorkflowNodeAgent:
			if in.References == nil && wf.Nodes[i].ID == "hook" && wf.Nodes[i].Config.Role != openaicompat.ReferenceDraftRole {
				choice = in.Planner
			}
		}
		if choice != nil {
			wf.Nodes[i].Config.Model = choice.Model
			wf.Nodes[i].Config.ReasoningEffort = choice.ReasoningEffort
			wf.Nodes[i].Config.ServiceTier = choice.ServiceTier
		}
	}
	if err = ValidateWorkflow(wf); err != nil {
		return RerunResult{}, err
	}
	wf, err = (Store{DataRoot: s.dataRoot}).freezeWorkflowWriter(wf)
	if err != nil {
		return RerunResult{}, err
	}
	raw, err := json.Marshal(wf)
	if err != nil {
		return RerunResult{}, err
	}
	promptID := ""
	if node := workflowWriter(wf); node != nil {
		promptID = node.Config.PromptID
	}
	st := Store{DataRoot: s.dataRoot}
	if promptID == "" {
		if active, ok, e := st.GetActive(); e == nil && ok {
			promptID = active.ID
		}
	}
	prompt, ok, err := st.GetPrompt(promptID)
	if err != nil {
		return RerunResult{}, err
	}
	if !ok {
		prompt = ResolvePrompt("elder_stable", "", "")
	}
	slot.ID = uuid.NewString()
	slot.Model = in.Writer.Model
	slot.ReasoningEffort = in.Writer.ReasoningEffort
	slot.ServiceTier = in.Writer.ServiceTier
	slot.Label = "重跑 · " + in.Writer.Model
	slot.RunCount = 1
	slot.Pipeline = ""
	run := store.RemixLabRunRecord{ID: uuid.NewString(), ExperimentID: exp.ID, SlotID: slot.ID, RunIndex: 1, Status: "queued", PromptID: prompt.ID, PromptStamp: prompt.Stamp, PromptName: prompt.Name}
	if err = s.repo.AppendRemixRun(ctx, slot, run, string(raw), s.now()); err != nil {
		return RerunResult{}, err
	}
	view, err := s.GetExperiment(ctx, exp.ID)
	go s.drive(exp.ID)
	return RerunResult{Experiment: view, RunID: run.ID}, err
}

func (s *Service) effectiveRunExperiment(ctx context.Context, exp store.RemixLabExperimentRecord, runID string) (store.RemixLabExperimentRecord, error) {
	raw, err := s.repo.GetRunWorkflowJSON(ctx, runID)
	if err != nil {
		return exp, err
	}
	if raw != "" {
		exp.WorkflowJSON = raw
		exp.ProduceAuto = false
	}
	return exp, nil
}
