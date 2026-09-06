package remixlab

import (
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
)

func TestListLibraryDefaultsToCatalog(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	list, err := store.ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(Catalog()) {
		t.Fatalf("len=%d want %d", len(list), len(Catalog()))
	}
	if list[0].ID != "elder_stable" || !strings.Contains(list[0].System, openaicompat.SharedEditorialPolicy) {
		t.Fatalf("elder_stable=%+v", list[0])
	}
	if list[0].Stamp != openaicompat.RewritePromptStampStable {
		t.Fatalf("stamp=%q", list[0].Stamp)
	}
}

func TestUpsertAndDeleteCustomPrompt(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	saved, err := store.UpsertPrompt(PromptTemplate{
		Name:   "试验稿",
		System: "自定义系统提示",
		User:   "自定义用户 {{SOURCE}}",
		Stamp:  "试验稿 stamp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(saved.ID, "custom-") {
		t.Fatalf("id=%q", saved.ID)
	}
	list, err := store.ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		if p.ID == saved.ID {
			found = true
			if p.System != "自定义系统提示" || p.Builtin {
				t.Fatalf("%+v", p)
			}
		}
	}
	if !found {
		t.Fatal("custom prompt missing from library")
	}
	if err := store.DeletePrompt(saved.ID); err != nil {
		t.Fatal(err)
	}
	_, ok, err := store.GetPrompt(saved.ID)
	if err != nil || ok {
		t.Fatalf("deleted prompt still present ok=%v err=%v", ok, err)
	}
}

func TestDeleteBuiltinHidesFromLibrary(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	if err := store.DeletePrompt("wash"); err != nil {
		t.Fatal(err)
	}
	_, ok, err := store.GetPrompt("wash")
	if err != nil || ok {
		t.Fatalf("wash should be hidden ok=%v err=%v", ok, err)
	}
	list, err := store.ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.ID == "wash" {
			t.Fatal("wash still listed")
		}
	}
}

func TestAdoptPromptWritesActiveFile(t *testing.T) {
	store := Store{DataRoot: t.TempDir()}
	active, err := store.AdoptPrompt("bone_flesh")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "bone_flesh" || strings.TrimSpace(active.System) == "" || active.Style != "rewrite" {
		t.Fatalf("%+v", active)
	}
	got, ok, err := store.GetActive()
	if err != nil || !ok || got.ID != "bone_flesh" {
		t.Fatalf("active=%+v ok=%v err=%v", got, ok, err)
	}
	cleared, err := store.AdoptPrompt("elder_stable")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(cleared.System) != "" {
		t.Fatalf("elder_stable should clear compiled path: %+v", cleared)
	}
	_, ok, err = store.GetActive()
	if err != nil || ok {
		t.Fatalf("compiled default should have no active file ok=%v err=%v", ok, err)
	}
}
