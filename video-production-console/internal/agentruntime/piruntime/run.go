package piruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options configures the Pi remix/topic runner.
type Options struct {
	ManifestPath      string
	SkillRoot         string
	OutputLastMessage string
	Model             string
	PiBinary          string
	LookPath          func(file string) (string, error)
	RunCommand        func(name string, args []string, stdin string, dir string) ([]byte, error)
}

type manifestLite struct {
	TaskID    string `json:"task_id"`
	Action    string `json:"action"`
	Skill     string `json:"skill"`
	OutputDir string `json:"output_dir"`
}

// Run assembles a skill+manifest prompt, invokes the local pi CLI, and writes
// the console result envelope to output-last-message.
func Run(opts Options) error {
	manifestPath := strings.TrimSpace(opts.ManifestPath)
	skillRoot := strings.TrimSpace(opts.SkillRoot)
	outPath := strings.TrimSpace(opts.OutputLastMessage)
	if manifestPath == "" || skillRoot == "" || outPath == "" {
		return fmt.Errorf("manifest, skill-root, and output-last-message are required")
	}

	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("read manifest: %w", err))
	}
	var manifest manifestLite
	if err := json.Unmarshal(stripBOM(rawManifest), &manifest); err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("decode manifest: %w", err))
	}
	if strings.TrimSpace(manifest.OutputDir) == "" {
		return writeFailure(outPath, manifestPath, fmt.Errorf("manifest output_dir is required"))
	}
	if err := os.MkdirAll(manifest.OutputDir, 0o755); err != nil {
		return writeFailure(outPath, manifestPath, err)
	}

	piPath, err := resolvePiBinary(opts)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}

	skillMD, _ := os.ReadFile(filepath.Join(skillRoot, "SKILL.md"))
	contract, _ := os.ReadFile(filepath.Join(skillRoot, "references", "console-contract.md"))
	prompt := buildPrompt(string(skillMD), string(contract), manifestPath, manifest.OutputDir)

	args := []string{"-p"}
	model := strings.TrimSpace(opts.Model)
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "Execute the console skill task described on stdin. Prefer writing output_dir/result.json.")

	run := opts.RunCommand
	if run == nil {
		run = defaultRunCommand
	}
	workdir := filepath.Dir(manifestPath)
	if _, err := run(piPath, args, prompt, workdir); err != nil {
		if fileRaw, readErr := os.ReadFile(filepath.Join(manifest.OutputDir, "result.json")); readErr == nil {
			if writeErr := writeEnvelope(outPath, fileRaw); writeErr == nil {
				return nil
			}
		}
		return writeFailure(outPath, manifestPath, fmt.Errorf("pi run failed: %w", err))
	}

	resultFile := filepath.Join(manifest.OutputDir, "result.json")
	if fileRaw, err := os.ReadFile(resultFile); err == nil {
		if writeErr := writeEnvelope(outPath, fileRaw); writeErr == nil {
			return nil
		}
	}
	return writeFailure(outPath, manifestPath, fmt.Errorf("pi finished without writing output_dir/result.json"))
}

func resolvePiBinary(opts Options) (string, error) {
	if explicit := strings.TrimSpace(opts.PiBinary); explicit != "" {
		return explicit, nil
	}
	look := opts.LookPath
	if look == nil {
		look = exec.LookPath
	}
	path, err := look("pi")
	if err != nil {
		return "", fmt.Errorf("pi binary not found on PATH: %w", err)
	}
	return path, nil
}

func buildPrompt(skillMD, contract, manifestPath, outputDir string) string {
	var b strings.Builder
	b.WriteString("You are the console skill worker for finance remix/topic tasks.\n")
	b.WriteString("Follow the skill and console contract exactly.\n")
	b.WriteString("Do not invent absolute paths outside the declared roots.\n")
	b.WriteString("Final deliverable must be output_dir/result.json matching schema_version 2.0.\n\n")
	b.WriteString("Authoritative task manifest:\n")
	b.WriteString(manifestPath)
	b.WriteString("\n\nRequired output_dir:\n")
	b.WriteString(outputDir)
	b.WriteString("\n\n")
	if strings.TrimSpace(contract) != "" {
		b.WriteString("# Console contract\n")
		b.WriteString(contract)
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(skillMD) != "" {
		b.WriteString("# Skill\n")
		b.WriteString(skillMD)
	}
	return b.String()
}

func defaultRunCommand(name string, args []string, stdin string, dir string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, detail)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func stripBOM(raw []byte) []byte {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}

func writeEnvelope(path string, raw []byte) error {
	trimmed := strings.TrimSpace(string(stripBOM(raw)))
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return fmt.Errorf("result is not JSON")
	}
	candidate := []byte(trimmed[start : end+1])
	var probe map[string]any
	if err := json.Unmarshal(candidate, &probe); err != nil {
		return err
	}
	if _, ok := probe["schema_version"]; !ok {
		return fmt.Errorf("result missing schema_version")
	}
	normalizePaths(probe)
	encoded, err := json.Marshal(probe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func normalizePaths(probe map[string]any) {
	strip := func(value string) string {
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, `\\?\`) {
			return trimmed[4:]
		}
		return trimmed
	}
	for _, key := range []string{"artifacts", "asset_outputs"} {
		items, ok := probe[key].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
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
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("%v; also failed to create output dir: %w", cause, err)
	}
	if err := os.WriteFile(outPath, raw, 0o644); err != nil {
		return fmt.Errorf("%v; also failed to write envelope: %w", cause, err)
	}
	return nil
}

func identityFromManifest(path string) (taskID, action string) {
	action = "remix.standard"
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", action
	}
	var manifest manifestLite
	if json.Unmarshal(stripBOM(raw), &manifest) != nil {
		return "", action
	}
	if manifest.Action != "" {
		action = manifest.Action
	}
	return manifest.TaskID, action
}
