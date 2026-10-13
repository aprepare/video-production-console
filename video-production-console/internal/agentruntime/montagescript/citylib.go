package montagescript

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/agentruntime/montageplan"
)

// The Python executor still requires an index file in its machine profile.
// Override only its task-local copy; the frozen manifest and trusted profile stay intact.
func prepareCityScriptManifest(manifestPath, planPath string) (string, error) {
	if mixPresetFromManifest(manifestPath) != "scenic_semantic" {
		return manifestPath, nil
	}
	var manifest map[string]any
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	settings, ok := manifest["non_secret_settings"].(map[string]any)
	if !ok {
		return manifestPath, nil
	}
	value := func(m map[string]any, key string) string { s, _ := m[key].(string); return strings.TrimSpace(s) }
	root, index := value(settings, "media_root"), value(settings, "media_index_path")
	profilePath := value(settings, "machine_profile_path")
	var profile map[string]any
	loadProfile := func() error {
		data, err := os.ReadFile(profilePath)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &profile)
	}
	if root == "" && profilePath != "" {
		if err := loadProfile(); err != nil {
			return "", err
		}
		root = value(profile, "media_root")
		if index == "" {
			index = value(profile, "media_index_path")
		}
	}
	if root == "" {
		return manifestPath, nil
	}
	catalog, err := montageplan.CityLibraryPathForPlan(index, root, planPath)
	if err != nil {
		return "", err
	}
	if catalog == "" {
		return manifestPath, nil
	}
	if err := montageplan.ValidateMediaLibraryForPreset("scenic_semantic", index, root, ""); err != nil {
		return "", err
	}
	if profile == nil {
		if err := loadProfile(); err != nil {
			return "", err
		}
	}
	if profile == nil {
		return "", fmt.Errorf("machine profile must be an object")
	}
	profile["media_root"], profile["media_index_path"] = root, catalog
	output := filepath.Dir(planPath)
	derivedProfile := filepath.Join(output, "citylib_machine_profile.json")
	if err := writeCityScriptJSON(derivedProfile, profile); err != nil {
		return "", err
	}
	settings["media_root"], settings["media_index_path"], settings["machine_profile_path"] = root, catalog, derivedProfile
	derivedManifest := filepath.Join(output, "citylib_task_manifest.json")
	if err := writeCityScriptJSON(derivedManifest, manifest); err != nil {
		return "", err
	}
	return derivedManifest, nil
}

func writeCityScriptJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}

func scriptResultCompleted(raw []byte) bool {
	var result struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(raw, &result) == nil && result.Status == "completed"
}

func finishCityTask(receiptPath, outPath string, awaitingRegistration bool) {
	if _, err := os.Stat(receiptPath); os.IsNotExist(err) {
		return
	}
	var warnings []string
	if !awaitingRegistration {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		err := montageplan.FinalizeCityLibraryReservation(ctx, receiptPath, false)
		cancel()
		if err != nil {
			warnings = append(warnings, "城市素材预占释放失败: "+err.Error())
		}
	}
	for _, warning := range warnings {
		log.Printf("citylib warning: %s", warning)
	}
	for _, path := range []string{outPath, filepath.Join(filepath.Dir(receiptPath), "result.json"), receiptPath} {
		if len(warnings) == 0 {
			break
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var envelope map[string]any
		if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
			continue
		}
		items, _ := envelope["warnings"].([]any)
		for _, warning := range warnings {
			items = append(items, warning)
		}
		envelope["warnings"] = items
		if err := writeCityScriptJSON(path, envelope); err != nil {
			log.Printf("citylib warning output: %v", err)
		}
	}
}
