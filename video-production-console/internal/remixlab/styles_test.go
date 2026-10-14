package remixlab

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
)

func TestBuiltinStylesHaveStableIDsAndFinancePrompts(t *testing.T) {
	styles := BuiltinStyles()
	wantIDs := []string{"finance_course", "universal_remix", "local_store", "product_seller", "knowledge", "boss_story", "light_wash", "finance_v2"}
	if len(styles) != len(wantIDs) {
		t.Fatalf("builtin count = %d, want %d", len(styles), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		got := styles[i]
		if got.ID != wantID || !got.Builtin {
			t.Fatalf("builtin[%d] = %#v, want id %q and builtin", i, got, wantID)
		}
		if got.Writer == "" || got.WriterUser == "" || (got.ID != "finance_v2" && got.Reviewer == "") {
			t.Fatalf("builtin %q has an empty required prompt", got.ID)
		}
		if !got.Finance {
			for _, token := range []string{"{{SOURCE}}", "{{NOTES}}", "{{BRIEF}}"} {
				if !strings.Contains(got.WriterUser, token) {
					t.Fatalf("builtin %q writer user missing %s", got.ID, token)
				}
			}
		}
	}
	finance := styles[0]
	if !finance.Finance || finance.Planner != openaicompat.PlannerSystemPrompt ||
		finance.Writer != openaicompat.FinanceWriterRole() || finance.WriterUser != openaicompat.BoneFleshUserPrompt ||
		finance.Reviewer != openaicompat.FinanceReviewerRole() || finance.Rules != openaicompat.SharedEditorialPolicy {
		t.Fatal("finance style did not preserve the existing prompts byte-for-byte")
	}
}

func TestStyleCRUDPersistsAndKeepsBuiltinsFirst(t *testing.T) {
	root := t.TempDir()
	store := Store{DataRoot: root}
	created, err := store.UpsertStyle(StyleTemplate{
		Name: " 门店口播 ", Category: " 本地生活 ", Writer: " 写门店事实 ",
		WriterUser: "{{SOURCE}}", BestFor: []string{" 探店 ", " 团购 "}, Finance: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "custom-") || created.Builtin || !created.Finance || created.Name != "门店口播" {
		t.Fatalf("created style = %#v", created)
	}
	if got, ok, err := (Store{DataRoot: root}).GetStyle(created.ID); err != nil || !ok || got.Writer != "写门店事实" {
		t.Fatalf("persisted style = %#v, ok=%v, err=%v", got, ok, err)
	}

	created.Description = "更新后"
	created.Finance = false
	updated, err := store.UpsertStyle(created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Description != "更新后" || updated.Finance {
		t.Fatalf("updated style = %#v", updated)
	}
	list, err := store.ListStyles()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(BuiltinStyles())+1 || list[len(list)-1].ID != created.ID {
		t.Fatalf("list order/content = %#v", list)
	}
	if err := store.DeleteStyle(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetStyle(created.ID); err != nil || ok {
		t.Fatalf("deleted style still exists: ok=%v err=%v", ok, err)
	}
	if err := store.DeleteStyle(created.ID); !errors.Is(err, ErrStyleNotFound) {
		t.Fatalf("deleting missing style error = %v", err)
	}
}

func TestEditingBuiltinCreatesCustomDerivedStyle(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	builtin, ok, err := store.GetStyle("local_store")
	if err != nil || !ok {
		t.Fatalf("get builtin: ok=%v err=%v", ok, err)
	}
	builtin.Writer = "自定义门店写手"
	builtin.Finance = true
	derived, err := store.UpsertStyle(builtin)
	if err != nil {
		t.Fatal(err)
	}
	if derived.ID == builtin.ID || !strings.HasPrefix(derived.ID, "custom-") || derived.BaseID != builtin.ID || derived.Builtin || !derived.Finance {
		t.Fatalf("derived style = %#v", derived)
	}
	unchanged, ok, err := store.GetStyle("local_store")
	if err != nil || !ok || unchanged.Writer == derived.Writer {
		t.Fatalf("builtin changed: %#v, ok=%v, err=%v", unchanged, ok, err)
	}
	if err := store.DeleteStyle("local_store"); !errors.Is(err, ErrBuiltinStyle) {
		t.Fatalf("deleting builtin error = %v", err)
	}
	if err := store.DeleteStyle(derived.ID); err != nil {
		t.Fatalf("deleting derived style: %v", err)
	}
}

func TestUpsertStyleValidatesRequiredAndRuneLimits(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	tests := []StyleTemplate{
		{Writer: "writer"},
		{Name: "name"},
		{Name: "name", Writer: "writer", Description: strings.Repeat("界", 50001)},
		{Name: "name", Writer: "writer", BestFor: []string{strings.Repeat("界", 30000), strings.Repeat("文", 20001)}},
	}
	for i, input := range tests {
		if _, err := store.UpsertStyle(input); !errors.Is(err, ErrInvalidStyle) {
			t.Fatalf("case %d error = %v, want ErrInvalidStyle", i, err)
		}
	}
	if _, err := store.UpsertStyle(StyleTemplate{Name: "name", Writer: strings.Repeat("界", 50000)}); err != nil {
		t.Fatalf("50000-rune boundary rejected: %v", err)
	}
}

func TestConcurrentStyleCreatesDoNotLoseUpdates(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	const count = 24
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.UpsertStyle(StyleTemplate{Name: fmt.Sprintf("style-%02d", i), Writer: "writer"})
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	styles, err := store.ListStyles()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(styles) - len(BuiltinStyles()); got != count {
		t.Fatalf("custom style count = %d, want %d", got, count)
	}
}

func TestUniversalPublishingContractContainsOperationalRequirements(t *testing.T) {
	for _, want := range []string{"continuous_script", "titles=[]", "short_titles", "6–20", "descriptions", "≤ 60", "topics", "3–5", "cta", "禁用词", "广告法", "食品不宣称功效"} {
		if !strings.Contains(UniversalPublishingContract, want) {
			t.Fatalf("publishing contract missing %q", want)
		}
	}
}
