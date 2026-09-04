package remixlab

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"video-production-console/internal/store"
)

// 已发布文案库：每条出过剪映草稿的成稿都在这里，操作员手填播放/点赞/出单，
// 复盘哪种开头、课尾真正带来转化。

type PublishedMetrics struct {
	Views     int       `json:"views"`
	Likes     int       `json:"likes"`
	Orders    int       `json:"orders"`
	Notes     string    `json:"notes"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PublishedScript struct {
	RunID        string            `json:"run_id"`
	ExperimentID string            `json:"experiment_id"`
	AccountID    string            `json:"account_id"`
	ProjectID    string            `json:"project_id"`
	Title        string            `json:"title"`         // 实验标题（原文首句）
	BoardTitle   string            `json:"board_title"`   // 发布包板标题
	Model        string            `json:"model"`
	PromptStamp  string            `json:"prompt_stamp"`
	Script       string            `json:"script"`
	ScriptRunes  int               `json:"script_runes"`
	Opening      string            `json:"opening"` // 前 120 字
	Ending       string            `json:"ending"`  // 后 300 字（课尾）
	ProducedAt   time.Time         `json:"produced_at"`
	Metrics      *PublishedMetrics `json:"metrics,omitempty"`
}

// ListPublished 返回文案库全部条目，最新在前。
func (s *Service) ListPublished(ctx context.Context) ([]PublishedScript, error) {
	rows, err := s.repo.ListPublishedRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PublishedScript, 0, len(rows))
	for _, row := range rows {
		script := strings.TrimSpace(row.Run.ContinuousScript)
		runes := []rune(script)
		item := PublishedScript{
			RunID: row.Run.ID, ExperimentID: row.Experiment.ID,
			AccountID:   firstNonBlank(row.AccountID, row.Experiment.ProduceAccountID),
			ProjectID:   row.ProjectID,
			Title:       row.Experiment.Title,
			BoardTitle:  boardTitleFromPackage(row.Run.PackageJSON),
			Model:       row.SlotModel,
			PromptStamp: row.Run.PromptStamp,
			Script:      script,
			ScriptRunes: len(runes),
			ProducedAt:  row.ProducedAt,
		}
		if len(runes) > 120 {
			item.Opening = string(runes[:120])
		} else {
			item.Opening = script
		}
		if len(runes) > 300 {
			item.Ending = string(runes[len(runes)-300:])
		} else {
			item.Ending = script
		}
		if row.Metrics != nil {
			item.Metrics = &PublishedMetrics{
				Views: row.Metrics.Views, Likes: row.Metrics.Likes, Orders: row.Metrics.Orders,
				Notes: row.Metrics.Notes, UpdatedAt: row.Metrics.UpdatedAt,
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// SavePublishedMetrics 保存手填成绩；负数按 0 记。
func (s *Service) SavePublishedMetrics(ctx context.Context, runID string, m PublishedMetrics) error {
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		return v
	}
	return s.repo.UpsertPublishMetrics(ctx, store.RemixLabPublishMetrics{
		RunID: strings.TrimSpace(runID), Views: clamp(m.Views), Likes: clamp(m.Likes), Orders: clamp(m.Orders),
		Notes: strings.TrimSpace(m.Notes), UpdatedAt: s.now(),
	})
}

func boardTitleFromPackage(raw string) string {
	var pkg struct {
		ShortTitles []string `json:"short_titles"`
		Titles      []string `json:"titles"`
	}
	if json.Unmarshal([]byte(raw), &pkg) != nil {
		return ""
	}
	if len(pkg.ShortTitles) > 0 {
		return pkg.ShortTitles[0]
	}
	if len(pkg.Titles) > 0 {
		return pkg.Titles[0]
	}
	return ""
}
