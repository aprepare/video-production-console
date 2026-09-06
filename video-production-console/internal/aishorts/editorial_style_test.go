package aishorts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"video-production-console/internal/agentruntime/openaicompat"
)

type editorialPlannerCapture struct{ system string }

func (c *editorialPlannerCapture) Chat(req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	c.system = req.Messages[0].Content
	content := `{"shots":[{"narration":"家庭积蓄先安排日常支出。","style_key":"paper_collage","scene":"三层纸片呈现生活开支","subject_type":"object"},{"narration":"剩下的钱分作应急与长期储备。","style_key":"miniature","scene":"两个不同高度的平台放储蓄罐与树苗","subject_type":"comparison"}]}`
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	var response openaicompat.ChatResponse
	err := json.Unmarshal(raw, &response)
	return response, err
}

func TestEditorialPlannerSelectsStyleBeforeScene(t *testing.T) {
	c := &editorialPlannerCapture{}
	short := &Short{Mode: ModeExplainer, Style: "finance_editorial", Story: "家庭积蓄先安排日常支出。剩下的钱分作应急与长期储备。"}
	if err := buildExplainerStoryboard(context.Background(), c, "fixture", "", short); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.system, "先判断本镜要传递的信息") || !strings.Contains(c.system, `"style_key"`) {
		t.Fatal("missing visual planning instructions")
	}
	if len(short.Shots) != 2 || short.Shots[0].StyleKey != "paper_collage" || short.Shots[1].StyleKey != "miniature" {
		t.Fatalf("planner choices lost: %+v", short.Shots)
	}
	if short.Shots[0].Narration+short.Shots[1].Narration != short.Story {
		t.Fatal("narration changed")
	}
	fallback := fallbackSplitWithHints(short.Story, short.Shots)
	if len(fallback) != 2 || fallback[1].StyleKey != "miniature" {
		t.Fatalf("fallback lost matched style: %+v", fallback)
	}
}

func TestEditorialDefaultAndLegacyCompatibility(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	short, err := svc.Create("", ModeExplainer, "", "家庭积蓄先安排日常支出，剩下的钱分作应急与长期储备。", "", "", "", "", "")
	if err != nil || short.Style != "finance_editorial" {
		t.Fatal("new default", short, err)
	}
	legacy := &Short{Mode: ModeExplainer, Shots: []Shot{{StyleKey: "miniature"}}}
	normalizeLegacy(legacy)
	if legacy.Style != "documentary" || legacy.Shots[0].StyleKey != "documentary" {
		t.Fatal("legacy default changed")
	}
	for _, style := range []string{"documentary", "warm_realism", "poster", "collage"} {
		old := &Short{Mode: ModeExplainer, Style: style, Shots: []Shot{{StyleKey: "miniature"}}}
		normalizeLegacy(old)
		if old.Shots[0].StyleKey != style {
			t.Fatal("single style changed", style)
		}
	}
}

func TestEditorialPromptsKeepMaterialAndNoText(t *testing.T) {
	for _, style := range []string{"paper_collage", "miniature"} {
		shot := Shot{StyleKey: style, Scene: "两位成年人讨论资金安排", SubjectType: "person"}
		prompt := explainerImagePrompt(shot)
		if strings.Contains(prompt, "自然皮肤") || !strings.Contains(prompt, explainerNoTextRule) {
			t.Fatal("incompatible prompt", prompt)
		}
		video := explainerVideoPrompt(shot)
		if !strings.Contains(video, "纸片或模型") {
			t.Fatal("video lacks material direction", video)
		}
	}
}

func TestEditorialStylesSurviveFinalizeAndReload(t *testing.T) {
	shots := []Shot{{Narration: "这部分钱留给日常支出。", StyleKey: "miniature"}, {Narration: "另一部分留作应急储备。", StyleKey: "paper_collage"}, {Narration: "先问清楚具体条件。", StyleKey: "invalid"}}
	finalizeExplainerShots(shots, "finance_editorial")
	svc := NewService(t.TempDir(), nil, nil)
	s := &Short{ID: "editorial", Mode: ModeExplainer, Style: "finance_editorial", Shots: shots}
	if err := svc.store.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"miniature", "paper_collage", "paper_collage"} {
		if got.Shots[i].StyleKey != want {
			t.Fatalf("shot %d: %s, want %s", i, got.Shots[i].StyleKey, want)
		}
	}
}

func TestEditorialShotOverrideOnlyInvalidatesItsImage(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	s := &Short{ID: "override", Mode: ModeExplainer, Style: "finance_editorial", Shots: []Shot{{Narration: "日常支出先留好。", Scene: "三个信封", StyleKey: "paper_collage", ImagePath: "one.png", ImageStatus: ShotDone}, {Narration: "还有一部分备用。", Scene: "储蓄罐", StyleKey: "paper_collage", ImagePath: "two.png", ImageStatus: ShotDone}}}
	fillAllPrompts(s, false)
	if err := svc.store.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UpdateShot(s.ID, 0, ShotPatch{StyleKey: "miniature"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Shots[0].StyleKey != "miniature" || !got.Shots[0].ImageStale || got.Shots[1].ImageStale {
		t.Fatalf("unexpected shots: %+v", got.Shots)
	}
	got, err = svc.UpdateShot(s.ID, 0, ShotPatch{Scene: "两个平台上的微缩储蓄罐"})
	if err != nil || got.Shots[0].StyleKey != "miniature" {
		t.Fatal("ordinary edit lost override", err)
	}
	got, err = svc.Get(s.ID)
	if err != nil || got.Shots[0].StyleKey != "miniature" {
		t.Fatal("override lost on read", err)
	}
	if _, err = svc.UpdateShot(s.ID, 0, ShotPatch{StyleKey: "documentary"}); err == nil {
		t.Fatal("invalid mixed style accepted")
	}
}
