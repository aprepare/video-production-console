package aishorts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"video-production-console/internal/agentruntime/openaicompat"
)

type effortCapture struct{ seen string }

func (c *effortCapture) Chat(req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	c.seen = req.ReasoningEffort
	var out openaicompat.ChatResponse
	json.Unmarshal([]byte(`{"choices":[{"message":{"content":"{\"shots\":[{\"narration\":\"一家人一起核对生活支出。\",\"subject\":\"夫妻\",\"scene\":\"夫妻一起核对生活支出\"}]}"}}]}`), &out)
	return out, nil
}
func TestStoryboardReasoningSavedAndSent(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	s, err := svc.CreateWithReasoning("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "", "documentary", "model", "", "", "high")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(s.ID)
	if err != nil || got.TextReasoningEffort != "high" {
		t.Fatal("effort persistence", err)
	}
	c := &effortCapture{}
	got.Story = "一家人一起核对生活支出。"
	if err := buildExplainerStoryboard(context.Background(), c, "model", "", got); err != nil {
		t.Fatal(err)
	}
	if c.seen != "high" {
		t.Fatal("effort was not sent", c.seen)
	}
	bad := "bad"
	if _, err := svc.UpdateTextWithReasoning(s.ID, "", "", "", "", nil, nil, nil, &bad); err == nil {
		t.Fatal("invalid effort accepted")
	}
}

type phaseAssembler struct {
	arrived chan struct{}
	resume  chan struct{}
	fail    bool
}

func (a phaseAssembler) Assemble(_ context.Context, _ Runtime, _ *Short, _ string, p func(string)) (AssembleResult, error) {
	p("导入剪映")
	close(a.arrived)
	<-a.resume
	if a.fail {
		return AssembleResult{}, errors.New("fixture import failed")
	}
	return AssembleResult{DraftPath: "fixture-draft", DraftName: "fixture"}, nil
}
func TestAssemblyProgressSuccessAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		a := phaseAssembler{make(chan struct{}), make(chan struct{}), fail}
		svc := NewService(t.TempDir(), func(context.Context) (Runtime, error) { return Runtime{}, nil }, a)
		s := &Short{ID: "phase", Mode: ModeExplainer, Status: StatusReady, Shots: []Shot{{ImageStatus: ShotDone, ImagePath: "fixture.png"}}}
		if err := svc.store.Save(s); err != nil {
			t.Fatal(err)
		}
		if err := svc.AssembleAsync(s.ID); err != nil {
			t.Fatal(err)
		}
		select {
		case <-a.arrived:
		case <-time.After(time.Second):
			t.Fatal("no phase")
		}
		got, err := svc.Get(s.ID)
		if err != nil || got.AssemblyProgress.Stage != 3 || got.Error != "" {
			t.Fatal("phase not persisted", err)
		}
		close(a.resume)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			got, _ = svc.Get(s.ID)
			if got.Status != StatusAssembling {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if fail {
			if got.Status != StatusFailed || got.Error == "" || got.AssemblyProgress.Stage != 3 {
				t.Fatal("failure state", got)
			}
		} else if got.Status != StatusAssembled || got.AssemblyProgress.Stage != 4 {
			t.Fatal("success state", got)
		}
	}
}
func TestAssemblySubprocessPhaseAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	message := ""
	w := &assemblyOutput{output: &out, notify: func(s string) { message = s }}
	w.Write([]byte("AI_SHORT_STA"))
	w.Write([]byte("GE:导入剪映\n"))
	if message != "导入剪映" {
		t.Fatal(message)
	}
}
