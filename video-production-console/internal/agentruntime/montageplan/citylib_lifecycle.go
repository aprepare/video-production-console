package montageplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

func cityClipIDs(clips []CityLibraryClip) ([]string, error) {
	ids := make([]string, 0, len(clips))
	for _, c := range clips {
		if c.AssetID == "" {
			return nil, fmt.Errorf("预占计划包含空 asset_id")
		}
		ids = append(ids, c.AssetID)
	}
	slices.Sort(ids)
	if len(ids) == 0 || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, fmt.Errorf("预占计划没有素材或包含重复 asset_id")
	}
	return ids, nil
}

func checkCityReservationSets(ctx context.Context, conn *sql.Conn, projectID string, stored, receipt []CityLibraryClip) error {
	want, err := cityClipIDs(stored)
	if err != nil {
		return err
	}
	got, err := cityClipIDs(receipt)
	if err != nil {
		return err
	}
	if !slices.Equal(want, got) {
		return fmt.Errorf("预占计划与任务回执的 asset_id 集合不一致")
	}
	rows, err := conn.QueryContext(ctx, "SELECT asset_id FROM reservations WHERE project_id=? ORDER BY asset_id", projectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		actual = append(actual, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !slices.Equal(want, actual) {
		return fmt.Errorf("reservations 与计划 clips 的 asset_id 集合不一致，拒绝提交")
	}
	return nil
}

// FinalizeCityLibraryReservation is a no-op for tasks without a city receipt.
// Callers report errors as warnings; a registered draft must remain successful.
func FinalizeCityLibraryReservation(ctx context.Context, receiptPath string, commit bool) error {
	raw, err := os.ReadFile(receiptPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt CityLibraryReservation
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	return finalizeCityReservation(ctx, &receipt, commit)
}

// EnsureCityLibraryReservation restores the exact clips of a retained workspace
// when registration is retried after an earlier failure released its reservation.
func EnsureCityLibraryReservation(ctx context.Context, receiptPath string) error {
	raw, err := os.ReadFile(receiptPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt CityLibraryReservation
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if receipt.ProjectID == "" || receipt.CatalogPath == "" {
		return fmt.Errorf("城市素材预占回执缺少 project_id 或 catalog_path")
	}
	db, err := openCityCatalog(ctx, receipt.CatalogPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	return withCityTransaction(ctx, db, func(conn *sql.Conn) error {
		var status, saved string
		if err := conn.QueryRowContext(ctx, "SELECT status,plan FROM projects WHERE project_id=?", receipt.ProjectID).Scan(&status, &saved); err != nil {
			return err
		}
		if status == "completed" {
			return nil
		}
		var plan CityLibraryReservation
		if err := json.Unmarshal([]byte(saved), &plan); err != nil {
			return err
		}
		if status == "reserved" {
			return checkCityReservationSets(ctx, conn, receipt.ProjectID, plan.Clips, receipt.Clips)
		}
		if status != "cancelled" {
			return fmt.Errorf("城市素材项目状态 %q 不支持恢复预占", status)
		}
		want, err := cityClipIDs(plan.Clips)
		if err != nil {
			return err
		}
		got, err := cityClipIDs(receipt.Clips)
		if err != nil {
			return err
		}
		if !slices.Equal(want, got) {
			return fmt.Errorf("预占计划与任务回执的 asset_id 集合不一致")
		}
		assets, err := cityAssets(ctx, conn, plan.Account)
		if err != nil {
			return err
		}
		byID := make(map[string]cityAsset, len(assets))
		for _, asset := range assets {
			byID[asset.id] = asset
		}
		for _, clip := range plan.Clips {
			asset, ok := byID[clip.AssetID]
			lo, hi := asset.interval()
			if !ok || asset.status != "eligible" || asset.uses+asset.reserved > 5 || asset.height > asset.width || asset.path != clip.Path || clip.SourceIn < lo || clip.SourceOut > hi || clip.SourceOut <= clip.SourceIn {
				return fmt.Errorf("素材 %s 已不可预占，无法注册原草稿，请新建任务", clip.AssetID)
			}
			if info, err := os.Stat(asset.path); err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("素材 %s 文件已不可用，无法恢复预占", clip.AssetID)
			}
		}
		if _, err := conn.ExecContext(ctx, "UPDATE projects SET status='reserved' WHERE project_id=?", receipt.ProjectID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM reservations WHERE project_id=?", receipt.ProjectID); err != nil {
			return err
		}
		for _, clip := range plan.Clips {
			if _, err := conn.ExecContext(ctx, "INSERT INTO reservations(project_id,asset_id) VALUES(?,?)", receipt.ProjectID, clip.AssetID); err != nil {
				return err
			}
		}
		return nil
	})
}

func finalizeCityReservation(ctx context.Context, receipt *CityLibraryReservation, commit bool) error {
	if receipt.ProjectID == "" || receipt.CatalogPath == "" {
		return fmt.Errorf("城市素材预占回执缺少 project_id 或 catalog_path")
	}
	db, err := openCityCatalog(ctx, receipt.CatalogPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	return withCityTransaction(ctx, db, func(conn *sql.Conn) error {
		var status string
		var raw sql.NullString
		if err := conn.QueryRowContext(ctx, "SELECT status,plan FROM projects WHERE project_id=?", receipt.ProjectID).Scan(&status, &raw); err != nil {
			return err
		}
		if status == "completed" || (!commit && status == "cancelled") {
			return nil
		}
		if status != "reserved" {
			return fmt.Errorf("城市素材项目不是 reserved 状态: %s", status)
		}
		if commit {
			var stored CityLibraryReservation
			if err := json.Unmarshal([]byte(raw.String), &stored); err != nil {
				return err
			}
			if err := checkCityReservationSets(ctx, conn, receipt.ProjectID, stored.Clips, receipt.Clips); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, "INSERT INTO uses(project_id,asset_id) SELECT project_id,asset_id FROM reservations WHERE project_id=?", receipt.ProjectID); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM reservations WHERE project_id=?", receipt.ProjectID); err != nil {
			return err
		}
		next := "cancelled"
		if commit {
			next = "completed"
		}
		_, err := conn.ExecContext(ctx, "UPDATE projects SET status=? WHERE project_id=?", next, receipt.ProjectID)
		return err
	})
}
