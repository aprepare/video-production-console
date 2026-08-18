package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type toolHost struct {
	skillRoot    string
	outputDir    string
	manifestPath string
	python       string
	inputPaths   map[string]bool
}

func newToolHost(skillRoot, outputDir, manifestPath, python string) *toolHost {
	h := &toolHost{
		skillRoot:    filepath.Clean(skillRoot),
		outputDir:    filepath.Clean(outputDir),
		manifestPath: filepath.Clean(manifestPath),
		python:       python,
		inputPaths:   map[string]bool{},
	}
	h.loadInputPaths()
	return h
}

func (h *toolHost) loadInputPaths() {
	raw, err := os.ReadFile(h.manifestPath)
	if err != nil {
		return
	}
	var manifest struct {
		Inputs []struct {
			Path string `json:"path"`
		} `json:"inputs"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return
	}
	for _, input := range manifest.Inputs {
		if abs, err := absClean(input.Path); err == nil {
			h.inputPaths[strings.ToLower(abs)] = true
		}
	}
}

func toolSpecs() []ToolSpec {
	return []ToolSpec{
		{
			Type: "function",
			Function: ToolSpecFunc{
				Name:        "read_file",
				Description: "Read a UTF-8 text file under the skill root, output_dir, or the task manifest path.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolSpecFunc{
				Name:        "write_file",
				Description: "Write a UTF-8 text file inside output_dir only.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolSpecFunc{
				Name:        "run_python",
				Description: "Run a python script that already exists under skill_root/scripts with optional args.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"script": map[string]any{"type": "string", "description": "Relative path under skill scripts/, e.g. validate_publishing_package.py"},
						"args":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"script"},
				},
			},
		},
	}
}

func (h *toolHost) Dispatch(name, arguments string) (string, error) {
	switch name {
	case "read_file":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(arguments), &in); err != nil {
			return "", err
		}
		path, err := h.resolveReadable(in.Path)
		if err != nil {
			return "", err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if len(raw) > 512*1024 {
			return "", fmt.Errorf("file exceeds 512KiB read limit")
		}
		return string(raw), nil
	case "write_file":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(arguments), &in); err != nil {
			return "", err
		}
		path, err := h.resolveWritable(in.Path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
			return "", err
		}
		return "ok", nil
	case "run_python":
		var in struct {
			Script string   `json:"script"`
			Args   []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(arguments), &in); err != nil {
			return "", err
		}
		scriptPath, err := h.resolveScript(in.Script)
		if err != nil {
			return "", err
		}
		args := append([]string{scriptPath}, in.Args...)
		cmd := exec.Command(h.python, args...)
		out, err := cmd.CombinedOutput()
		text := strings.TrimSpace(string(out))
		if err != nil {
			if text == "" {
				return "", err
			}
			return text, fmt.Errorf("%w", err)
		}
		if text == "" {
			return "ok", nil
		}
		return text, nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (h *toolHost) resolveReadable(path string) (string, error) {
	abs, err := absClean(path)
	if err != nil {
		return "", err
	}
	if samePath(abs, h.manifestPath) || within(h.skillRoot, abs) || within(h.outputDir, abs) || h.inputPaths[strings.ToLower(abs)] {
		return abs, nil
	}
	return "", fmt.Errorf("path not readable in sandbox: %s", path)
}

func (h *toolHost) resolveWritable(path string) (string, error) {
	abs, err := absClean(path)
	if err != nil {
		return "", err
	}
	if !within(h.outputDir, abs) {
		return "", fmt.Errorf("write path must be inside output_dir")
	}
	return abs, nil
}

func (h *toolHost) resolveScript(script string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(script)))
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("script must be a relative path under scripts/")
	}
	if !strings.HasPrefix(filepath.ToSlash(rel), "scripts/") {
		rel = filepath.Join("scripts", rel)
	}
	abs := filepath.Join(h.skillRoot, rel)
	if !within(filepath.Join(h.skillRoot, "scripts"), abs) {
		return "", fmt.Errorf("script escaped skill scripts directory")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("script unavailable: %s", rel)
	}
	return abs, nil
}

func absClean(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(path, `\\?\`) {
		path = path[4:]
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return abs, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
