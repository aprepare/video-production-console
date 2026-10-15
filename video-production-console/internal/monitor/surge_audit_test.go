package monitor

import (
	"testing"
	"time"

	"video-production-console/internal/store"
)

func TestSurgeRejectsRoundedStaleAndZeroGrowth(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, platform                 string
		previousLikes, likes, comments int64
		minutes                        int
		delta                          int64
		ratio                          float64
		want                           bool
	}{
		{"xhs rounded one thousand", "xhs", 10000, 11000, 0, 30, 500, .5, false},
		{"xhs two thousand", "xhs", 10000, 12000, 0, 30, 500, .5, true},
		{"xhs configured higher delta", "xhs", 10000, 12000, 0, 30, 3000, .5, false},
		{"xhs relative still valid", "xhs", 10000, 11000, 0, 30, 500, .1, true},
		{"xhs below rounded threshold", "xhs", 9000, 9500, 0, 30, 500, .5, true},
		{"douyin exact count", "douyin", 10000, 10500, 0, 30, 500, .5, true},
		{"zero likes positive comment below threshold", "douyin", 0, 0, 1, 30, 500, .5, false},
		{"long gap absolute only", "douyin", 10000, 11000, 0, 90, 500, .5, false},
		{"long gap relative too slow", "douyin", 1000, 1500, 0, 90, 500, .5, false},
		{"long gap relative fast enough", "douyin", 1000, 2500, 0, 90, 500, .5, true},
		{"exact two intervals retains absolute", "douyin", 10000, 10500, 0, 60, 500, .5, true},
		{"long gap comment absolute only", "douyin", 1000, 1000, 50, 90, 500, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := DefaultTask()
			task.Platform, task.SurgeLikeDelta, task.SurgeLikeRatio, task.SurgeMinBase = tc.platform, tc.delta, tc.ratio, 0
			previous := store.MonitorItem{Likes: tc.previousLikes, LastSeenAt: stamp(now.Add(-time.Duration(tc.minutes) * time.Minute))}
			current := store.MonitorItem{Platform: tc.platform, Likes: tc.likes, Comments: tc.comments}
			d := evaluateSurge(task, current, &previous, now)
			if d.Hit != tc.want {
				t.Fatalf("decision=%+v want hit=%v", d, tc.want)
			}
		})
	}
}
