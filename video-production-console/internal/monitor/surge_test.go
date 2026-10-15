package monitor

import (
	"strings"
	"testing"
	"time"

	"video-production-console/internal/store"
)

func TestSurgeRulesAndGraceRound(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 30, 0, 0, time.UTC)
	task := DefaultTask()
	previous := store.MonitorItem{Likes: 2000, Comments: 100, LastSeenAt: stamp(now.Add(-30 * time.Minute))}
	for _, tc := range []struct {
		name            string
		likes, comments int64
		change          func(*store.MonitorTask)
		want            bool
	}{
		{"absolute likes", 2500, 100, nil, true},
		{"relative likes", 3000, 100, func(t *store.MonitorTask) { t.SurgeLikeDelta = 0 }, true},
		{"absolute comments", 2000, 150, nil, true},
		{"relative comments", 2000, 150, func(t *store.MonitorTask) { t.SurgeCommentDelta = 0 }, true},
		{"no hit", 2010, 101, nil, false},
		{"disabled thresholds", 9000, 900, func(t *store.MonitorTask) {
			t.SurgeLikeDelta = 0
			t.SurgeLikeRatio = 0
			t.SurgeCommentDelta = 0
			t.SurgeCommentRatio = 0
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := task
			if tc.change != nil {
				tc.change(&cfg)
			}
			current := store.MonitorItem{Likes: tc.likes, Comments: tc.comments}
			d := evaluateSurge(cfg, current, &previous, now)
			if d.Hit != tc.want {
				t.Fatalf("hit=%v want %v", d.Hit, tc.want)
			}
			if d.LikeDelta != tc.likes-2000 || d.CommentDelta != tc.comments-100 || d.Rate != float64(tc.likes-2000)*2 {
				t.Fatalf("bad deltas: %+v", d)
			}
			if d.Hit && !strings.Contains(d.Reason, "30 分钟点赞") {
				t.Fatalf("reason=%s", d.Reason)
			}
		})
	}
	low := store.MonitorItem{Likes: 1, Comments: 1, LastSeenAt: stamp(now)}
	if d := evaluateSurge(task, store.MonitorItem{Likes: 3, Comments: 3}, &low, now); d.Hit || d.Rate != 120 {
		t.Fatalf("min base / one minute floor: %+v", d)
	}
	if d := evaluateSurge(task, store.MonitorItem{Likes: 100000, Comments: 10000}, nil, now); d.Hit || d.LikeDelta != 0 {
		t.Fatalf("first sighting must not surge: %+v", d)
	}
	previous.Surge = true
	previous.SurgeAt = previous.LastSeenAt
	current := store.MonitorItem{Likes: previous.Likes, Comments: previous.Comments}
	d := evaluateSurge(task, current, &previous, now)
	applySurge(&current, &previous, d, now)
	if !current.Surge || current.SurgeAt != previous.SurgeAt {
		t.Fatalf("first miss should retain: %+v", current)
	}
	current.LastSeenAt = stamp(now)
	next := current
	now = now.Add(30 * time.Minute)
	d = evaluateSurge(task, next, &current, now)
	applySurge(&next, &current, d, now)
	if next.Surge {
		t.Fatal("second miss should clear")
	}
}

func TestMonitorValidationAndDefaults(t *testing.T) {
	task := DefaultTask()
	task.Platform, task.Keyword = "xhs", "岚山海鲜"
	if err := ValidateTask(&task); err != nil || task.Name != task.Keyword {
		t.Fatalf("default valid task: %+v %v", task, err)
	}
	for _, change := range []func(*store.MonitorTask){
		func(t *store.MonitorTask) { t.Platform = "channels" },
		func(t *store.MonitorTask) { t.Keyword = strings.Repeat("字", 41) },
		func(t *store.MonitorTask) { t.IntervalMinutes = 29 },
		func(t *store.MonitorTask) { t.Pages = 4 },
		func(t *store.MonitorTask) { t.ScoreThreshold = 101 },
		func(t *store.MonitorTask) { t.SurgeLikeRatio = 11 },
		func(t *store.MonitorTask) { t.SurgeCommentDelta = -1 },
		func(t *store.MonitorTask) { t.SurgeMode = "unknown" },
	} {
		invalid := task
		change(&invalid)
		if ValidateTask(&invalid) == nil {
			t.Fatalf("accepted invalid task %+v", invalid)
		}
	}
}
