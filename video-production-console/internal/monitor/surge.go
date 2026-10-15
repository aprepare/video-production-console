package monitor

import (
	"fmt"
	"time"

	"video-production-console/internal/store"
)

type surgeDecision struct {
	Hit                     bool
	Reason                  string
	LikeDelta, CommentDelta int64
	Rate                    float64
	Minutes                 float64
}

func evaluateSurge(task store.MonitorTask, current store.MonitorItem, previous *store.MonitorItem, now time.Time) surgeDecision {
	if previous == nil {
		return surgeDecision{}
	}
	priorTime, err := time.Parse(time.RFC3339Nano, previous.LastSeenAt)
	minutes := now.Sub(priorTime).Minutes()
	if err != nil || minutes < 1 {
		minutes = 1
	}
	d := surgeDecision{LikeDelta: current.Likes - previous.Likes, CommentDelta: current.Comments - previous.Comments, Minutes: minutes}
	d.Rate = float64(d.LikeDelta) / (minutes / 60)
	commentBase := max(int64(5), task.SurgeMinBase/10)
	likeThreshold := task.SurgeLikeDelta
	if task.Platform == "xhs" && previous.Likes >= 10000 {
		likeThreshold = max(likeThreshold, 2000)
	}
	absolute := (task.SurgeLikeDelta > 0 && d.LikeDelta >= likeThreshold) ||
		(task.SurgeCommentDelta > 0 && d.CommentDelta >= task.SurgeCommentDelta)
	relative := (d.LikeDelta > 0 && task.SurgeLikeRatio > 0 && previous.Likes >= task.SurgeMinBase && float64(d.LikeDelta) >= float64(previous.Likes)*task.SurgeLikeRatio) ||
		(d.CommentDelta > 0 && task.SurgeCommentRatio > 0 && previous.Comments >= commentBase && float64(d.CommentDelta) >= float64(previous.Comments)*task.SurgeCommentRatio)
	if task.IntervalMinutes > 0 && minutes > 2*float64(task.IntervalMinutes) {
		absolute = false
		relative = relative && d.Rate >= float64(task.SurgeLikeDelta)/(float64(task.IntervalMinutes)/60)
	}
	d.Hit = absolute || relative
	if d.Hit {
		ratio := ""
		if previous.Likes > 0 {
			ratio = fmt.Sprintf("（%+.0f%%）", 100*float64(d.LikeDelta)/float64(previous.Likes))
		}
		d.Reason = fmt.Sprintf("%.0f 分钟点赞 %+d%s，评论 %+d", minutes, d.LikeDelta, ratio, d.CommentDelta)
	}
	return d
}

// The last hit's snapshot timestamp persists across restarts. One miss keeps
// that timestamp, so the following miss can clear the flag without extra schema.
func applySurge(current *store.MonitorItem, previous *store.MonitorItem, d surgeDecision, now time.Time) {
	current.LikeDelta, current.CommentDelta, current.LikeRatePerHour = d.LikeDelta, d.CommentDelta, d.Rate
	switch {
	case d.Hit:
		current.Surge = true
		current.SurgeReason = d.Reason
		current.SurgeAt = current.LastSeenAt
		if current.SurgeAt == "" {
			current.SurgeAt = stamp(now)
		}
		current.Read = false
	case previous != nil && previous.Surge && previous.SurgeAt == previous.LastSeenAt:
		current.Surge = true
		current.SurgeReason = previous.SurgeReason
		current.SurgeAt = previous.SurgeAt
	default:
		current.Surge = false
		current.SurgeReason = ""
		current.SurgeAt = ""
	}
}
