package mediacatalog

import (
	"context"
	"errors"
	"testing"
)

func seedRecallShot(t *testing.T, repo *Repository, seed, kind, mood, setting string, tags []string, vector []float32) RecalledShot {
	t.Helper()
	ctx := context.Background()
	source := testVideoSource(seed)
	source.Kind = SourceKind(kind)
	if kind == string(SourceKindBroll) {
		source.RelativePath = "originals/broll/" + seed + ".mp4"
	}
	source.DurationMS = 120_000
	source.Status = SourceStatusReady
	stored, _, err := repo.UpsertSource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateSourceStatus(ctx, stored.ID, SourceStatusReady, ""); err != nil {
		t.Fatal(err)
	}
	shot, err := repo.InsertShot(ctx, Shot{
		SourceID: stored.ID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 12_000,
		AnalysisStatus: AnalysisCompleted, Summary: seed, Mood: mood, Setting: setting,
		MotionLevel: "low", AnalysisVersion: "test-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	tagRows := make([]Tag, 0, len(tags))
	for _, value := range tags {
		tagRows = append(tagRows, Tag{ShotID: shot.ID, Namespace: "vision", Value: value, Confidence: 0.9})
	}
	if err := repo.UpsertTags(ctx, shot.ID, tagRows); err != nil {
		t.Fatal(err)
	}
	if len(vector) > 0 {
		if err := repo.SetShotEmbedding(ctx, shot.ID, "test-emb", vector, "test-1"); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := repo.loadRecalledShot(ctx, shot.ID)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestRecallByTagsIsCaseInsensitiveAndCapped(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	bank := seedRecallShot(t, repo, "bank", "movie", "tense", "bank", []string{"银行柜台", "取款"}, []float32{1, 0})
	_ = seedRecallShot(t, repo, "door", "movie", "tense", "hallway", []string{"关门"}, []float32{0, 1})
	got, err := repo.RecallByTags(ctx, []string{"银行柜台", "BANK"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Shot.ID != bank.Shot.ID {
		t.Fatalf("got %#v", got)
	}
	if len(got[0].Embedding) != 2 {
		t.Fatalf("embedding missing: %#v", got[0].Embedding)
	}
}

func TestRecallByMoodSettingAndReadyPool(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	night := seedRecallShot(t, repo, "night", "broll", "anxious", "apartment", []string{"深夜独坐"}, []float32{0, 1})
	_ = seedRecallShot(t, repo, "traffic", "broll", "neutral", "city", []string{"城市交通"}, nil)
	got, err := repo.RecallByMoodSetting(ctx, "anxious", "", 50)
	if err != nil || len(got) != 1 || got[0].Shot.ID != night.Shot.ID {
		t.Fatalf("mood recall=%#v err=%v", got, err)
	}
	ready, err := repo.RecallReadyShots(ctx, 50)
	if err != nil || len(ready) != 2 {
		t.Fatalf("ready=%d err=%v", len(ready), err)
	}
}

func TestRecallNilRepositoryIsUnavailable(t *testing.T) {
	var repo *Repository
	_, err := repo.RecallReadyShots(context.Background(), 10)
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
