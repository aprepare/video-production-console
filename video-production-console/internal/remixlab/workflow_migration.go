package remixlab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var workflowMigrationMu sync.Mutex

// MigrateExistingAccountWorkflows 在升级时固定老账号继承的全局工作流。
// 完成标记使后续启动不会把新账号的通用默认再次改成财经工作流。
func (s Store) MigrateExistingAccountWorkflows(accountIDs []string) error {
	workflowMigrationMu.Lock()
	defer workflowMigrationMu.Unlock()
	if strings.TrimSpace(s.DataRoot) == "" {
		return fmt.Errorf("migrate account workflows: data root is required")
	}
	dir := filepath.Join(filepath.Clean(s.DataRoot), "remix_lab")
	marker := filepath.Join(dir, "workflow_style_migration.json")
	if raw, err := os.ReadFile(marker); err == nil {
		var state struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(raw, &state); err != nil || state.Version != 1 {
			return fmt.Errorf("migrate account workflows: invalid completion marker")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read workflow migration marker: %w", err)
	}
	var missing []string
	for _, accountID := range accountIDs {
		id, err := normalizeWorkflowAccountID(accountID)
		if err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("%w: 迁移账号ID不能为空", ErrInvalidWorkflow)
		}
		path := s.accountWorkflowPath(id)
		if info, err := os.Stat(path); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("migrate account workflow: %s is not a file", id)
			}
		} else if os.IsNotExist(err) {
			missing = append(missing, path)
		} else {
			return fmt.Errorf("stat account workflow: %w", err)
		}
	}
	if len(missing) > 0 {
		// 已有全局文件逐字复制，保留冻结提示词、空规则及旧字段。
		raw, err := os.ReadFile(s.workflowPath())
		if os.IsNotExist(err) {
			w, loadErr := s.LoadWorkflow()
			if loadErr != nil {
				return loadErr
			}
			raw, err = json.MarshalIndent(w, "", "  ")
		} else if err == nil {
			_, err = decodeWorkflow(raw)
		}
		if err != nil {
			return fmt.Errorf("load migration workflow: %w", err)
		}
		for _, path := range missing {
			if err := createMigrationFile(path, raw); err != nil {
				return err
			}
		}
	}
	return createMigrationFile(marker, []byte("{\"version\":1}\n"))
}

// 先完整写入临时文件再建链接；目标已存在时不覆盖，失败不会留下半份配置。
func createMigrationFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".workflow-migration-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(raw); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if os.IsExist(err) {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				return nil
			}
		}
		return fmt.Errorf("publish workflow migration: %w", err)
	}
	return nil
}
