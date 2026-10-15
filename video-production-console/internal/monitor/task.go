// Package monitor schedules keyword searches and ranks useful local-business references.
package monitor

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"video-production-console/internal/store"
)

var ErrClosed = errors.New("监控服务已关闭")
var ErrInvalid = errors.New("监控参数不正确")

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func DefaultTask() store.MonitorTask {
	return store.MonitorTask{IntervalMinutes: 30, Pages: 1, Enabled: true, ScoreEnabled: true, ScoreThreshold: 70, AutoCollect: true,
		SurgeMode: "rule", SurgeLikeDelta: 500, SurgeLikeRatio: 0.5, SurgeCommentDelta: 50, SurgeCommentRatio: 0.5, SurgeMinBase: 20}
}

func ValidateTask(t *store.MonitorTask) error {
	t.Name, t.Keyword, t.Model, t.ScoreFocus = strings.TrimSpace(t.Name), strings.TrimSpace(t.Keyword), strings.TrimSpace(t.Model), strings.TrimSpace(t.ScoreFocus)
	message := ""
	switch {
	case t.Platform != "douyin" && t.Platform != "xhs":
		message = "监控只支持抖音和小红书"
	case len([]rune(t.Keyword)) < 1 || len([]rune(t.Keyword)) > 40:
		message = "关键词需要 1–40 个字"
	case !utf8.ValidString(t.Keyword) || strings.ContainsRune(t.Keyword, utf8.RuneError) || !utf8.ValidString(t.Name) || !utf8.ValidString(t.ScoreFocus):
		message = "关键词或名称含有乱码，请重新输入"
	case t.IntervalMinutes < 30 || t.IntervalMinutes > 1440:
		message = "监控间隔需要在 30–1440 分钟之间"
	case t.Pages < 1 || t.Pages > 3:
		message = "搜索页数需要在 1–3 页之间"
	case t.ScoreThreshold < 0 || t.ScoreThreshold > 100:
		message = "分数阈值需要在 0–100 之间"
	case !validRatio(t.SurgeLikeRatio) || !validRatio(t.SurgeCommentRatio):
		message = "相对增幅需要在 0–10 之间"
	case t.SurgeLikeDelta < 0 || t.SurgeCommentDelta < 0 || t.SurgeMinBase < 0:
		message = "增量阈值和最低基数不能为负数"
	case t.SurgeMode != "rule" && t.SurgeMode != "ai" && t.SurgeMode != "both":
		message = "飙升模式需要是 rule、ai 或 both"
	case len(t.Model) > 256:
		message = "模型名称过长"
	}
	if message != "" {
		return &ValidationError{message}
	}
	if t.Name == "" {
		t.Name = t.Keyword
	}
	return nil
}
func validRatio(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 10 }

// TaskPatch uses pointers so false and zero remain meaningful partial updates.
type TaskPatch struct {
	Name              *string  `json:"name"`
	Platform          *string  `json:"platform"`
	Keyword           *string  `json:"keyword"`
	IntervalMinutes   *int     `json:"interval_minutes"`
	Pages             *int     `json:"pages"`
	Enabled           *bool    `json:"enabled"`
	Model             *string  `json:"model"`
	ScoreEnabled      *bool    `json:"score_enabled"`
	ScoreThreshold    *int     `json:"score_threshold"`
	ScoreFocus        *string  `json:"score_focus"`
	AutoCollect       *bool    `json:"auto_collect"`
	AutoTranscribe    *bool    `json:"auto_transcribe"`
	AutoAnalyze       *bool    `json:"auto_analyze"`
	AutoInsight       *bool    `json:"auto_insight"`
	SurgeMode         *string  `json:"surge_mode"`
	SurgeLikeDelta    *int64   `json:"surge_like_delta"`
	SurgeLikeRatio    *float64 `json:"surge_like_ratio"`
	SurgeCommentDelta *int64   `json:"surge_comment_delta"`
	SurgeCommentRatio *float64 `json:"surge_comment_ratio"`
	SurgeMinBase      *int64   `json:"surge_min_base"`
}

func assign[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}
func (p TaskPatch) Apply(t *store.MonitorTask) {
	assign(&t.Name, p.Name)
	assign(&t.Platform, p.Platform)
	assign(&t.Keyword, p.Keyword)
	assign(&t.IntervalMinutes, p.IntervalMinutes)
	assign(&t.Pages, p.Pages)
	assign(&t.Enabled, p.Enabled)
	assign(&t.Model, p.Model)
	assign(&t.ScoreEnabled, p.ScoreEnabled)
	assign(&t.ScoreThreshold, p.ScoreThreshold)
	assign(&t.ScoreFocus, p.ScoreFocus)
	assign(&t.AutoCollect, p.AutoCollect)
	assign(&t.AutoTranscribe, p.AutoTranscribe)
	assign(&t.AutoAnalyze, p.AutoAnalyze)
	assign(&t.AutoInsight, p.AutoInsight)
	assign(&t.SurgeMode, p.SurgeMode)
	assign(&t.SurgeLikeDelta, p.SurgeLikeDelta)
	assign(&t.SurgeLikeRatio, p.SurgeLikeRatio)
	assign(&t.SurgeCommentDelta, p.SurgeCommentDelta)
	assign(&t.SurgeCommentRatio, p.SurgeCommentRatio)
	assign(&t.SurgeMinBase, p.SurgeMinBase)
}
