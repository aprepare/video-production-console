package montageplan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type testScenicSelector struct {
	Calls atomic.Int32
	Bad   bool
}

func (s *testScenicSelector) SelectShots(_ context.Context, jobs []ShotSelectJob) ([]ShotSelectPick, error) {
	s.Calls.Add(1)
	out := []ShotSelectPick{}
	for _, j := range jobs {
		id := j.Candidates[len(j.Candidates)-1].ShotID
		if s.Bad {
			id = "../../not-a-candidate"
		}
		out = append(out, ShotSelectPick{IntentID: j.Intent.SegmentID, ShotID: id, Why: "候选场景与文案相关"})
	}
	return out, nil
}
func (s *testScenicSelector) ModelIdentity() string { return "unit-model" }
func TestScenicModelActuallySelectsAndCaches(t *testing.T) {
	opts := scenicTestOptions(t)
	model := &testScenicSelector{}
	opts.ShotSelector = model
	if err := BuildV2(opts); err != nil {
		t.Fatal(err)
	}
	var qc ScenicSelectionQC
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(opts.PlanPath), "scenic_selection_qc.json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &qc) != nil || qc.ModelSelectedShots == 0 || qc.RepeatCount != 0 {
		t.Fatalf("bad selection QC: %+v", qc)
	}
	first, _ := os.ReadFile(opts.PlanPath)
	n := model.Calls.Load()
	if err := BuildV2(opts); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(opts.PlanPath)
	if model.Calls.Load() != n {
		t.Fatal("same task re-called provider")
	}
	if string(first) != string(second) {
		t.Fatal("cached replay changed approved plan")
	}
}
func TestScenicRejectsCandidateInjection(t *testing.T) {
	opts := scenicTestOptions(t)
	opts.ShotSelector = &testScenicSelector{Bad: true}
	if err := BuildV2(opts); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(opts.PlanPath)
	if strings.Contains(string(raw), "not-a-candidate") {
		t.Fatal("untrusted path accepted")
	}
	var qc ScenicSelectionQC
	raw, _ = os.ReadFile(filepath.Join(filepath.Dir(opts.PlanPath), "scenic_selection_qc.json"))
	json.Unmarshal(raw, &qc)
	if qc.ModelSelectedShots != 0 {
		t.Fatal("invented model selection counted")
	}
}
func TestEditorDefaultPacingAndFirstTransition(t *testing.T) {
	p := defaultScenicPolicy()
	if p.OpeningMin < 6 || p.BodyMin < 6 {
		t.Fatal("opening is too fast")
	}
	opts := scenicTestOptions(t)
	if err := BuildV2(opts); err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	raw, _ := os.ReadFile(opts.PlanPath)
	json.Unmarshal(raw, &plan)
	if plan.Editorial == nil || plan.Editorial.TransitionAttachment != "outgoing" {
		t.Fatal("missing versioned editor contract")
	}
	if len(plan.Timeline) > 1 && plan.Timeline[1].Transition == nil {
		t.Fatal("first boundary must demonstrate a transition")
	}
}
func TestEnglishWordsCannotMisclassifyPresidentialPalace(t *testing.T) {
	if containsScenicVisual("presidential palace", "residential") {
		t.Fatal("substring is not a home")
	}
}
