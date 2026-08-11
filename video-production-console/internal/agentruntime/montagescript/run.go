package montagescript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"video-production-console/internal/agentruntime/montageplan"
)

// Options configures the local montage script orchestration.
type Options struct {
	ManifestPath       string
	SkillRoot          string
	OutputLastMessage  string
	PythonBinary       string
	Duration           montageplan.DurationFunc
	CommandRunner      func(name string, args ...string) ([]byte, error)
}

// Run validates inputs, builds a deterministic plan, executes the skill script,
// and writes a result envelope to OutputLastMessage.
func Run(opts Options) error {
	manifestPath := strings.TrimSpace(opts.ManifestPath)
	skillRoot := strings.TrimSpace(opts.SkillRoot)
	outPath := strings.TrimSpace(opts.OutputLastMessage)
	if manifestPath == "" || skillRoot == "" || outPath == "" {
		return fmt.Errorf("manifest, skill-root, and output-last-message are required")
	}
	script := filepath.Join(skillRoot, "scripts", "run_montage_job.py")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("montage script unavailable: %w", err)
	}
	python := strings.TrimSpace(opts.PythonBinary)
	if python == "" {
		python = "python"
	}
	run := opts.CommandRunner
	if run == nil {
		run = func(name string, args ...string) ([]byte, error) {
			cmd := exec.Command(name, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
			}
			return out, nil
		}
	}
	durationFn := opts.Duration
	if durationFn == nil {
		durationFn = func(path string) (float64, error) {
			if value, err := montageplan.ProbeDuration(path); err == nil {
				return value, nil
			}
			return probeDurationViaSkill(python, skillRoot, path)
		}
	}

	if _, err := run(python, script, "validate-inputs", "--manifest", manifestPath); err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("validate-inputs: %w", err))
	}

	planPath, err := planPathFromManifest(manifestPath)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	if err := montageplan.Build(montageplan.Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		Duration:     durationFn,
	}); err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("build plan: %w", err))
	}

	if _, err := run(python, script, "validate-plan", "--manifest", manifestPath, "--plan", planPath); err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("validate-plan: %w", err))
	}

	raw, err := run(python, script, "execute", "--manifest", manifestPath, "--plan", planPath)
	resultFile := filepath.Join(filepath.Dir(planPath), "result.json")
	if fileRaw, readErr := os.ReadFile(resultFile); readErr == nil {
		if writeErr := writeRawEnvelope(outPath, fileRaw); writeErr == nil {
			return nil
		}
	}
	if err != nil {
		if writeErr := writeRawEnvelope(outPath, raw); writeErr == nil {
			return nil
		}
		return writeFailure(outPath, manifestPath, fmt.Errorf("execute: %w", err))
	}
	if err := writeRawEnvelope(outPath, raw); err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	return nil
}

func probeDurationViaSkill(pythonBinary, skillRoot, audioPath string) (float64, error) {
	// Prefer pyJianYingDraft/AudioMaterial duration so project_duration_s matches
	// the same microsecond length the draft backend will enforce.
	code := "" +
		"import sys\n" +
		"path = sys.argv[1]\n" +
		"try:\n" +
		"    from pyJianYingDraft.local_materials import AudioMaterial\n" +
		"    print(AudioMaterial(path).duration / 1_000_000)\n" +
		"except Exception:\n" +
		"    from pathlib import Path\n" +
		"    sys.path.insert(0, r'''" + filepath.Join(skillRoot, "scripts") + "''')\n" +
		"    from run_montage_job import audio_duration_seconds\n" +
		"    print(audio_duration_seconds(Path(path)))\n"
	cmd := exec.Command(pythonBinary, "-c", code, audioPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("python duration probe: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var value float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &value); err != nil {
		return 0, fmt.Errorf("parse python duration: %w (%q)", err, strings.TrimSpace(string(out)))
	}
	if value <= 0 {
		return 0, fmt.Errorf("python duration probe returned non-positive value")
	}
	return value, nil
}

func planPathFromManifest(manifestPath string) (string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read manifest: %w", err)
	}
	var manifest struct {
		OutputDir string `json:"output_dir"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("decode manifest: %w", err)
	}
	if strings.TrimSpace(manifest.OutputDir) == "" {
		return "", fmt.Errorf("manifest output_dir is required")
	}
	return filepath.Join(manifest.OutputDir, "production_plan.json"), nil
}

func writeRawEnvelope(path string, raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return fmt.Errorf("empty script result")
	}
	// CombinedOutput may include logs before the final JSON object.
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return fmt.Errorf("script result is not JSON")
	}
	candidate := []byte(trimmed[start : end+1])
	var probe map[string]any
	if err := json.Unmarshal(candidate, &probe); err != nil {
		// Windows Python may emit GBK text inside JSON; rewrite as UTF-8.
		decoded, decErr := decodeBestEffortJSON(candidate)
		if decErr != nil {
			return fmt.Errorf("decode script result: %w", err)
		}
		probe = decoded
	}
	if _, ok := probe["schema_version"]; !ok {
		return fmt.Errorf("script result missing schema_version")
	}
	normalizeEnvelopePaths(probe)
	normalized, err := json.Marshal(probe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, normalized, 0o644)
}

func normalizeEnvelopePaths(probe map[string]any) {
	strip := func(value string) string {
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, `\\?\`) {
			return trimmed[4:]
		}
		return trimmed
	}
	if artifacts, ok := probe["artifacts"].([]any); ok {
		for _, item := range artifacts {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := obj["path"].(string); ok {
				obj["path"] = strip(path)
			}
		}
	}
	if assets, ok := probe["asset_outputs"].([]any); ok {
		for _, item := range assets {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := obj["path"].(string); ok {
				obj["path"] = strip(path)
			}
		}
	}
}

func decodeBestEffortJSON(raw []byte) (map[string]any, error) {
	for _, decode := range []func([]byte) ([]byte, error){
		func(b []byte) ([]byte, error) { return b, nil },
		decodeGBK,
	} {
		converted, err := decode(raw)
		if err != nil {
			continue
		}
		var probe map[string]any
		if json.Unmarshal(converted, &probe) == nil {
			return probe, nil
		}
	}
	return nil, fmt.Errorf("unsupported script result encoding")
}

func decodeGBK(raw []byte) ([]byte, error) {
	// Minimal GBK→UTF-8 via PowerShell-free table is heavy; use python when needed.
	cmd := exec.Command("python", "-c", "import sys; sys.stdout.buffer.write(sys.stdin.buffer.read().decode('gb18030','replace').encode('utf-8'))")
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func writeFailure(outPath, manifestPath string, cause error) error {
	taskID, action := identityFromManifest(manifestPath)
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         action,
		"status":         "failed",
		"summary":        cause.Error(),
		"questions":      []any{},
		"artifacts":      []any{},
		"asset_outputs":  []any{},
		"warnings":       []any{},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return cause
	}
	if writeErr := os.MkdirAll(filepath.Dir(outPath), 0o755); writeErr != nil {
		return fmt.Errorf("%v; also failed to create output dir: %w", cause, writeErr)
	}
	if writeErr := os.WriteFile(outPath, raw, 0o644); writeErr != nil {
		return fmt.Errorf("%v; also failed to write envelope: %w", cause, writeErr)
	}
	// Envelope written: caller should exit 0 so Runner classifies via status.
	return nil
}

func identityFromManifest(path string) (taskID, action string) {
	action = "montage.execute"
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", action
	}
	var manifest struct {
		TaskID string `json:"task_id"`
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return "", action
	}
	if manifest.Action != "" {
		action = manifest.Action
	}
	return manifest.TaskID, action
}
