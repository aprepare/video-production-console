package montageplan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type CityLibraryClip struct {
	AssetID           string  `json:"asset_id"`
	Path              string  `json:"path"`
	Order             int     `json:"order"`
	Category          string  `json:"category"`
	Origin            string  `json:"origin"`
	SourceIn          float64 `json:"source_in"`
	SourceOut         float64 `json:"source_out"`
	Duration          float64 `json:"duration"`
	TimelineIn        float64 `json:"timeline_in"`
	TimelineOut       float64 `json:"timeline_out"`
	PriorUses         int     `json:"prior_uses"`
	PriorAccountCount int     `json:"prior_account_count"`
}

type citySelectionPolicy struct {
	StartTrim         float64  `json:"source_start_trim_seconds"`
	EndTrim           float64  `json:"source_end_trim_seconds"`
	OpeningUntil      float64  `json:"opening_categories_until_seconds"`
	OpeningCategories []string `json:"opening_categories"`
	RequireMotion     bool     `json:"require_opening_motion_review"`
	MotionUntil       float64  `json:"opening_motion_until_seconds"`
	OpeningExcluded   []string `json:"opening_excluded_asset_ids"`
}

type CityOpeningMotionEvidence struct {
	ProjectID            string    `json:"project_id"`
	AssetID              string    `json:"asset_id"`
	Path                 string    `json:"path"`
	SampleTimes          []float64 `json:"sample_times"`
	MeanPixelDifferences []float64 `json:"mean_pixel_differences"`
	Static               bool      `json:"static"`
	Error                string    `json:"error,omitempty"`
}

type CityOpeningMotionReview struct {
	Passed       bool                        `json:"passed"`
	CheckedUntil float64                     `json:"checked_until"`
	Evidence     []CityOpeningMotionEvidence `json:"evidence"`
}

// CityLibraryReservation is both the library.py-compatible plan and the task receipt.
type CityLibraryReservation struct {
	Version             int                     `json:"version"`
	ProjectID           string                  `json:"project_id"`
	Account             string                  `json:"account"`
	CatalogPath         string                  `json:"catalog_path"`
	Source              string                  `json:"source"`
	Duration            float64                 `json:"duration"`
	SelectionPolicy     citySelectionPolicy     `json:"selection_policy"`
	RandomSeed          int64                   `json:"random_seed"`
	Status              string                  `json:"status"`
	CreatedAt           string                  `json:"created_at"`
	Clips               []CityLibraryClip       `json:"clips"`
	OpeningMotionReview CityOpeningMotionReview `json:"opening_motion_review"`
	Warnings            []string                `json:"warnings,omitempty"`
}

func openCityCatalog(ctx context.Context, path string, readOnly bool) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("打开城市素材台账 %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("城市素材台账不是文件: %s", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	query := url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(30000)", "foreign_keys(ON)"}}
	if readOnly {
		query.Set("mode", "ro")
	}
	dsnPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(dsnPath, "/") {
		dsnPath = "/" + dsnPath
	}
	dsn := (&url.URL{Scheme: "file", Path: dsnPath, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func withCityTransaction(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) (resultErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(rollbackCtx, "ROLLBACK"); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if err = fn(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

type cityAsset struct {
	id, path, category, origin, status, family string
	duration, in, out                          float64
	width, height, uses, reserved, accounts    int
	accountUsed                                bool
}

func (a cityAsset) interval() (float64, float64) {
	return math.Max(a.in, 2), math.Min(a.out, a.duration-3)
}

func cityAssets(ctx context.Context, conn *sql.Conn, account string) ([]cityAsset, error) {
	rows, err := conn.QueryContext(ctx, `SELECT a.asset_id,a.path,a.category,COALESCE(a.origin,''),
 COALESCE(a.status,''),COALESCE(a.family,''),COALESCE(a.duration,0),COALESCE(a.usable_in,0),
 COALESCE(a.usable_out,0),COALESCE(a.width,0),COALESCE(a.height,0),
 (SELECT COUNT(*) FROM uses u WHERE u.asset_id=a.asset_id),
 (SELECT COUNT(*) FROM reservations r JOIN projects p USING(project_id) WHERE r.asset_id=a.asset_id AND p.status='reserved'),
 (SELECT COUNT(DISTINCT p.account) FROM uses u JOIN projects p USING(project_id) WHERE u.asset_id=a.asset_id),
 EXISTS(SELECT 1 FROM uses u JOIN projects p USING(project_id) WHERE u.asset_id=a.asset_id AND p.account=?)
 FROM assets a ORDER BY a.asset_id`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var assets []cityAsset
	for rows.Next() {
		var a cityAsset
		if err := rows.Scan(&a.id, &a.path, &a.category, &a.origin, &a.status, &a.family, &a.duration, &a.in, &a.out, &a.width, &a.height, &a.uses, &a.reserved, &a.accounts, &a.accountUsed); err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

func cityAccount(ctx context.Context, pc *planContext) string {
	if pc.manifest.Project == nil {
		return ""
	}
	id := strings.TrimSpace(pc.manifest.Project.AccountID)
	if id == "" || pc.manifest.NonSecretSettings.DataRoot == "" {
		return id
	}
	db, err := openCityCatalog(ctx, filepath.Join(pc.manifest.NonSecretSettings.DataRoot, "console.db"), true)
	if err != nil {
		return id
	}
	defer db.Close()
	var name string
	if err := db.QueryRowContext(ctx, "SELECT name FROM accounts WHERE id=?", id).Scan(&name); err == nil && strings.TrimSpace(name) != "" {
		return name
	}
	return id
}

func reserveCityLibrary(ctx context.Context, pc *planContext, p ScenicPolicy, projectID string, excluded []string) (*CityLibraryReservation, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if pc.duration <= 0 || math.IsInf(pc.duration, 0) || math.IsNaN(pc.duration) {
		return nil, fmt.Errorf("城市混剪需要有效的配音时长")
	}
	account := cityAccount(ctx, pc)
	if account == "" || pc.manifest.TaskID == "" {
		return nil, fmt.Errorf("城市混剪需要任务 ID 和所属账号")
	}
	catalogPath := cityCatalogPath(pc.mediaRoot)
	db, err := openCityCatalog(ctx, catalogPath, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	excluded = slices.Clone(excluded)
	slices.Sort(excluded)
	excluded = slices.Compact(excluded)
	if excluded == nil {
		excluded = []string{}
	}
	var plan *CityLibraryReservation
	err = withCityTransaction(ctx, db, func(conn *sql.Conn) error {
		var status, savedAccount string
		var savedJSON sql.NullString
		err := conn.QueryRowContext(ctx, "SELECT status,account,plan FROM projects WHERE project_id=?", projectID).Scan(&status, &savedAccount, &savedJSON)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		switch status {
		case "completed":
			return fmt.Errorf("该任务的素材已提交，请新建任务重出草稿")
		case "reserved":
			if err := json.Unmarshal([]byte(savedJSON.String), &plan); err != nil {
				return fmt.Errorf("读取已预占计划: %w", err)
			}
			if plan == nil || savedAccount != account || plan.ProjectID != projectID || math.Abs(plan.Duration-pc.duration) > .001 || !slices.Equal(plan.SelectionPolicy.OpeningExcluded, excluded) {
				return fmt.Errorf("已预占计划与当前账号、配音时长或开头排除名单不一致，请新建任务")
			}
			return checkCityReservationSets(ctx, conn, projectID, plan.Clips, plan.Clips)
		case "", "cancelled":
		default:
			return fmt.Errorf("城市素材项目状态 %q 不支持预占", status)
		}
		assets, err := cityAssets(ctx, conn, account)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(pc.manifest.TaskID))
		seed := int64(binary.BigEndian.Uint64(sum[:8]) & ((1 << 52) - 1))
		rng := rand.New(rand.NewSource(seed))
		plan = &CityLibraryReservation{Version: 2, ProjectID: projectID, Account: account, CatalogPath: catalogPath, Source: "video-production-console", Duration: pc.duration, RandomSeed: seed, Status: "reserved", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			SelectionPolicy: citySelectionPolicy{2, 3, 60, []string{"City_Aerial", "Housing_Residential"}, true, math.Min(10, pc.duration), excluded}}
		families := map[string]bool{}
		cursor := 0.0
		for cursor < pc.duration-1e-9 {
			if err := ctx.Err(); err != nil {
				return err
			}
			var tier []cityAsset
			minCount := 6
			pending, exhausted, occupied := 0, 0, 0
			busySeconds := 0.0
			busyFamilies := map[string]float64{}
			for _, a := range assets {
				if !cityCategory(a.category) || (len(p.AllowedCategories) > 0 && !slices.Contains(p.AllowedCategories, a.category)) {
					continue
				}
				if a.status == "pending" {
					pending++
				}
				if a.uses > 5 {
					exhausted++
				}
				lo, hi := a.interval()
				if a.status != "eligible" || hi <= lo || a.height > a.width || families[a.family] || (cursor < 60 && !cityOpeningCategory(a.category)) || (cursor < 10 && slices.Contains(excluded, a.id)) {
					continue
				}
				if info, err := os.Stat(a.path); err != nil || !info.Mode().IsRegular() {
					continue
				}
				count := a.uses + a.reserved
				if count > 5 {
					if a.uses <= 5 {
						occupied++
						busyFamilies[a.family] = math.Max(busyFamilies[a.family], math.Min(hi-lo, p.BodyMax))
					}
					continue
				}
				if count < minCount {
					minCount = count
					tier = nil
				}
				if count == minCount {
					tier = append(tier, a)
				}
			}
			if len(tier) == 0 {
				stage, required := "后段", pc.duration
				if cursor < 60 {
					stage, required = "开头一分钟", math.Min(60, pc.duration)
				}
				for _, seconds := range busyFamilies {
					busySeconds += seconds
				}
				suggestion := "补充素材（或先审核待审核素材）"
				if cursor+busySeconds >= required {
					suggestion = "等正在制作的项目完成，或释放其预占后重试"
				}
				return fmt.Errorf("城市素材不足：%s缺少 %.2f 秒；待审核 %d，已用满 %d，被预占 %d；建议%s（本次预占已回滚）", stage, required-cursor, pending, exhausted, occupied, suggestion)
			}
			prefer := func(keep func(cityAsset) bool) {
				var filtered []cityAsset
				for _, a := range tier {
					if keep(a) {
						filtered = append(filtered, a)
					}
				}
				if len(filtered) > 0 {
					tier = filtered
				}
			}
			prefer(func(a cityAsset) bool { return !a.accountUsed })
			if len(plan.Clips) > 0 {
				last := plan.Clips[len(plan.Clips)-1]
				prefer(func(a cityAsset) bool { return a.category != last.Category })
				prefer(func(a cityAsset) bool { return a.origin != last.Origin })
			}
			a := tier[rng.Intn(len(tier))]
			lo, hi := p.BodyMin, p.BodyMax
			if cursor < 30 {
				lo, hi = p.OpeningMin, p.OpeningMax
			}
			length := math.Round((lo+rng.Float64()*(hi-lo))*10) / 10
			sourceIn, sourceEnd := a.interval()
			length = math.Min(length, math.Min(sourceEnd-sourceIn, pc.duration-cursor))
			plan.Clips = append(plan.Clips, CityLibraryClip{AssetID: a.id, Path: a.path, Order: len(plan.Clips) + 1, Category: a.category, Origin: a.origin, SourceIn: sourceIn, SourceOut: sourceIn + length, Duration: length, TimelineIn: cursor, TimelineOut: cursor + length, PriorUses: a.uses, PriorAccountCount: a.accounts})
			cursor += length
			families[a.family] = true
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		// A cancelled row must become active before its reservations are restored.
		if _, err = conn.ExecContext(ctx, `INSERT INTO projects(project_id,account,status,seed,plan) VALUES(?,?,'reserved',?,?)
 ON CONFLICT(project_id) DO UPDATE SET account=excluded.account,status='reserved',seed=excluded.seed,plan=excluded.plan`, projectID, account, seed, string(raw)); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "DELETE FROM reservations WHERE project_id=?", projectID); err != nil {
			return err
		}
		for _, c := range plan.Clips {
			if _, err = conn.ExecContext(ctx, "INSERT INTO reservations VALUES(?,?)", projectID, c.AssetID); err != nil {
				return err
			}
		}
		return nil
	})
	return plan, err
}

func validateCityCatalog(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	db, err := openCityCatalog(ctx, path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	var eligible int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assets WHERE status='eligible'").Scan(&eligible); err != nil {
		return fmt.Errorf("城市素材台账无法读取: %w", err)
	}
	if eligible == 0 {
		return fmt.Errorf("城市素材台账没有 eligible 素材，请先审核或补充素材")
	}
	return nil
}
