package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxSteps = 24

// Options configures the OpenAI-compatible remix/topic runner.
type Options struct {
	ManifestPath      string
	SkillRoot         string
	OutputLastMessage string
	Model             string
	BaseURL           string
	APIKey            string
	MaxSteps          int
	PythonBinary      string
	Client            ChatClient
}

type manifestLite struct {
	TaskID    string `json:"task_id"`
	Action    string `json:"action"`
	Skill     string `json:"skill"`
	OutputDir string `json:"output_dir"`
}

// Run executes a bounded tool-calling loop and writes the result envelope.
func Run(opts Options) error {
	manifestPath := strings.TrimSpace(opts.ManifestPath)
	skillRoot := strings.TrimSpace(opts.SkillRoot)
	outPath := strings.TrimSpace(opts.OutputLastMessage)
	if manifestPath == "" || skillRoot == "" || outPath == "" {
		return fmt.Errorf("manifest, skill-root, and output-last-message are required")
	}
	baseURL := strings.TrimSpace(opts.BaseURL)
	apiKey := strings.TrimSpace(opts.APIKey)
	if baseURL == "" || apiKey == "" {
		return writeFailure(outPath, manifestPath, fmt.Errorf("openai base url and api key are required"))
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	python := strings.TrimSpace(opts.PythonBinary)
	if python == "" {
		python = "python"
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

	skillMD, _ := os.ReadFile(filepath.Join(skillRoot, "SKILL.md"))
	contract, _ := os.ReadFile(filepath.Join(skillRoot, "references", "console-contract.md"))
	system := buildSystemPrompt(string(skillMD), string(contract))
	user := fmt.Sprintf(
		"Execute console action using the authoritative task manifest at:\n%s\n\nWrite all outputs under output_dir from the manifest. When finished, write exactly one result envelope to output_dir/result.json and stop.",
		manifestPath,
	)

	client := opts.Client
	if client == nil {
		client = &HTTPChatClient{BaseURL: baseURL, APIKey: apiKey}
	}
	tools := newToolHost(skillRoot, manifest.OutputDir, manifestPath, python)
	messages := []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	var lastErr error
	for step := 0; step < maxSteps; step++ {
		resp, err := client.Chat(ChatRequest{
			Model:    model,
			Messages: messages,
			Tools:    toolSpecs(),
		})
		if err != nil {
			lastErr = err
			break
		}
		if len(resp.Choices) == 0 {
			lastErr = fmt.Errorf("empty chat choices")
			break
		}
		msg := resp.Choices[0].Message
		messages = append(messages, msg)
		if len(msg.ToolCalls) == 0 {
			break
		}
		for _, call := range msg.ToolCalls {
			result, toolErr := tools.Dispatch(call.Function.Name, call.Function.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf("error: %v", toolErr)
			}
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
		if _, err := os.Stat(filepath.Join(manifest.OutputDir, "result.json")); err == nil {
			break
		}
	}

	resultFile := filepath.Join(manifest.OutputDir, "result.json")
	if fileRaw, err := os.ReadFile(resultFile); err == nil {
		if writeErr := writeEnvelope(outPath, fileRaw); writeErr == nil {
			return nil
		}
	}
	if lastErr != nil {
		return writeFailure(outPath, manifestPath, lastErr)
	}
	return writeFailure(outPath, manifestPath, fmt.Errorf("model finished without writing output_dir/result.json"))
}

func buildSystemPrompt(skillMD, contract string) string {
	var b strings.Builder
	b.WriteString("You are the console skill worker for finance remix/topic tasks.\n")
	b.WriteString("Follow the skill and console contract exactly. Use tools to read inputs and write outputs.\n")
	b.WriteString("Do not invent absolute paths outside the declared roots.\n")
	b.WriteString("Final deliverable must be output_dir/result.json matching schema_version 2.0.\n\n")
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
