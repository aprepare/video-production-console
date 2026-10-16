package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReferenceAnalysesMigrationAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version IN (42, 43)`); got != "2" {
		t.Fatalf("原片缓存与拆解分析迁移未全部登记：%s", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	defer db.Close()
	if !tableExists(t, db, "reference_analyses") {
		t.Fatal("拆解分析表未创建")
	}
}

func TestReferenceAnalysisRepositoryLifecycleAndDeduplication(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	refs := NewReferenceRepository(db)
	if _, err := refs.Upsert(ctx, ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "ref"}); err != nil {
		t.Fatal(err)
	}
	repo := NewReferenceAnalysisRepository(db)
	var wg sync.WaitGroup
	ids := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, _, err := repo.CreateQueued(ctx, ReferenceAnalysis{ID: fmt.Sprint("job-", i), ReferenceID: "ref", Model: "model", Product: "蓝色礼盒海鲜"})
			if err != nil {
				t.Errorf("创建失败: %v", err)
				return
			}
			ids <- a.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("重复启动创建了多个活动分析")
		}
		id = got
	}
	if err := repo.MarkRunning(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFrames(ctx, id, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, id, `{"summary":"完整报告"}`); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil || got.Status != "completed" || got.FrameCount != 2 || got.ReportJSON != `{"summary":"完整报告"}` || got.Product != "蓝色礼盒海鲜" {
		t.Fatalf("完成状态: %+v %v", got, err)
	}
	if err := repo.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, id)
	if got.Status != "completed" {
		t.Fatal("恢复覆盖了已完成记录")
	}
	if err := refs.Delete(ctx, "ref"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, id); !errors.Is(err, ErrReferenceAnalysisNotFound) {
		t.Fatalf("没有级联删除: %v", err)
	}
}

func TestReferenceAnalysisListLimitAndInterruptedStates(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	refs := NewReferenceRepository(db)
	for _, id := range []string{"ref", "other"} {
		if _, err := refs.Upsert(ctx, ReferencePost{ID: id, Platform: "xhs", ExternalID: id}); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewReferenceAnalysisRepository(db)
	for i := 0; i < 22; i++ {
		id := fmt.Sprintf("job-%02d", i)
		if _, _, err := repo.CreateQueued(ctx, ReferenceAnalysis{ID: id, ReferenceID: "ref", CreatedAt: time.Unix(int64(i), 0).UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := repo.Fail(ctx, id, "失败"); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.ListByReference(ctx, "ref")
	if err != nil || len(list) != 20 || list[0].ID != "job-21" || list[19].ID != "job-02" {
		t.Fatalf("排序或上限: %+v %v", list, err)
	}
	for _, id := range []string{"ref", "other"} {
		if _, _, err := repo.CreateQueued(ctx, ReferenceAnalysis{ID: id, ReferenceID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.MarkRunning(ctx, "ref"); err != nil {
		t.Fatal(err)
	}
	if err := repo.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ref", "other"} {
		got, err := repo.Get(ctx, id)
		if err != nil || got.Status != "failed" || got.Error != "控制台重启，分析中断，请重新分析" {
			t.Fatalf("恢复状态: %+v %v", got, err)
		}
		if err := repo.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Delete(ctx, "ref"); !errors.Is(err, ErrReferenceAnalysisNotFound) {
		t.Fatalf("重复删除: %v", err)
	}
}
